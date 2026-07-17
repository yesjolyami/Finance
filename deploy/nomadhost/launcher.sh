#!/bin/sh
set -eu

app=/opt/finance/current
secret="$app/secrets/database-url"

[ -f "$secret" ] && [ ! -L "$secret" ] || {
  echo "Finance database credential is unavailable" >&2
  exit 1
}
chmod 0400 "$secret"
find "$app/database/migrations" -type f -name '._*' -delete

exec /bin/bash "$app/run.sh"
