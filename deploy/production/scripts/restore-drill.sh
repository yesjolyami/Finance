#!/usr/bin/env bash
set -euo pipefail

umask 077

BACKUP_FILE=${BACKUP_FILE:-}
RESTORE_PGSERVICE=${RESTORE_PGSERVICE:-}
RESTORE_EXPECTED_DATABASE=${RESTORE_EXPECTED_DATABASE:-}
RESTORE_DRILL_ACK=${RESTORE_DRILL_ACK:-}
PGSERVICEFILE=${PGSERVICEFILE:-/etc/finance/secrets/pg_service.conf}
PGPASSFILE=${PGPASSFILE:-/etc/finance/secrets/pgpass}
AGE_IDENTITY_FILE=${AGE_IDENTITY_FILE:-/etc/finance/backup/age-identity.txt}
DATABASE_TOOL_DIR=${DATABASE_TOOL_DIR:-/opt/finance/current/database}
GOOSE_BIN=${GOOSE_BIN:-/opt/finance/current/bin/goose}

if [[ -z "$BACKUP_FILE" || ! -f "$BACKUP_FILE" || -L "$BACKUP_FILE" ]]; then
  echo "encrypted backup fixture is required" >&2
  exit 1
fi
if [[ -z "$RESTORE_PGSERVICE" || "$RESTORE_PGSERVICE" == "finance_production" ]]; then
  echo "restore drill requires an isolated non-production libpq service" >&2
  exit 1
fi
if [[ "$RESTORE_DRILL_ACK" != "ack-isolated-empty-restore-database" ]]; then
  echo "restore drill acknowledgement is required" >&2
  exit 1
fi
if [[ ! "$RESTORE_EXPECTED_DATABASE" =~ ^[a-z][a-z0-9_]{0,47}_restore_drill$ ]]; then
  echo "restore drill database name is invalid" >&2
  exit 1
fi

VALIDATE_FILE=$(cd -- "$(dirname "${BASH_SOURCE[0]}")" && pwd)/validate-operational-file.py
python3 "$VALIDATE_FILE" "$PGSERVICEFILE" readonly 65536 ||
  { echo "restore credential material is unavailable" >&2; exit 1; }
python3 "$VALIDATE_FILE" "$PGPASSFILE" owner-secret 65536 ||
  { echo "restore credential material is unavailable" >&2; exit 1; }
python3 "$VALIDATE_FILE" "$AGE_IDENTITY_FILE" owner-secret 65536 ||
  { echo "restore credential material is unavailable" >&2; exit 1; }

export PGSERVICEFILE PGPASSFILE PGSERVICE="$RESTORE_PGSERVICE"
actual_database=$(psql --no-psqlrc --tuples-only --no-align --set ON_ERROR_STOP=1 \
  --command 'SELECT current_database()')
if [[ "$actual_database" != "$RESTORE_EXPECTED_DATABASE" ]]; then
  echo "restore drill target verification failed" >&2
  exit 1
fi
user_table_count=$(psql --no-psqlrc --tuples-only --no-align --set ON_ERROR_STOP=1 \
  --command "SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace WHERE c.relkind IN ('r', 'p') AND n.nspname NOT IN ('pg_catalog', 'information_schema') AND n.nspname !~ '^pg_toast'")
if [[ "$user_table_count" != "0" ]]; then
  echo "restore drill target verification failed" >&2
  exit 1
fi

age --decrypt --identity "$AGE_IDENTITY_FILE" "$BACKUP_FILE" |
  pg_restore --exit-on-error --no-owner --no-privileges --dbname="$RESTORE_EXPECTED_DATABASE"

export GOOSE_DRIVER=postgres GOOSE_DBSTRING="service=$RESTORE_PGSERVICE"
cd "$DATABASE_TOOL_DIR"
"$GOOSE_BIN" -version | grep -Fq 'v3.24.3'
"$GOOSE_BIN" -dir migrations status
version=$("$GOOSE_BIN" -dir migrations version 2>&1 | awk 'END {print $NF}')
if [[ "$version" != "4" ]]; then
  echo "restore drill migration verification failed" >&2
  exit 1
fi
applied=$(psql --no-psqlrc --tuples-only --no-align --set ON_ERROR_STOP=1 \
  --command "SELECT format('%s|%s', COALESCE(MAX(version_id) FILTER (WHERE is_applied), 0), COUNT(DISTINCT version_id) FILTER (WHERE is_applied AND version_id BETWEEN 1 AND 4)) FROM goose_db_version")
if [[ "$applied" != "4|4" ]]; then
  echo "restore drill migration verification failed" >&2
  exit 1
fi
psql --no-psqlrc -v ON_ERROR_STOP=1 -f tests/schema_constraints.sql
psql --no-psqlrc -v ON_ERROR_STOP=1 -f tests/household_invitations_constraints.sql
psql --no-psqlrc -v ON_ERROR_STOP=1 -f tests/finance_core_idempotency_constraints.sql
psql --no-psqlrc -v ON_ERROR_STOP=1 -f tests/backup_v5_import_control_constraints.sql
unset GOOSE_DBSTRING
echo "isolated restore drill completed"
