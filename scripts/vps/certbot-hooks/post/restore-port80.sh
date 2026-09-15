#!/bin/bash
# certbot post-hook: give port 80 back to the landing container. Runs whether
# the renewal succeeded or not. Installed at /etc/letsencrypt/renewal-hooks/post/.
docker start proxyness-landing >/dev/null 2>&1 || true
