#!/bin/bash
# Redirect ingress of the uplink NIC into ifb0 and shape it with CAKE.
# Idempotent. Usage: cake-ingress.sh [<bandwidth>|off]   (default 45Mbit)
# Why ingress and not egress: the bloat measured on Serverspace was on the
# receive side (a provider policer), an egress qdisc changes nothing there.
set -euo pipefail
BW="${1:-45Mbit}"
NIC=$(ip -o route show default | awk '{print $5; exit}')
if [ "$BW" = off ]; then
  tc qdisc del dev "$NIC" ingress 2>/dev/null || true
  tc qdisc del dev ifb0 root 2>/dev/null || true
  ip link set ifb0 down 2>/dev/null || true
  exit 0
fi
modprobe ifb numifbs=1 2>/dev/null || true
ip link show ifb0 >/dev/null 2>&1 || ip link add ifb0 type ifb
ip link set ifb0 up
tc qdisc replace dev "$NIC" handle ffff: ingress
tc filter replace dev "$NIC" parent ffff: protocol all prio 1 u32 match u32 0 0 \
  action mirred egress redirect dev ifb0
tc qdisc replace dev ifb0 root cake bandwidth "$BW" besteffort
echo "cake ingress on $NIC → ifb0 @ $BW"
