#!/bin/bash
# Export vnstat statistics into the proxyness data volume.
#
# The proxy container has no view of the host's NIC, so the admin dashboard
# cannot measure the uplink itself. This drops vnstat's JSON where the container
# already has it mounted (/data), no container changes required.
#
# Installed on the host as /usr/local/sbin/vnstat-export.sh, run every 5 minutes
# by vnstat-export.timer.
set -euo pipefail

DEST=${1:-/var/lib/docker/volumes/proxyness-data/_data/vnstat.json}
TMP="${DEST}.tmp"

# Write to a temporary file and rename: the server may read this at any moment,
# and rename within one filesystem is atomic, so it never sees a half-written file.
vnstat --json > "$TMP"
chmod 644 "$TMP"
mv -f "$TMP" "$DEST"
