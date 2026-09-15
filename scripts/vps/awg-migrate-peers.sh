#!/bin/bash
# Rebuild the AmneziaWG server config from a directory of CLIENT configs.
#
#   awg-migrate-peers.sh <dir-with-client-confs> [server-public-ip]
#
# Use case: the old server (and its private key) is gone, but every client
# config we ever issued is mirrored on the Mac. Each client keeps its own
# private key, preshared key, address and the shared obfuscation "magic";
# only the server key and the Endpoint change. The script:
#   1. generates a server key if /etc/amnezia/amneziawg/server_private.key is missing
#   2. derives each peer's public key from the client conf's PrivateKey
#   3. writes awg0.conf (peers named after the conf files) and starts awg-quick@awg0
#   4. writes re-pointed client confs + QR PNGs to /root/awg-clients/
# Re-running with the same inputs is a no-op for the server key; peers are rebuilt.
set -euo pipefail
IN="${1:?dir with client *.conf}"
PUBLIC_IP="${2:-$(ip -o -4 addr show dev "$(ip -o route show default | awk '{print $5; exit}')" scope global | awk '{print $4; exit}' | cut -d/ -f1)}"
NIC=$(ip -o route show default | awk '{print $5; exit}')
PORT=13337
SUBNET=10.100.0.0/24
SERVER_ADDR=10.100.0.1/24
CFG=/etc/amnezia/amneziawg
OUT=/root/awg-clients
command -v awg >/dev/null || { echo "awg not installed — run awg-install.sh" >&2; exit 1; }

install -d -m 700 "$CFG" "$OUT"
if [ ! -f "$CFG/server_private.key" ]; then
  umask 077; awg genkey > "$CFG/server_private.key"; umask 022
fi
SERVER_PRIV=$(cat "$CFG/server_private.key")
SERVER_PUB=$(echo "$SERVER_PRIV" | awg pubkey)
echo "$SERVER_PUB" > "$CFG/server_public.key"

conf_get() { sed -nE "s/^\s*$2\s*=\s*(.*)\s*$/\1/p" "$1" | head -1; }

first=$(ls "$IN"/*.conf | head -1)
[ -n "$first" ] || { echo "no *.conf in $IN" >&2; exit 1; }
MAGIC=""
for k in Jc Jmin Jmax S1 S2 H1 H2 H3 H4; do
  v=$(conf_get "$first" "$k"); [ -n "$v" ] || { echo "$first lacks $k" >&2; exit 1; }
  MAGIC+="$k = $v"$'\n'
done

{
  echo "[Interface]"
  echo "Address = $SERVER_ADDR"
  echo "ListenPort = $PORT"
  echo "PrivateKey = $SERVER_PRIV"
  printf '%s' "$MAGIC"
  echo "PostUp = iptables -t nat -A POSTROUTING -s $SUBNET -o $NIC -j MASQUERADE; iptables -A INPUT -p udp --dport $PORT -j ACCEPT; iptables -A FORWARD -i awg0 -j ACCEPT; iptables -A FORWARD -o awg0 -j ACCEPT"
  echo "PostDown = iptables -t nat -D POSTROUTING -s $SUBNET -o $NIC -j MASQUERADE; iptables -D INPUT -p udp --dport $PORT -j ACCEPT; iptables -D FORWARD -i awg0 -j ACCEPT; iptables -D FORWARD -o awg0 -j ACCEPT"
} > "$CFG/awg0.conf.new"

n=0
for f in "$IN"/*.conf; do
  name=$(basename "$f" .conf)
  priv=$(conf_get "$f" PrivateKey); addr=$(conf_get "$f" Address); psk=$(conf_get "$f" PresharedKey)
  dns=$(conf_get "$f" DNS); mtu=$(conf_get "$f" MTU)
  [ -n "$priv" ] && [ -n "$addr" ] || { echo "skip $name: no PrivateKey/Address" >&2; continue; }
  pub=$(echo "$priv" | awg pubkey)
  ip_only=${addr%%/*}
  {
    echo; echo "# $name"; echo "[Peer]"; echo "PublicKey = $pub"
    [ -n "$psk" ] && echo "PresharedKey = $psk"
    echo "AllowedIPs = $ip_only/32"
  } >> "$CFG/awg0.conf.new"
  # Client side: same identity, new server key + endpoint.
  {
    echo "[Interface]"; echo "PrivateKey = $priv"; echo "Address = $addr"
    echo "DNS = ${dns:-9.9.9.9, 8.8.8.8}"; echo "MTU = ${mtu:-1280}"
    printf '%s' "$MAGIC"
    echo; echo "[Peer]"; echo "PublicKey = $SERVER_PUB"
    [ -n "$psk" ] && echo "PresharedKey = $psk"
    echo "AllowedIPs = 0.0.0.0/0, ::/0"; echo "Endpoint = $PUBLIC_IP:$PORT"; echo "PersistentKeepalive = 25"
  } > "$OUT/$name.conf"
  chmod 600 "$OUT/$name.conf"
  qrencode -t PNG -o "$OUT/$name.png" < "$OUT/$name.conf"
  # keep the private material on the server too, like the old layout
  umask 077; echo "$priv" > "$CFG/${name}_private.key"; echo "$pub" > "$CFG/${name}_public.key"
  [ -n "$psk" ] && echo "$psk" > "$CFG/${name}_psk.key"; umask 022
  n=$((n+1))
done
mv "$CFG/awg0.conf.new" "$CFG/awg0.conf"; chmod 600 "$CFG/awg0.conf"

systemctl enable awg-quick@awg0 >/dev/null 2>&1 || true
if systemctl is-active --quiet awg-quick@awg0; then
  awg syncconf awg0 <(awg-quick strip awg0)
else
  systemctl restart awg-quick@awg0
fi
echo "awg0 up on $PUBLIC_IP:$PORT, server pubkey $SERVER_PUB, $n peers; client confs + QR in $OUT"
awg show awg0 | head -5
