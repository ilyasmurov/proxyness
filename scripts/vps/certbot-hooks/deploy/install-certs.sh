#!/bin/bash
# certbot deploy-hook: runs once per renewed lineage with RENEWED_LINEAGE set.
# Installed at /etc/letsencrypt/renewal-hooks/deploy/.
#
#  proxyness.smurov.com        → copy into the proxyness-data volume as
#                                 cert.pem/key.pem and restart the container
#                                 (the Go server loads the cert once at boot).
#  admin.proxyness.smurov.com  → copy into /etc/nginx/ssl/admin and reload
#                                 nginx (it terminates TLS for the admin SPA).
set -euo pipefail

lineage="${RENEWED_LINEAGE:-}"
[ -n "$lineage" ] || { echo "install-certs: RENEWED_LINEAGE not set" >&2; exit 1; }
name=$(basename "$lineage")

case "$name" in
  proxyness.smurov.com)
    vol=$(docker volume inspect proxyness-data --format '{{ .Mountpoint }}')
    install -m 644 "$lineage/fullchain.pem" "$vol/cert.pem"
    install -m 600 "$lineage/privkey.pem" "$vol/key.pem"
    docker restart proxyness >/dev/null 2>&1 || true
    echo "install-certs: proxy cert installed, container restarted"
    ;;
  admin.proxyness.smurov.com)
    install -d -m 750 /etc/nginx/ssl/admin
    install -m 644 "$lineage/fullchain.pem" /etc/nginx/ssl/admin/fullchain.pem
    install -m 600 "$lineage/privkey.pem" /etc/nginx/ssl/admin/privkey.pem
    nginx -t >/dev/null 2>&1 && systemctl reload nginx
    echo "install-certs: admin cert installed, nginx reloaded"
    ;;
  *)
    echo "install-certs: unknown lineage $name, nothing to do"
    ;;
esac
