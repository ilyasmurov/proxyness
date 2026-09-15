#!/bin/bash
# Proxyness control plane on a SHARED docker host (the Ansamblist box, PRXNS-22).
#
# The control plane runs the same four containers as an exit (proxy binary for
# the admin API + /api/sync, config service, admin SPA, landing) but carries no
# VPN traffic: its address is never in the client's server list. What differs
# from provision.sh: no host nginx (the box's own Caddy routes the domains),
# no certbot (Caddy issues certs), no AWG, no sysctl — and Postgres runs as a
# container on port 5433 so nothing on the host is touched. Idempotent.
#
#   scripts/vps/push.sh <alias> sync-only && ssh <alias> bash /root/proxyness-setup/vps/control-plane-shared.sh
set -euo pipefail
PG_PORT="${PG_PORT:-5433}"
SETUP_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
log() { printf '\n\033[1;34m==> %s\033[0m\n' "$*"; }
[ "$(id -u)" = 0 ] || { echo "run as root" >&2; exit 1; }
command -v docker >/dev/null || { echo "docker missing" >&2; exit 1; }

log "volumes"
for v in proxyness-data proxyness-config-data proxyness-pg-data; do
  docker volume inspect "$v" >/dev/null 2>&1 || docker volume create "$v" >/dev/null
done

install -d -m 700 /etc/proxyness
DB_ENV=/etc/proxyness/db.env
if [ -f "$DB_ENV" ]; then
  DB_PASS=$(sed -nE 's|^PROXYNESS_DB_URL=postgres://proxyness:([^@]+)@.*|\1|p' "$DB_ENV")
else
  DB_PASS=$(openssl rand -hex 16)
fi

log "postgres 16 container on 127.0.0.1:$PG_PORT + 172.17.0.1:$PG_PORT"
if ! docker ps -a --format '{{.Names}}' | grep -qx proxyness-pg; then
  docker run -d --name proxyness-pg --restart unless-stopped \
    -e POSTGRES_USER=proxyness -e POSTGRES_PASSWORD="$DB_PASS" -e POSTGRES_DB=proxyness \
    -p "127.0.0.1:$PG_PORT:5432" -p "172.17.0.1:$PG_PORT:5432" \
    -v proxyness-pg-data:/var/lib/postgresql/data \
    postgres:16-alpine >/dev/null
else
  docker start proxyness-pg >/dev/null 2>&1 || true
fi
# The official image runs a temporary server during first init and then
# restarts — pg_isready answers for that throwaway instance too. Wait for a
# real query to succeed twice in a row, 2s apart.
ok=0
for i in $(seq 1 60); do
  if docker exec proxyness-pg psql -U proxyness -d proxyness -Atc 'select 1' >/dev/null 2>&1; then
    ok=$((ok+1)); [ $ok -ge 2 ] && break
  else
    ok=0
  fi
  sleep 2
done
[ $ok -ge 2 ] || { echo "postgres not ready" >&2; exit 1; }
# keep the password in step with db.env on re-runs
docker exec proxyness-pg psql -U proxyness -d proxyness -q -c "ALTER ROLE proxyness WITH PASSWORD '$DB_PASS' CREATEDB;" >/dev/null
docker exec -i proxyness-pg psql -U proxyness -d proxyness -q -v ON_ERROR_STOP=1 < "$SETUP_DIR/schema.sql" >/dev/null
umask 077
printf 'PROXYNESS_DB_URL=postgres://proxyness:%s@172.17.0.1:%s/proxyness?sslmode=disable\n' "$DB_PASS" "$PG_PORT" > "$DB_ENV"
umask 022
chmod 600 "$DB_ENV"

# The proxy container terminates its own TLS on 4430; Caddy in front holds the
# public cert and skips verification of this self-signed one.
VOL=$(docker volume inspect proxyness-data --format '{{ .Mountpoint }}')
if [ ! -f "$VOL/cert.pem" ]; then
  log "self-signed cert for the proxy container"
  openssl req -x509 -newkey rsa:2048 -nodes -days 3650 -subj "/CN=proxyness.smurov.com" \
    -keyout "$VOL/key.pem" -out "$VOL/cert.pem" >/dev/null 2>&1
  chmod 600 "$VOL/key.pem"; chmod 644 "$VOL/cert.pem"
fi

log "done"
cat <<SUMMARY
  postgres   container proxyness-pg, db proxyness, creds in $DB_ENV
  volumes    proxyness-data (cert placeholder), proxyness-config-data, proxyness-pg-data
  next       1) CI key in /root/.ssh/authorized_keys, secrets VPS3_HOST/VPS3_SSH_KEY
             2) deploy workflows (target "control": landing on 8082)
             3) Caddy: proxyness.smurov.com → /api,/admin,/health → https://172.17.0.1:4430, rest → :8082;
                admin.proxyness.smurov.com → 172.17.0.1:8081   (deploy/Caddyfile.ansamblist)
             4) DNS → this box; exits: PRIMARY_SSH=<this box>, PRIMARY_PSQL="docker exec -i proxyness-pg psql -U proxyness -d proxyness"
SUMMARY
