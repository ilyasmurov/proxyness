#!/bin/bash
# Pull the account tables from the primary Proxyness host into the local DB.
# Runs on a SECONDARY host from proxyness-pg-sync.timer (every minute).
#
# Config: /etc/proxyness/pg-sync.env with
#   PRIMARY_SSH=<ssh alias or root@ip>   root there must accept this host's /root/.ssh/id_ed25519
#   PRIMARY_PSQL=<psql command on the primary>   default: runuser -u postgres -- psql -d proxyness
#     control plane on the shared box: docker exec -i proxyness-pg psql -U proxyness -d proxyness
#   LOCAL_PSQL=<psql command here>   default: host Postgres via runuser (must be a host psql —
#     the import reads CSV files from a temp dir, a container cannot see them)
#
# Tables: everything the admin panel edits — users, devices, sites,
# site_domains, site_ips, user_sites, changelog. NOT traffic_stats/logs: those
# are per-host measurements and must survive locally. Rows are upserted by
# primary key, then rows missing on the primary are deleted (child tables
# first). Deleting a device cascades its local traffic_stats — intended.
#
# Why pull over SSH instead of logical replication: no open Postgres port, no
# wal_level change on the primary, and a dead primary just means the last
# snapshot stays — which is exactly the failover behaviour we want.
set -euo pipefail
ENV=/etc/proxyness/pg-sync.env
[ -f "$ENV" ] || { echo "pg-sync: $ENV missing (PRIMARY_SSH=...)" >&2; exit 1; }
. "$ENV"
: "${PRIMARY_SSH:?PRIMARY_SSH not set}"
PRIMARY_PSQL="${PRIMARY_PSQL:-runuser -u postgres -- psql -d proxyness}"
LOCAL_PSQL="${LOCAL_PSQL:-runuser -u postgres -- psql -d proxyness}"
TABLES="users sites devices site_domains site_ips user_sites changelog"
TMP=$(mktemp -d); trap 'rm -rf "$TMP"' EXIT

for t in $TABLES; do
  ssh -o BatchMode=yes -o ConnectTimeout=10 "$PRIMARY_SSH" \
    "$PRIMARY_PSQL -Atc \"\\\\copy $t TO STDOUT WITH (FORMAT csv)\"" > "$TMP/$t.csv"
done

col_list() { $LOCAL_PSQL -Atc "SELECT string_agg(quote_ident(column_name), ', ' ORDER BY ordinal_position) FROM information_schema.columns WHERE table_name='$1'"; }
pk_list()  { $LOCAL_PSQL -Atc "SELECT string_agg(quote_ident(a.attname), ', ' ORDER BY array_position(i.indkey, a.attnum)) FROM pg_index i JOIN pg_attribute a ON a.attrelid=i.indrelid AND a.attnum=ANY(i.indkey) WHERE i.indrelid='$1'::regclass AND i.indisprimary"; }

SQL="$TMP/sync.sql"
echo "BEGIN;" > "$SQL"
for t in $TABLES; do
  cols=$(col_list "$t"); pk=$(pk_list "$t")
  set_clause=$(echo "$cols" | tr ',' '\n' | sed 's/^ *//' | grep -vxF -f <(echo "$pk" | tr ',' '\n' | sed 's/^ *//') | sed 's/.*/& = EXCLUDED.&/' | paste -sd, -)
  cat >> "$SQL" <<S
CREATE TEMP TABLE in_$t (LIKE $t INCLUDING DEFAULTS);
\\copy in_$t ($cols) FROM '$TMP/$t.csv' WITH (FORMAT csv)
INSERT INTO $t ($cols) SELECT $cols FROM in_$t
  ON CONFLICT ($pk) DO UPDATE SET $set_clause;
S
done
# delete orphans, children first
for t in changelog user_sites site_ips site_domains devices sites users; do
  pk=$(pk_list "$t")
  echo "DELETE FROM $t WHERE ($pk) NOT IN (SELECT $pk FROM in_$t);" >> "$SQL"
done
for t in users devices sites; do
  echo "SELECT setval(pg_get_serial_sequence('$t','id'), COALESCE((SELECT max(id) FROM $t), 1));" >> "$SQL"
done
echo "COMMIT;" >> "$SQL"
chmod 644 "$TMP"/*; chmod 755 "$TMP"
$LOCAL_PSQL -v ON_ERROR_STOP=1 -q -f "$SQL" >/dev/null
echo "pg-sync: $(date -Is) synced from $PRIMARY_SSH: $(for t in $TABLES; do printf '%s=%s ' "$t" "$(($(wc -l < "$TMP/$t.csv")))"; done)"
