#!/bin/bash
# certbot pre-hook: standalone HTTP-01 needs port 80, which the landing
# container holds. Stop it for the duration of the renewal; post/restore-port80.sh
# brings it back. Installed at /etc/letsencrypt/renewal-hooks/pre/.
docker stop proxyness-landing >/dev/null 2>&1 || true
