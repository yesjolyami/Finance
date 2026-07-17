#!/usr/bin/env bash
set -euo pipefail

umask 077

BACKUP_DIR=${BACKUP_DIR:-/var/backups/finance}
BACKUP_RETENTION_DAYS=${BACKUP_RETENTION_DAYS:-14}
PGSERVICEFILE=${PGSERVICEFILE:-/etc/finance/secrets/pg_service.conf}
PGPASSFILE=${PGPASSFILE:-/etc/finance/secrets/pgpass}
PGSERVICE=${PGSERVICE:-finance_production}
AGE_RECIPIENT_FILE=${AGE_RECIPIENT_FILE:-/etc/finance/backup/age-recipient.txt}

if [[ ! "$BACKUP_RETENTION_DAYS" =~ ^[0-9]+$ ]] || (( BACKUP_RETENTION_DAYS < 1 || BACKUP_RETENTION_DAYS > 365 )); then
  echo "backup retention is invalid" >&2
  exit 1
fi
VALIDATE_FILE=$(cd -- "$(dirname "${BASH_SOURCE[0]}")" && pwd)/validate-operational-file.py
python3 "$VALIDATE_FILE" "$PGSERVICEFILE" readonly 65536 ||
  { echo "backup credential material is unavailable" >&2; exit 1; }
python3 "$VALIDATE_FILE" "$PGPASSFILE" owner-secret 65536 ||
  { echo "backup credential material is unavailable" >&2; exit 1; }
python3 "$VALIDATE_FILE" "$AGE_RECIPIENT_FILE" readonly 65536 ||
  { echo "backup credential material is unavailable" >&2; exit 1; }
mkdir -p "$BACKUP_DIR"
chmod 0700 "$BACKUP_DIR"

timestamp=$(date -u +%Y%m%dT%H%M%SZ)
temporary="$BACKUP_DIR/.finance-$timestamp.dump.age.tmp"
destination="$BACKUP_DIR/finance-$timestamp.dump.age"
trap 'rm -f -- "$temporary"' EXIT

export PGSERVICEFILE PGPASSFILE PGSERVICE
pg_dump --format=custom --compress=0 --no-owner --no-privileges |
  age --encrypt --recipients-file "$AGE_RECIPIENT_FILE" --output "$temporary"
chmod 0600 "$temporary"
mv -- "$temporary" "$destination"
trap - EXIT

find "$BACKUP_DIR" -maxdepth 1 -type f -name 'finance-*.dump.age' -mtime "+$BACKUP_RETENTION_DAYS" -delete
echo "encrypted backup completed"
