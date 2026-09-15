#!/bin/bash
# Issue the two Let's Encrypt certs on a freshly provisioned host. Run once
# DNS for both names resolves to this box; renewals then run unattended via
# certbot.timer + the hooks installed by provision.sh.
#
# `certonly` does not read renewal-hooks/, so the same hooks are passed
# explicitly: free port 80 (stop landing), issue, restore, install.
set -euo pipefail
HOOKS=/etc/letsencrypt/renewal-hooks
EMAIL="${CERTBOT_EMAIL:-admin@smurov.com}"
for d in proxyness.smurov.com admin.proxyness.smurov.com; do
  if [ -d "/etc/letsencrypt/live/$d" ]; then
    echo "cert for $d already exists — renewing instead"
    continue
  fi
  certbot certonly --standalone --non-interactive --agree-tos -m "$EMAIL" -d "$d" \
    --pre-hook "$HOOKS/pre/free-port80.sh" \
    --post-hook "$HOOKS/post/restore-port80.sh" \
    --deploy-hook "$HOOKS/deploy/install-certs.sh"
done
certbot renew --quiet || true
certbot certificates
