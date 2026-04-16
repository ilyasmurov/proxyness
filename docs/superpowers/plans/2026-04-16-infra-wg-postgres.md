# Infra: WireGuard Aeza↔Timeweb + Postgres on Aeza Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Poднять WireGuard туннель между Aeza и Timeweb, установить Postgres 16 на Aeza слушающий только на WG-интерфейсе, подготовить пустую БД `proxyness` с пользователем для приложений.

**Architecture:** WireGuard on port 51820/udp — Aeza is the "server" (10.8.0.1), Timeweb is the "peer" (10.8.0.2). Postgres listens only on 10.8.0.1:5432, access from Timeweb (10.8.0.2) + localhost. Public internet cannot reach Postgres. Credentials stored in `/etc/proxyness/db.env` on both VPSs (read by app systemd/docker env).

**Tech Stack:** WireGuard (kernel module, wg-quick), Postgres 16 (apt from pgdg), Ubuntu (both VPSs). Pure ops — no code changes in this plan.

**Out of scope (other plans):** Schema + data migration (plan 2), admin extraction (plan 3), proxy-server switch to Postgres (plan 4), rollout (plan 5).

**Coordinates:**
- Aeza: `95.181.162.242`, WG IP `10.8.0.1`
- Timeweb: `82.97.246.65`, WG IP `10.8.0.2`
- WG port: `51820/udp`
- Postgres: `10.8.0.1:5432`
- DB name: `proxyness`
- DB app user: `proxyness` (not superuser)

---

## Task 1: Install WireGuard on both VPSs

**Files:** (none — package install)

- [ ] **Step 1: SSH into Aeza and install wireguard**

Run on local machine:
```bash
ssh root@95.181.162.242 'apt update && apt install -y wireguard wireguard-tools && wg --version'
```
Expected: `wireguard-tools v1.0.x` printed, no errors.

- [ ] **Step 2: SSH into Timeweb and install wireguard**

```bash
ssh root@82.97.246.65 'apt update && apt install -y wireguard wireguard-tools && wg --version'
```
Expected: same version printed.

- [ ] **Step 3: Verify kernel module loads on both**

```bash
ssh root@95.181.162.242 'modprobe wireguard && lsmod | grep wireguard'
ssh root@82.97.246.65 'modprobe wireguard && lsmod | grep wireguard'
```
Expected: `wireguard` line present on both. If missing, the host kernel is too old — stop and investigate before continuing.

## Task 2: Generate WireGuard keys

**Files:**
- Create: `/etc/wireguard/private.key` (on each VPS, mode 0600)
- Create: `/etc/wireguard/public.key` (on each VPS)

- [ ] **Step 1: Generate keys on Aeza**

```bash
ssh root@95.181.162.242 'umask 077 && cd /etc/wireguard && wg genkey | tee private.key | wg pubkey > public.key && echo "AEZA_PRIV=$(cat private.key)" && echo "AEZA_PUB=$(cat public.key)"'
```
Expected: two base64 strings printed. **Copy both values to your local notes** — you need AEZA_PUB in Task 3 step 2 and AEZA_PRIV goes into Aeza's config.

- [ ] **Step 2: Generate keys on Timeweb**

```bash
ssh root@82.97.246.65 'umask 077 && cd /etc/wireguard && wg genkey | tee private.key | wg pubkey > public.key && echo "TW_PRIV=$(cat private.key)" && echo "TW_PUB=$(cat public.key)"'
```
Expected: two base64 strings. Copy both to notes.

- [ ] **Step 3: Verify key file permissions**

```bash
ssh root@95.181.162.242 'stat -c "%a %n" /etc/wireguard/*.key'
ssh root@82.97.246.65 'stat -c "%a %n" /etc/wireguard/*.key'
```
Expected: both `private.key` files are `600`. If not, `chmod 600 /etc/wireguard/private.key` on the offending host.

## Task 3: Write WireGuard configs

**Files:**
- Create: `/etc/wireguard/wg0.conf` on Aeza
- Create: `/etc/wireguard/wg0.conf` on Timeweb

- [ ] **Step 1: Write Aeza config**

Substitute `<AEZA_PRIV>` and `<TW_PUB>` from Task 2 notes. Run locally:
```bash
ssh root@95.181.162.242 'cat > /etc/wireguard/wg0.conf <<EOF
[Interface]
Address = 10.8.0.1/24
ListenPort = 51820
PrivateKey = <AEZA_PRIV>

[Peer]
# Timeweb
PublicKey = <TW_PUB>
AllowedIPs = 10.8.0.2/32
PersistentKeepalive = 25
EOF
chmod 600 /etc/wireguard/wg0.conf'
```
Expected: no output, command succeeds.

- [ ] **Step 2: Write Timeweb config**

Substitute `<TW_PRIV>` and `<AEZA_PUB>`:
```bash
ssh root@82.97.246.65 'cat > /etc/wireguard/wg0.conf <<EOF
[Interface]
Address = 10.8.0.2/24
PrivateKey = <TW_PRIV>

[Peer]
# Aeza
PublicKey = <AEZA_PUB>
Endpoint = 95.181.162.242:51820
AllowedIPs = 10.8.0.1/32
PersistentKeepalive = 25
EOF
chmod 600 /etc/wireguard/wg0.conf'
```
Expected: no output. Note no `ListenPort` — Timeweb is a client-style peer.

- [ ] **Step 3: Verify configs parse**

```bash
ssh root@95.181.162.242 'wg-quick strip wg0 >/dev/null && echo OK'
ssh root@82.97.246.65 'wg-quick strip wg0 >/dev/null && echo OK'
```
Expected: `OK` on both. Any parse error means a typo — fix and retry.

## Task 4: Open WireGuard port on Aeza firewall

**Files:** (firewall rules — whatever the host uses)

- [ ] **Step 1: Check current firewall state on Aeza**

```bash
ssh root@95.181.162.242 'ufw status 2>/dev/null; iptables -L INPUT -n -v | head -20'
```
Note which firewall is active (ufw / raw iptables / none). Aeza currently allows 443 TCP+UDP for the proxy container — we're adding 51820/udp.

- [ ] **Step 2: Allow 51820/udp from Timeweb IP only**

If ufw:
```bash
ssh root@95.181.162.242 'ufw allow from 82.97.246.65 to any port 51820 proto udp && ufw reload'
```
If raw iptables:
```bash
ssh root@95.181.162.242 'iptables -I INPUT -p udp --dport 51820 -s 82.97.246.65 -j ACCEPT && iptables-save > /etc/iptables/rules.v4'
```
If no firewall: skip. Expected: rule added, no error.

## Task 5: Bring up the tunnel

**Files:** (systemd)

- [ ] **Step 1: Enable + start wg0 on Aeza first**

```bash
ssh root@95.181.162.242 'systemctl enable --now wg-quick@wg0 && systemctl status wg-quick@wg0 --no-pager | head -15'
```
Expected: `active (exited)`, `wg-quick up wg0` logs show `[#] ip link add wg0 type wireguard` etc.

- [ ] **Step 2: Enable + start wg0 on Timeweb**

```bash
ssh root@82.97.246.65 'systemctl enable --now wg-quick@wg0 && systemctl status wg-quick@wg0 --no-pager | head -15'
```
Expected: same.

- [ ] **Step 3: Verify handshake**

```bash
ssh root@95.181.162.242 'wg show wg0'
ssh root@82.97.246.65 'wg show wg0'
```
Expected: `latest handshake: X seconds ago` (under ~30s) on both sides. `transfer: N B received, M B sent` with non-zero values. If handshake is "never" after 60s, check the firewall rule (Task 4) and that endpoint IP+port is correct on Timeweb.

- [ ] **Step 4: Verify L3 connectivity**

```bash
ssh root@82.97.246.65 'ping -c 3 10.8.0.1'
ssh root@95.181.162.242 'ping -c 3 10.8.0.2'
```
Expected: 0% packet loss, RTT ~5-10ms (both VPSs are Amsterdam).

## Task 6: Install Postgres 16 on Aeza

**Files:**
- Modify: `/etc/postgresql/16/main/postgresql.conf`
- Modify: `/etc/postgresql/16/main/pg_hba.conf`

- [ ] **Step 1: Add pgdg repo and install**

```bash
ssh root@95.181.162.242 'install -d /usr/share/postgresql-common/pgdg && curl -fsS https://www.postgresql.org/media/keys/ACCC4CF8.asc -o /usr/share/postgresql-common/pgdg/apt.postgresql.org.asc && echo "deb [signed-by=/usr/share/postgresql-common/pgdg/apt.postgresql.org.asc] https://apt.postgresql.org/pub/repos/apt $(lsb_release -cs)-pgdg main" > /etc/apt/sources.list.d/pgdg.list && apt update && apt install -y postgresql-16 && pg_lsclusters'
```
Expected: `16 main  5432 online postgres ...` line. Default cluster is running.

- [ ] **Step 2: Bind Postgres to WG interface only**

```bash
ssh root@95.181.162.242 "sed -i \"s/^#*listen_addresses.*/listen_addresses = '127.0.0.1,10.8.0.1'/\" /etc/postgresql/16/main/postgresql.conf && grep ^listen_addresses /etc/postgresql/16/main/postgresql.conf"
```
Expected: `listen_addresses = '127.0.0.1,10.8.0.1'`. Postgres will NOT listen on 95.181.162.242 — we verify in step 5.

- [ ] **Step 3: Allow connections from Timeweb's WG IP**

Append to pg_hba.conf (SCRAM-SHA-256 for password auth over the trusted tunnel):
```bash
ssh root@95.181.162.242 "cat >> /etc/postgresql/16/main/pg_hba.conf <<EOF

# Proxyness app access via WireGuard
host    proxyness    proxyness    10.8.0.2/32    scram-sha-256
host    proxyness    proxyness    127.0.0.1/32   scram-sha-256
EOF"
```
Expected: no output. Verify with `tail -5 /etc/postgresql/16/main/pg_hba.conf`.

- [ ] **Step 4: Restart Postgres**

```bash
ssh root@95.181.162.242 'systemctl restart postgresql@16-main && systemctl status postgresql@16-main --no-pager | head -10'
```
Expected: `active (running)`. If it fails to start, `journalctl -u postgresql@16-main -n 50` to diagnose (usually pg_hba.conf typo).

- [ ] **Step 5: Verify listen addresses**

```bash
ssh root@95.181.162.242 'ss -tlnp | grep 5432'
```
Expected: two lines — `127.0.0.1:5432` and `10.8.0.1:5432`. There must NOT be a `0.0.0.0:5432` or `95.181.162.242:5432` line. If there is, step 2 didn't take — re-check the sed.

## Task 7: Create database and application user

**Files:**
- Create: `/etc/proxyness/db.env` on Aeza (mode 0600)
- Create: `/etc/proxyness/db.env` on Timeweb (mode 0600)

- [ ] **Step 1: Generate a strong password locally**

```bash
openssl rand -base64 32
```
Expected: 44-char base64 string. **Save to your password manager** — we'll paste it into both VPSs.

- [ ] **Step 2: Create role and database on Aeza**

Substitute `<PW>` with the generated password:
```bash
ssh root@95.181.162.242 "sudo -u postgres psql -v ON_ERROR_STOP=1 <<EOF
CREATE ROLE proxyness WITH LOGIN PASSWORD '<PW>';
CREATE DATABASE proxyness OWNER proxyness;
\l proxyness
EOF"
```
Expected: `CREATE ROLE`, `CREATE DATABASE`, then a row listing the `proxyness` DB with owner `proxyness`.

- [ ] **Step 3: Write db.env on Aeza (for local proxy-server + admin later)**

```bash
ssh root@95.181.162.242 'install -d -m 0755 /etc/proxyness && cat > /etc/proxyness/db.env <<EOF
PROXYNESS_DB_URL=postgres://proxyness:<PW>@10.8.0.1:5432/proxyness?sslmode=disable
EOF
chmod 600 /etc/proxyness/db.env'
```
Expected: no output. `sslmode=disable` is OK because the link is already WireGuard-encrypted; adding TLS on top would double-encrypt for zero benefit.

- [ ] **Step 4: Write db.env on Timeweb**

Same URL (note: still `10.8.0.1`, because the DB lives on Aeza and Timeweb reaches it via the tunnel):
```bash
ssh root@82.97.246.65 'install -d -m 0755 /etc/proxyness && cat > /etc/proxyness/db.env <<EOF
PROXYNESS_DB_URL=postgres://proxyness:<PW>@10.8.0.1:5432/proxyness?sslmode=disable
EOF
chmod 600 /etc/proxyness/db.env'
```
Expected: no output.

## Task 8: End-to-end connectivity verification

**Files:** (none — verification only)

- [ ] **Step 1: Install psql client on Timeweb**

```bash
ssh root@82.97.246.65 'apt install -y postgresql-client-common postgresql-client && psql --version'
```
Expected: `psql (PostgreSQL) 16.x` or close. (If Ubuntu ships an older client, any psql 14+ works as a client against a 16 server.)

- [ ] **Step 2: Connect from Timeweb to Aeza's Postgres**

```bash
ssh root@82.97.246.65 'source /etc/proxyness/db.env && psql "$PROXYNESS_DB_URL" -c "SELECT version(), inet_server_addr(), inet_client_addr();"'
```
Expected: one row. `inet_server_addr` = `10.8.0.1`, `inet_client_addr` = `10.8.0.2`. Proves the connection went through the tunnel.

- [ ] **Step 3: Connect from Aeza to Postgres locally**

```bash
ssh root@95.181.162.242 'source /etc/proxyness/db.env && psql "$PROXYNESS_DB_URL" -c "SELECT current_user, current_database();"'
```
Expected: `proxyness | proxyness`.

- [ ] **Step 4: Verify public internet CANNOT reach Postgres**

From your local machine:
```bash
nc -zv 95.181.162.242 5432
```
Expected: connection refused OR timeout. If it connects, `listen_addresses` is misconfigured — go back to Task 6 step 2.

- [ ] **Step 5: Verify Timeweb CANNOT reach Postgres via public IP either**

```bash
ssh root@82.97.246.65 'nc -zv 95.181.162.242 5432'
```
Expected: refused / timeout. Only the 10.8.0.1 path works.

## Task 9: Persistence + reboot safety

**Files:** (systemd verification only)

- [ ] **Step 1: Confirm wg-quick and postgresql are enabled**

```bash
ssh root@95.181.162.242 'systemctl is-enabled wg-quick@wg0 postgresql@16-main'
ssh root@82.97.246.65 'systemctl is-enabled wg-quick@wg0'
```
Expected: `enabled` on each line.

- [ ] **Step 2: Document the setup in CLAUDE.md**

Add a new section under "## Deployment" → after the VPS subsection, append a "**Shared infra (Aeza)**" bullet list describing: WG tunnel 10.8.0.0/24, Postgres 16 on 10.8.0.1:5432 only, db.env location, credentials in password manager. One paragraph, ~5 lines. (Exact wording left to the executor; the goal is that the next session doesn't rediscover any of this from scratch.)

Commit:
```bash
git add CLAUDE.md
git commit -m "docs: record WG tunnel + Postgres infra on Aeza"
```

## Task 10: Final verification

- [ ] **Step 1: Re-run end-to-end query from Timeweb**

```bash
ssh root@82.97.246.65 'source /etc/proxyness/db.env && psql "$PROXYNESS_DB_URL" -c "CREATE TABLE _ping (id int); DROP TABLE _ping; SELECT '\''ok'\'' AS status;"'
```
Expected: `status = ok`. Confirms the `proxyness` user has DDL rights on the `proxyness` DB (owner).

- [ ] **Step 2: Record handshake stats as a smoke baseline**

```bash
ssh root@95.181.162.242 'wg show wg0 | grep -E "(latest handshake|transfer)"'
```
Note the values. If you come back tomorrow and handshake is "never" or transfer counters are stale, the tunnel died — first thing to check.

---

## Done criteria

- `wg show wg0` on both VPSs shows recent handshake + non-zero transfer
- `psql` from Timeweb to `10.8.0.1:5432` authenticates as `proxyness` and runs `SELECT 1`
- `nc -zv 95.181.162.242 5432` from anywhere outside the tunnel fails
- `/etc/proxyness/db.env` exists on both VPSs, mode 0600
- CLAUDE.md documents the new infra

At this point the plumbing is ready. Plan 2 (schema + data migration) can start.
