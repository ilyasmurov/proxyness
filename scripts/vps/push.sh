#!/bin/bash
# Copy the provisioning kit to a VPS and run it. Usage:
#   scripts/vps/push.sh <ssh-alias> [provision|sync-only]
# Uploads scripts/ (vps/, systemd/, vnstat-export.sh) and the Postgres schema
# to /root/proxyness-setup on the host, then runs vps/provision.sh there
# unless the second argument is sync-only.
set -euo pipefail
HOST="${1:?ssh alias or user@host}"
MODE="${2:-provision}"
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
rsync -az --delete \
  "$ROOT/scripts/vps" "$ROOT/scripts/systemd" "$ROOT/scripts/vnstat-export.sh" \
  "$ROOT/server/internal/db/pg/schema.sql" \
  "$HOST":/root/proxyness-setup/
[ "$MODE" = sync-only ] && exit 0
ssh -t "$HOST" 'bash /root/proxyness-setup/vps/provision.sh'
