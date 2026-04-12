#!/bin/bash
# Proxyness benchmark — compares VPN path vs direct (physical interface)
# across three destination categories so the numbers actually mean something
# from a Russian ISP:
#
#   - RU-ref (selectel SPb):  not blocked, native RU bandwidth baseline
#   - EU-ref (leaseweb DE):   not blocked direct + near our NL exit →
#                             cleanest proxy-overhead measurement
#   - Blocked (cloudflare):   heavily TSPU-throttled direct → shows VPN value,
#                             NOT overhead (comparing proxy/direct here
#                             would overstate proxy speed by ~1000×)
#
# Run periodically to track perf. Usage: ./scripts/benchmark.sh
#
# Note: macOS ships bash 3.2, so this script avoids bash-4 features
# (associative arrays, ${!var}, etc.).

echo "=== Proxyness Benchmark ==="
echo "Date: $(date '+%Y-%m-%d %H:%M:%S')"
echo ""

# Detect physical interface (the one that bypasses TUN routes).
# The helper pins a /32 route to the server IP via physical, so asking
# for that route always returns the physical iface — even when VPN is up
# and the default route points at utun. If VPN is off the /32 route
# doesn't exist and we fall back to the plain default route.
PHYS_IF=$(route -n get 95.181.162.242 2>/dev/null | awk '/interface:/{print $2}')
case "$PHYS_IF" in ""|utun*)
  PHYS_IF=$(route -n get default 2>/dev/null | awk '/interface:/{print $2}')
  ;;
esac
PHYS_IF=${PHYS_IF:-en0}

# External IP via both paths — confirms VPN is actually routing through NL
# and that --interface binds to the right card.
IP_VPN=$(curl -s --max-time 5 https://api.ipify.org 2>/dev/null || echo "N/A")
IP_DIR=$(curl --interface "$PHYS_IF" -s --max-time 5 https://api.ipify.org 2>/dev/null || echo "N/A")
echo "Physical interface: $PHYS_IF"
echo "External IP (VPN):    $IP_VPN"
echo "External IP (direct): $IP_DIR"
echo ""

# Ping
echo "--- Ping (10 packets) ---"
printf "%-15s %8s %8s %8s %8s %6s\n" "Target" "Min" "Avg" "Max" "Stddev" "Loss"
for target in 8.8.8.8 1.1.1.1 ya.ru; do
  result=$(ping -c 10 -W 3 "$target" 2>&1)
  loss=$(echo "$result" | grep -o '[0-9.]*% packet loss' | grep -o '[0-9.]*%')
  stats=$(echo "$result" | grep 'round-trip' | grep -oE '[0-9.]+/[0-9.]+/[0-9.]+/[0-9.]+')
  if [ -n "$stats" ]; then
    IFS='/' read -r min avg max stddev <<< "$stats"
    printf "%-15s %7s ms %5s ms %5s ms %5s ms %5s\n" "$target" "$min" "$avg" "$max" "$stddev" "$loss"
  else
    printf "%-15s %8s %8s %8s %8s %5s\n" "$target" "—" "—" "—" "—" "$loss"
  fi
done
echo ""

# DNS
echo "--- DNS Resolution ---"
printf "%-15s %8s\n" "Domain" "Time"
for domain in google.com youtube.com github.com ya.ru telegram.org; do
  ms=$(dig "$domain" @8.8.8.8 2>/dev/null | grep "Query time" | awk '{print $4}')
  printf "%-15s %5s ms\n" "$domain" "${ms:-N/A}"
done
echo ""

# HTTPS Latency
echo "--- HTTPS Latency ---"
printf "%-25s %10s %10s %10s\n" "URL" "Connect" "TTFB" "Total"
for url in https://google.com https://youtube.com https://github.com https://ya.ru https://telegram.org; do
  result=$(curl -o /dev/null -s -w "%{time_connect} %{time_starttransfer} %{time_total}" --max-time 15 -L "$url" 2>/dev/null)
  read -r conn ttfb total <<< "$result"
  printf "%-25s %8s s %8s s %8s s\n" "$url" "$conn" "$ttfb" "$total"
done
echo ""

# Download speed — VPN path vs direct (physical iface), 3 categories.
# Each call does 3 runs and averages them. Range-request 10 MB so we don't
# waste time on the full file. Parallel arrays (bash 3.2 has no assoc arrays).
BYTES=10000000
TIMEOUT=30

LABELS=("RU (selectel SPb)" "EU (leaseweb DE)" "blocked (cloudflare)")
URLS=(
  "https://speedtest.selectel.ru/100MB"
  "https://mirror.de.leaseweb.net/ubuntu-releases/24.04/ubuntu-24.04.4-desktop-amd64.iso"
  "https://speed.cloudflare.com/__down?bytes=${BYTES}"
)

measure_speed() {
  # $1 = iface flag ("" or "--interface enX"), $2 = url
  # Prints average speed in bytes/sec across 3 runs.
  local iface_flag="$1"
  local url="$2"
  local sum=0 ok=0 i out
  for i in 1 2 3; do
    out=$(curl $iface_flag -r "0-$((BYTES-1))" -o /dev/null -s -w "%{speed_download}" --max-time "$TIMEOUT" "$url" 2>/dev/null)
    case "$out" in ""|"0"|"0.000") ;; *)
      sum=$(awk -v s="$sum" -v n="$out" 'BEGIN{print s + n}')
      ok=$((ok + 1))
      ;;
    esac
  done
  if [ "$ok" -eq 0 ]; then
    echo "0"
  else
    awk -v s="$sum" -v n="$ok" 'BEGIN{printf "%.0f", s/n}'
  fi
}

echo "--- Download (avg of 3 runs, 10 MB range request) ---"
printf "%-25s %13s %13s %10s\n" "Destination" "VPN MB/s" "Direct MB/s" "ratio"
i=0
while [ $i -lt ${#LABELS[@]} ]; do
  label="${LABELS[$i]}"
  url="${URLS[$i]}"

  vpn=$(measure_speed "" "$url")
  dir=$(measure_speed "--interface $PHYS_IF" "$url")

  vpn_mb=$(awk -v s="$vpn" 'BEGIN{printf "%.2f", s/1048576}')
  dir_mb=$(awk -v s="$dir" 'BEGIN{printf "%.2f", s/1048576}')

  # Ratio is only meaningful when both paths actually delivered data.
  # Direct < 10% of VPN almost always means the direct path is DPI'd to
  # garbage, in which case the ratio (e.g. "5000x") is noise and would
  # mislead anyone skimming this file into thinking the proxy is 5000x
  # faster than direct — it isn't, the direct side is just gone.
  if [ "$dir" = "0" ] || [ "$vpn" = "0" ]; then
    ratio="—"
  else
    ratio=$(awk -v v="$vpn" -v d="$dir" 'BEGIN{
      if (d * 10 < v) print "DPI'"'"'d"
      else printf "%.2fx", v/d
    }')
  fi

  printf "%-25s %13s %13s %10s\n" "$label" "$vpn_mb" "$dir_mb" "$ratio"
  i=$((i + 1))
done
echo ""

# Upload — VPN only. Cloudflare upload endpoint is throttled direct same as
# download, so a direct measurement would only show TSPU behaviour, not our
# proxy. Kept as single run for proxy upload-path regression tracking.
echo "--- Upload (cloudflare, VPN only, 50 MB) ---"
ul=$(dd if=/dev/urandom bs=1M count=50 2>/dev/null | curl -X POST -o /dev/null -s -w "%{size_upload} %{time_total}" --max-time 60 --data-binary @- "https://speed.cloudflare.com/__up" 2>/dev/null)
read -r ul_size ul_time <<< "$ul"
ul_mb=$(awk -v s="$ul_size" -v t="$ul_time" 'BEGIN{ if(t>0) printf "%.2f", s/t/1048576; else print "0.00" }')
ul_mib=$(awk -v s="$ul_size" 'BEGIN{ printf "%.1f", s/1048576 }')
echo "Upload:   ${ul_mb} MB/s (${ul_mib} MiB in ${ul_time}s)"
