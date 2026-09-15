#!/bin/bash
# Build and install AmneziaWG on Debian (no PPA there — the Amnezia PPA is
# Ubuntu-only). Kernel module via DKMS from amneziawg-linux-kernel-module,
# userspace (awg, awg-quick, awg-quick@.service) from amneziawg-tools.
# Idempotent: skips whatever is already installed for the running kernel.
set -euo pipefail
export DEBIAN_FRONTEND=noninteractive
SRC=/usr/local/src

apt-get install -y -qq --no-install-recommends git build-essential dkms pkg-config \
  "linux-headers-$(uname -r)" >/dev/null

if ! modinfo amneziawg >/dev/null 2>&1; then
  echo "==> amneziawg kernel module (dkms)"
  [ -d "$SRC/amneziawg-linux-kernel-module" ] || \
    git clone -q https://github.com/amnezia-vpn/amneziawg-linux-kernel-module "$SRC/amneziawg-linux-kernel-module"
  cd "$SRC/amneziawg-linux-kernel-module"
  git pull -q || true
  VER=$(git describe --tags --abbrev=0 2>/dev/null | sed 's/^v//'); VER="${VER:-1.0.$(date +%Y%m%d)}"
  if ! dkms status 2>/dev/null | grep -q "amneziawg/$VER"; then
    rm -rf "/usr/src/amneziawg-$VER"
    cp -r src "/usr/src/amneziawg-$VER"
    # Upstream ships no dkms.conf — write our own.
    cat > "/usr/src/amneziawg-$VER/dkms.conf" <<DKMS
PACKAGE_NAME="amneziawg"
PACKAGE_VERSION="$VER"
BUILT_MODULE_NAME[0]="amneziawg"
DEST_MODULE_LOCATION[0]="/kernel/extra"
AUTOINSTALL="yes"
MAKE[0]="make module"
CLEAN="make clean"
DKMS
    dkms add -m amneziawg -v "$VER"
  fi
  dkms build -m amneziawg -v "$VER"
  dkms install -m amneziawg -v "$VER"
fi
modprobe amneziawg
grep -qx amneziawg /etc/modules-load.d/amneziawg.conf 2>/dev/null || echo amneziawg > /etc/modules-load.d/amneziawg.conf

if ! command -v awg >/dev/null; then
  echo "==> amneziawg-tools"
  [ -d "$SRC/amneziawg-tools" ] || git clone -q https://github.com/amnezia-vpn/amneziawg-tools "$SRC/amneziawg-tools"
  make -C "$SRC/amneziawg-tools/src" -j"$(nproc)" >/dev/null
  make -C "$SRC/amneziawg-tools/src" install >/dev/null
  systemctl daemon-reload
fi
echo "awg: $(awg --version 2>/dev/null || echo installed), module: $(modinfo -F version amneziawg 2>/dev/null || echo loaded)"
