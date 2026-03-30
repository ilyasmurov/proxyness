#!/bin/bash
# Run this on the VPS to set up Let's Encrypt certificate
# Usage: ./setup-ssl.sh proxy.smurov.com

DOMAIN=${1:-proxy.smurov.com}

set -e

# Install certbot
apt-get update -qq
apt-get install -y -qq certbot

# Stop our container temporarily (certbot needs port 443)
docker stop smurov-proxy 2>/dev/null || true

# Get certificate
certbot certonly --standalone -d "$DOMAIN" --non-interactive --agree-tos -m admin@smurov.com

# Copy certs to Docker volume
VOLUME_PATH=$(docker volume inspect smurov-proxy-data --format '{{ .Mountpoint }}')
cp /etc/letsencrypt/live/$DOMAIN/fullchain.pem "$VOLUME_PATH/cert.pem"
cp /etc/letsencrypt/live/$DOMAIN/privkey.pem "$VOLUME_PATH/key.pem"

# Restart container
docker start smurov-proxy

echo ""
echo "Done! Certificate installed for $DOMAIN"
echo "Admin panel: https://$DOMAIN/admin/"
echo ""
echo "Auto-renewal: add to crontab:"
echo "0 3 * * * certbot renew --pre-hook 'docker stop smurov-proxy' --post-hook 'cp /etc/letsencrypt/live/$DOMAIN/fullchain.pem $VOLUME_PATH/cert.pem && cp /etc/letsencrypt/live/$DOMAIN/privkey.pem $VOLUME_PATH/key.pem && docker start smurov-proxy'"
