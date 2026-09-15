#!/bin/bash
# Copy the provisioning kit to a VPS and run it. Usage:
#   scripts/vps/push.sh <ssh-alias> [provision|sync-only]
# Uploads scripts/vps, scripts/systemd, scripts/vnstat-export.sh and the
# Postgres schema to /root/proxyness-setup on the host, then runs
# vps/provision.sh there unless the second argument is sync-only.
# tar over ssh, not rsync: a fresh Debian image has no rsync yet.
set -euo pipefail
HOST="${1:?ssh alias or user@host}"
MODE="${2:-provision}"
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
STAGE=$(mktemp -d); trap 'rm -rf "$STAGE"' EXIT
mkdir -p "$STAGE/proxyness-setup"
cp -R "$ROOT/scripts/vps" "$ROOT/scripts/systemd" "$ROOT/scripts/vnstat-export.sh" "$STAGE/proxyness-setup/"
cp "$ROOT/server/internal/db/pg/schema.sql" "$STAGE/proxyness-setup/schema.sql"
tar -C "$STAGE" -czf - proxyness-setup | ssh "$HOST" 'rm -rf /root/proxyness-setup && tar -C /root -xzf - && echo "kit at /root/proxyness-setup"'
[ "$MODE" = sync-only ] && exit 0
ssh -t "$HOST" 'bash /root/proxyness-setup/vps/provision.sh'
