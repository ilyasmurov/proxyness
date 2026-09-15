#!/bin/bash
# Insert (or reactivate) one user + device with a known key. For bootstrapping
# a host whose DB has no backup: the key already lives on the client machine
# (~/.config/proxyness/device-key, %APPDATA%\Proxyness\device-key).
#   seed-device.sh "<user name>" "<device name>" <64-hex-key>
set -euo pipefail
USER_NAME="${1:?user name}"; DEV_NAME="${2:?device name}"; KEY="${3:?device key}"
[[ "$KEY" =~ ^[0-9a-f]{64}$ ]] || { echo "key must be 64 hex chars" >&2; exit 1; }
su - postgres -c "psql -v ON_ERROR_STOP=1 -q -d proxyness" <<SQL
INSERT INTO users (name) SELECT '$USER_NAME' WHERE NOT EXISTS (SELECT 1 FROM users WHERE name='$USER_NAME');
INSERT INTO devices (user_id, name, key, active)
  SELECT id, '$DEV_NAME', '$KEY', TRUE FROM users WHERE name='$USER_NAME'
  ON CONFLICT (key) DO UPDATE SET active = TRUE, name = EXCLUDED.name;
SQL
su - postgres -c "psql -d proxyness -Atc \"SELECT u.name, d.id, d.name, left(d.key,6)||'…' FROM devices d JOIN users u ON u.id=d.user_id WHERE d.key='$KEY'\""
