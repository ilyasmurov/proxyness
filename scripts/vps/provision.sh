#!/bin/bash
# Proxyness VPS provisioning. Idempotent — safe to re-run on a host that is
# already half-set-up. Run as root on a fresh Debian 12/13 (Ubuntu 22.04+
# mostly works, but Debian is what prod runs).
#
#   scripts/vps/push.sh <ssh-alias>            # rsync + run from your Mac
#   /root/proxyness-setup/vps/provision.sh     # or by hand on the host
#
# What it sets up (mirrors docs/claude/deploy.md "Shared infra"):
#   swap · docker + volumes · Postgres 16 (pgdg) with schema and db.env ·
#   host nginx SNI router on :443 (port 80 left free for the landing container)
#   · certbot + renewal hooks · sysctl (TCP backlog, BBR, ip_forward) ·
#   vnstat + export timer · sysstat · AmneziaWG (kernel module + tools) ·
#   pg-sync timer (installed, disabled) · SSH key-only login.
#
# What it does NOT do: start the app containers (that is the deploy
# workflows' job — run all four via workflow_dispatch after this), issue
# Let's Encrypt certs (DNS has to point here first: scripts/vps/issue-certs.sh),
# or configure AWG peers (scripts/vps/awg-migrate-peers.sh).
set -euo pipefail

HOSTNAME_WANTED="${PROXYNESS_HOSTNAME:-proxyness-aeza}"
SKIP_AWG="${SKIP_AWG:-0}"
SETUP_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"   # …/proxyness-setup (holds vps/, systemd/, schema.sql)

log()  { printf '\n\033[1;34m==> %s\033[0m\n' "$*"; }
warn() { printf '\033[1;33m!!  %s\033[0m\n' "$*" >&2; }

[ "$(id -u)" = 0 ] || { echo "run as root" >&2; exit 1; }
export DEBIAN_FRONTEND=noninteractive

. /etc/os-release
DISTRO_ID="$ID"; CODENAME="${VERSION_CODENAME:-}"
NIC=$(ip -o route show default | awk '{print $5; exit}')
PUBLIC_IP=$(ip -o -4 addr show dev "$NIC" scope global | awk '{print $4; exit}' | cut -d/ -f1)
log "host: $DISTRO_ID $CODENAME, kernel $(uname -r), nic $NIC, ip $PUBLIC_IP"

# ---------------------------------------------------------------- hostname
if [ "$(hostname)" != "$HOSTNAME_WANTED" ]; then
  log "hostname → $HOSTNAME_WANTED"
  hostnamectl set-hostname "$HOSTNAME_WANTED"
fi
grep -qE "^127\.0\.1\.1\s+$HOSTNAME_WANTED" /etc/hosts || echo "127.0.1.1 $HOSTNAME_WANTED" >> /etc/hosts

# ---------------------------------------------------------------- swap
# A docker pull of the proxy image OOM-kills a 1 GB box without swap.
if ! swapon --show | grep -q .; then
  RAM_MB=$(awk '/MemTotal/{print int($2/1024)}' /proc/meminfo)
  if [ "$RAM_MB" -lt 4096 ]; then
    log "swap: 2G swapfile (RAM ${RAM_MB} MiB)"
    fallocate -l 2G /swapfile && chmod 600 /swapfile && mkswap /swapfile >/dev/null && swapon /swapfile
    grep -q '^/swapfile' /etc/fstab || echo '/swapfile none swap sw 0 0' >> /etc/fstab
  fi
fi

# ---------------------------------------------------------------- packages
log "apt packages"
apt-get update -qq
apt-get install -y -qq --no-install-recommends \
  ca-certificates curl gnupg lsb-release git jq openssl rsync \
  nginx libnginx-mod-stream certbot \
  vnstat sysstat iptables qrencode \
  build-essential dkms pkg-config "linux-headers-$(uname -r)" \
  postgresql-common >/dev/null
# Debian's default site listens on [::]:80 — crashes nginx on IPv4-only
# hosts and would steal port 80 from the landing container anyway.
rm -f /etc/nginx/sites-enabled/default

# ---------------------------------------------------------------- docker
if ! command -v docker >/dev/null; then
  log "docker (get.docker.com)"
  curl -fsSL https://get.docker.com | sh >/dev/null
fi
systemctl enable --now docker >/dev/null
for v in proxyness-data proxyness-config-data; do
  docker volume inspect "$v" >/dev/null 2>&1 || docker volume create "$v" >/dev/null
done

# ---------------------------------------------------------------- sysctl
log "sysctl"
install -m 644 "$SETUP_DIR/systemd/99-proxyness-tcp.conf" /etc/sysctl.d/99-proxyness-tcp.conf
cat > /etc/sysctl.d/98-proxyness-net.conf <<'SYSCTL'
# Forwarding for AmneziaWG peers + BBR/fq for the proxy relay.
net.ipv4.ip_forward = 1
net.core.default_qdisc = fq
net.ipv4.tcp_congestion_control = bbr
SYSCTL
sysctl --system >/dev/null

# ---------------------------------------------------------------- postgres 16
log "postgres 16"
if ! command -v pg_lsclusters >/dev/null || ! pg_lsclusters 2>/dev/null | grep -q '^16 '; then
  /usr/share/postgresql-common/pgdg/apt.postgresql.org.sh -y >/dev/null
  apt-get install -y -qq postgresql-16 >/dev/null
fi
PGCONF=/etc/postgresql/16/main/postgresql.conf
PGHBA=/etc/postgresql/16/main/pg_hba.conf
# Loopback for psql/tests, docker bridge gateway for the proxy container.
# The container reaches the host DB only through 172.17.0.1.
sed -i -E "s|^#?listen_addresses\s*=.*|listen_addresses = '127.0.0.1,172.17.0.1'|" "$PGCONF"
grep -qE '^host\s+all\s+all\s+172\.17\.0\.0/16\s+scram-sha-256' "$PGHBA" || \
  echo 'host    all             all             172.17.0.0/16           scram-sha-256' >> "$PGHBA"
# 172.17.0.1 only exists once docker0 is up — order Postgres after docker or
# it binds loopback only after a reboot and the proxy container crash-loops.
install -d /etc/systemd/system/postgresql@16-main.service.d
cat > /etc/systemd/system/postgresql@16-main.service.d/wait-for-docker.conf <<'UNIT'
[Unit]
After=docker.service
Wants=docker.service
UNIT
systemctl daemon-reload
systemctl enable postgresql@16-main >/dev/null 2>&1 || true
systemctl restart postgresql@16-main

install -d -m 700 /etc/proxyness
DB_ENV=/etc/proxyness/db.env
if [ -f "$DB_ENV" ]; then
  DB_PASS=$(sed -nE 's|^PROXYNESS_DB_URL=postgres://proxyness:([^@]+)@.*|\1|p' "$DB_ENV")
else
  DB_PASS=$(openssl rand -hex 16)
fi
[ -n "$DB_PASS" ] || { echo "could not determine DB password" >&2; exit 1; }
su - postgres -c "psql -v ON_ERROR_STOP=1 -q" <<SQL
DO \$\$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'proxyness') THEN
    CREATE ROLE proxyness LOGIN CREATEDB PASSWORD '$DB_PASS';
  ELSE
    ALTER ROLE proxyness WITH LOGIN CREATEDB PASSWORD '$DB_PASS';
  END IF;
END \$\$;
SQL
su - postgres -c "psql -Atc \"SELECT 1 FROM pg_database WHERE datname='proxyness'\"" | grep -q 1 || \
  su - postgres -c "createdb -O proxyness proxyness"
# Apply as the app role so tables are owned by proxyness, not postgres.
install -m 644 "$SETUP_DIR/schema.sql" /tmp/proxyness-schema.sql
su - postgres -c "psql -v ON_ERROR_STOP=1 -q -d proxyness -c 'SET ROLE proxyness' -f /tmp/proxyness-schema.sql" >/dev/null
rm -f /tmp/proxyness-schema.sql
umask 077
printf 'PROXYNESS_DB_URL=postgres://proxyness:%s@172.17.0.1:5432/proxyness?sslmode=disable\n' "$DB_PASS" > "$DB_ENV"
umask 022
chmod 600 "$DB_ENV"

# ---------------------------------------------------------------- certs (placeholders)
# The proxy transport never verifies the cert (InsecureSkipVerify + its own
# UDP crypto), so a self-signed placeholder lets the container boot before
# DNS points here. issue-certs.sh swaps in Let's Encrypt via the deploy hook.
VOL=$(docker volume inspect proxyness-data --format '{{ .Mountpoint }}')
if [ ! -f "$VOL/cert.pem" ]; then
  log "self-signed placeholder cert for the proxy container"
  openssl req -x509 -newkey rsa:2048 -nodes -days 3650 -subj "/CN=proxyness.smurov.com" \
    -keyout "$VOL/key.pem" -out "$VOL/cert.pem" >/dev/null 2>&1
  chmod 600 "$VOL/key.pem"; chmod 644 "$VOL/cert.pem"
fi
if [ ! -f /etc/nginx/ssl/admin/fullchain.pem ]; then
  log "self-signed placeholder cert for nginx (admin)"
  install -d -m 750 /etc/nginx/ssl/admin
  openssl req -x509 -newkey rsa:2048 -nodes -days 3650 -subj "/CN=admin.proxyness.smurov.com" \
    -keyout /etc/nginx/ssl/admin/privkey.pem -out /etc/nginx/ssl/admin/fullchain.pem >/dev/null 2>&1
  chmod 600 /etc/nginx/ssl/admin/privkey.pem
fi

# ---------------------------------------------------------------- nginx
log "nginx SNI router"
install -m 644 "$SETUP_DIR/vps/nginx.conf" /etc/nginx/nginx.conf
nginx -t
# restart, not reload: reload keeps old listen sockets in the master process.
systemctl enable nginx >/dev/null 2>&1 || true
systemctl restart nginx

# ---------------------------------------------------------------- certbot hooks
log "certbot renewal hooks"
for d in pre post deploy; do
  install -d "/etc/letsencrypt/renewal-hooks/$d"
  install -m 755 "$SETUP_DIR"/vps/certbot-hooks/$d/*.sh "/etc/letsencrypt/renewal-hooks/$d/"
done
systemctl enable --now certbot.timer >/dev/null 2>&1 || true

# ---------------------------------------------------------------- vnstat / sysstat
log "vnstat export + sysstat"
systemctl enable --now vnstat >/dev/null 2>&1 || true
install -m 755 "$SETUP_DIR/vnstat-export.sh" /usr/local/sbin/vnstat-export.sh
install -m 644 "$SETUP_DIR/systemd/vnstat-export.service" /etc/systemd/system/
install -m 644 "$SETUP_DIR/systemd/vnstat-export.timer" /etc/systemd/system/
sed -i 's/^ENABLED="false"/ENABLED="true"/' /etc/default/sysstat 2>/dev/null || true
systemctl daemon-reload
systemctl enable --now vnstat-export.timer >/dev/null
systemctl enable --now sysstat >/dev/null 2>&1 || true

# ---------------------------------------------------------------- pg-sync (installed, disabled)
log "pg-sync script + timer (disabled until this host becomes a secondary)"
install -m 755 "$SETUP_DIR/vps/pg-sync.sh" /usr/local/sbin/proxyness-pg-sync.sh
install -m 644 "$SETUP_DIR/vps/systemd/proxyness-pg-sync.service" /etc/systemd/system/
install -m 644 "$SETUP_DIR/vps/systemd/proxyness-pg-sync.timer" /etc/systemd/system/
install -m 755 "$SETUP_DIR/vps/cake-ingress.sh" /usr/local/sbin/cake-ingress.sh
install -m 644 "$SETUP_DIR/vps/systemd/cake-ingress.service" /etc/systemd/system/
systemctl daemon-reload

# ---------------------------------------------------------------- AmneziaWG
if [ "$SKIP_AWG" != 1 ]; then
  log "AmneziaWG kernel module + tools"
  bash "$SETUP_DIR/vps/awg-install.sh"
fi

# ---------------------------------------------------------------- SSH: key-only
# Only once a key is actually installed — otherwise we'd lock ourselves out.
if [ -s /root/.ssh/authorized_keys ]; then
  log "sshd: key-only root login"
  install -d /etc/ssh/sshd_config.d
  cat > /etc/ssh/sshd_config.d/50-proxyness.conf <<'SSHD'
PasswordAuthentication no
KbdInteractiveAuthentication no
PermitRootLogin prohibit-password
SSHD
  sshd -t && (systemctl reload ssh 2>/dev/null || systemctl reload sshd)
else
  warn "no /root/.ssh/authorized_keys — leaving password login enabled"
fi

log "done"
cat <<SUMMARY

  host        $HOSTNAME_WANTED ($PUBLIC_IP, nic $NIC)
  postgres    16, db proxyness, creds in $DB_ENV
  nginx       :443 SNI router → 4430 (proxy) / 8444→8081 (admin); :80 free
  docker      volumes proxyness-data, proxyness-config-data
  certs       self-signed placeholders — run vps/issue-certs.sh after DNS points here
  next        1) add the CI deploy key to /root/.ssh/authorized_keys
              2) set VPS2_HOST secret, run the four deploy workflows
              3) vps/issue-certs.sh   4) vps/awg-migrate-peers.sh <confs-dir>

SUMMARY
