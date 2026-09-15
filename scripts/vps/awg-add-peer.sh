#!/bin/bash
# Add one AmneziaWG peer on the exit, hot (no restart, other peers keep their
# handshakes). Usage:
#   awg-add-peer.sh <name> <ip-last-octet> "<who>" [bridge-ip]
# Writes /root/awg-clients/<name>.conf + .png (direct endpoint) and, with a
# bridge IP, /root/awg-clients/via-ru/<name>.conf + .png (same keys, endpoint
# on the RU bridge — for ISPs that drop data to the exit's subnet).
# The "# who" comment in awg0.conf is the only record of who owns which key.
set -euo pipefail
NAME="${1:?name, e.g. misha-client22}"; LAST="${2:?last octet, e.g. 23}"; WHO="${3:?who}"; BRIDGE="${4:-}"
CFG=/etc/amnezia/amneziawg; OUT=/root/awg-clients; PORT=13337
PUBLIC_IP=$(ip -o -4 addr show dev "$(ip -o route show default | awk '{print $5; exit}')" scope global | awk '{print $4; exit}' | cut -d/ -f1)
cd "$CFG"
[ -f "${NAME}_private.key" ] && { echo "peer $NAME already exists" >&2; exit 1; }
grep -q "AllowedIPs = 10.100.0.$LAST/32" awg0.conf && { echo "10.100.0.$LAST already taken" >&2; exit 1; }
umask 077
awg genkey | tee "${NAME}_private.key" | awg pubkey > "${NAME}_public.key"
awg genpsk > "${NAME}_psk.key"
umask 022
SERVER_PUB=$(cat server_public.key)
conf_get() { sed -nE "s/^\s*$2\s*=\s*(.*)\s*$/\1/p" "$1" | head -1; }
MAGIC=""; for k in Jc Jmin Jmax S1 S2 H1 H2 H3 H4; do MAGIC+="$k = $(conf_get awg0.conf "$k")"$'\n'; done
{
  echo; echo "# $WHO"; echo "[Peer]"
  echo "PublicKey = $(cat "${NAME}_public.key")"
  echo "PresharedKey = $(cat "${NAME}_psk.key")"
  echo "AllowedIPs = 10.100.0.$LAST/32"
} >> awg0.conf
awg syncconf awg0 <(awg-quick strip awg0)
write_client() { # $1 = endpoint host, $2 = out file
  {
    echo "[Interface]"; echo "PrivateKey = $(cat "${NAME}_private.key")"; echo "Address = 10.100.0.$LAST/32"
    echo "DNS = 9.9.9.9, 8.8.8.8"; echo "MTU = 1280"; printf '%s' "$MAGIC"
    echo; echo "[Peer]"; echo "PublicKey = $SERVER_PUB"; echo "PresharedKey = $(cat "${NAME}_psk.key")"
    echo "AllowedIPs = 0.0.0.0/0, ::/0"; echo "Endpoint = $1:$PORT"; echo "PersistentKeepalive = 25"
  } > "$2"; chmod 600 "$2"; qrencode -t PNG -o "${2%.conf}.png" < "$2"
}
install -d -m 700 "$OUT"; write_client "$PUBLIC_IP" "$OUT/$NAME.conf"
if [ -n "$BRIDGE" ]; then install -d -m 700 "$OUT/via-ru"; write_client "$BRIDGE" "$OUT/via-ru/$NAME.conf"; fi
echo "peer $NAME ($WHO) = 10.100.0.$LAST added; confs+QR in $OUT${BRIDGE:+ and $OUT/via-ru}"
awg show awg0 peers | wc -l | sed 's/^/peers now: /'
