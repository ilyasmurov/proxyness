#!/bin/bash
# Quick proxy benchmark — run periodically to track performance.
# Usage: ./scripts/benchmark.sh

echo "=== SmurovProxy Benchmark ==="
echo "Date: $(date '+%Y-%m-%d %H:%M:%S')"
echo ""

# External IP
IP=$(curl -s --max-time 5 https://api.ipify.org 2>/dev/null || echo "N/A")
echo "External IP: $IP"
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

# Speed
echo "--- Speed ---"
dl=$(curl -o /dev/null -s -w "%{speed_download} %{size_download} %{time_total}" --max-time 30 "https://speed.cloudflare.com/__down?bytes=25000000" 2>/dev/null)
read -r dl_speed dl_size dl_time <<< "$dl"
dl_mb=$(echo "$dl_speed" | awk '{printf "%.1f", $1/1048576}')
echo "Download: ${dl_mb} MB/s (25 MB in ${dl_time}s)"

ul=$(dd if=/dev/urandom bs=1M count=25 2>/dev/null | curl -X POST -o /dev/null -s -w "%{speed_upload} %{size_upload} %{time_total}" --max-time 30 --data-binary @- "https://speed.cloudflare.com/__up" 2>/dev/null)
read -r ul_speed ul_size ul_time <<< "$ul"
ul_mb=$(echo "$ul_speed" | awk '{printf "%.1f", $1/1048576}')
echo "Upload:   ${ul_mb} MB/s (25 MB in ${ul_time}s)"
