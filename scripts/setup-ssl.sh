#!/bin/bash
# Run this on the VPS to set up Let's Encrypt certificate
# Usage: ./setup-ssl.sh proxyness.smurov.com

DOMAIN=${1:-proxyness.smurov.com}

set -e

# Install certbot
apt-get update -qq
apt-get install -y -qq certbot

# Stop our container temporarily (certbot needs port 443)
docker stop proxyness 2>/dev/null || true

# Get certificate
certbot certonly --standalone -d "$DOMAIN" --non-interactive --agree-tos -m admin@smurov.com

# Copy certs to Docker volume
VOLUME_PATH=$(docker volume inspect proxyness-data --format '{{ .Mountpoint }}')
cp /etc/letsencrypt/live/$DOMAIN/fullchain.pem "$VOLUME_PATH/cert.pem"
cp /etc/letsencrypt/live/$DOMAIN/privkey.pem "$VOLUME_PATH/key.pem"

# Restart container
docker start proxyness

echo ""
echo "Done! Certificate installed for $DOMAIN"
echo "Admin panel: https://$DOMAIN/admin/"
echo ""
echo "Auto-renewal: add to crontab:"
echo "0 3 * * * certbot renew --pre-hook 'docker stop proxyness' --post-hook 'cp /etc/letsencrypt/live/$DOMAIN/fullchain.pem $VOLUME_PATH/cert.pem && cp /etc/letsencrypt/live/$DOMAIN/privkey.pem $VOLUME_PATH/key.pem && docker start proxyness'"
