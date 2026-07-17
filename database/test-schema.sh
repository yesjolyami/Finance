#!/bin/sh
set -eu

if [ -z "${DATABASE_TEST_URL:-}" ]; then
  echo "DATABASE_TEST_URL is required and must point to a disposable PostgreSQL database" >&2
  exit 1
fi

cd "$(dirname "$0")"

go tool goose -dir migrations postgres "$DATABASE_TEST_URL" up
psql "$DATABASE_TEST_URL" -v ON_ERROR_STOP=1 -Atc \
  "SELECT count(*) FROM information_schema.tables WHERE table_schema = 'public' AND table_name IN ('backup_v5_import_previews', 'backup_v5_import_runs')" \
  | grep -qx '2'
psql "$DATABASE_TEST_URL" -v ON_ERROR_STOP=1 -Atc \
  "SELECT (SELECT count(*) FROM information_schema.columns WHERE table_schema = 'public' AND table_name = 'backup_v5_import_previews'), (SELECT count(*) FROM information_schema.columns WHERE table_schema = 'public' AND table_name = 'backup_v5_import_runs')" \
  | grep -qx '11|24'
psql "$DATABASE_TEST_URL" -v ON_ERROR_STOP=1 -Atc \
  "SELECT count(*) FROM pg_trigger WHERE NOT tgisinternal AND tgname IN ('backup_v5_import_previews_update_guard', 'backup_v5_import_runs_append_only')" \
  | grep -qx '2'
psql "$DATABASE_TEST_URL" -v ON_ERROR_STOP=1 -Atc \
  "SELECT to_regclass('public.household_invitations') IS NOT NULL" \
  | grep -qx 't'
psql "$DATABASE_TEST_URL" -v ON_ERROR_STOP=1 -Atc \
  "SELECT count(*) FROM pg_collation c JOIN pg_namespace n ON n.oid = c.collnamespace WHERE n.nspname = 'public' AND c.collname = 'finance_category_name_ci' AND c.collprovider = 'i' AND NOT c.collisdeterministic" \
  | grep -qx '1'
psql "$DATABASE_TEST_URL" -v ON_ERROR_STOP=1 -Atc \
  "WITH expected(table_name, column_name) AS (VALUES ('accounts', 'version'), ('accounts', 'creation_idempotency_key'), ('accounts', 'creation_payload_hash'), ('categories', 'version'), ('categories', 'creation_idempotency_key'), ('categories', 'creation_payload_hash'), ('transactions', 'version'), ('transactions', 'idempotency_payload_hash')) SELECT count(*) FROM expected e JOIN information_schema.columns c USING (table_name, column_name) WHERE c.table_schema = 'public'" \
  | grep -qx '8'
psql "$DATABASE_TEST_URL" -v ON_ERROR_STOP=1 -Atc \
  "SELECT count(*) FROM pg_trigger WHERE NOT tgisinternal AND tgname IN ('accounts_prevent_creation_metadata_update', 'categories_prevent_creation_metadata_update', 'transactions_prevent_creation_metadata_update')" \
  | grep -qx '3'

psql "$DATABASE_TEST_URL" -v ON_ERROR_STOP=1 -f tests/backup_v5_import_down_fixture.sql

go tool goose -dir migrations postgres "$DATABASE_TEST_URL" down
psql "$DATABASE_TEST_URL" -v ON_ERROR_STOP=1 -Atc \
  "SELECT (to_regclass('public.backup_v5_import_previews') IS NULL), (to_regclass('public.backup_v5_import_runs') IS NULL)" \
  | grep -qx 't|t'
psql "$DATABASE_TEST_URL" -v ON_ERROR_STOP=1 -Atc \
  "SELECT (SELECT count(*) FROM accounts WHERE id = 'fd000000-0000-4000-8000-000000000001'), (SELECT count(*) FROM categories WHERE id = 'fd100000-0000-4000-8000-000000000001'), (SELECT count(*) FROM transactions WHERE id = 'fd200000-0000-4000-8000-000000000001'), (SELECT count(*) FROM budgets WHERE id = 'fd300000-0000-4000-8000-000000000001'), (SELECT count(*) FROM goals WHERE id = 'fd400000-0000-4000-8000-000000000001'), (SELECT count(*) FROM debts WHERE id = 'fd500000-0000-4000-8000-000000000001'), (SELECT count(*) FROM debt_payments WHERE id = 'fd600000-0000-4000-8000-000000000001'), (SELECT count(*) FROM audit_log WHERE id = 'fe000000-0000-4000-8000-000000000001' AND entity_type = 'households' AND action = 'imported')" \
  | grep -qx '1|1|1|1|1|1|1|1'
psql "$DATABASE_TEST_URL" -v ON_ERROR_STOP=1 -Atc \
  "SELECT (SELECT version FROM accounts WHERE id = 'fd000000-0000-4000-8000-000000000001'), (SELECT version FROM categories WHERE id = 'fd100000-0000-4000-8000-000000000001'), (SELECT version FROM transactions WHERE id = 'fd200000-0000-4000-8000-000000000001')" \
  | grep -qx '41|42|43'

go tool goose -dir migrations postgres "$DATABASE_TEST_URL" up-to 4
psql "$DATABASE_TEST_URL" -v ON_ERROR_STOP=1 -Atc \
  "SELECT (SELECT count(*) FROM backup_v5_import_previews), (SELECT count(*) FROM backup_v5_import_runs), (SELECT version FROM accounts WHERE id = 'fd000000-0000-4000-8000-000000000001'), (SELECT version FROM categories WHERE id = 'fd100000-0000-4000-8000-000000000001'), (SELECT version FROM transactions WHERE id = 'fd200000-0000-4000-8000-000000000001'), (SELECT count(*) FROM audit_log WHERE id = 'fe000000-0000-4000-8000-000000000001')" \
  | grep -qx '0|0|41|42|43|1'

go tool goose -dir migrations postgres "$DATABASE_TEST_URL" down

psql "$DATABASE_TEST_URL" -v ON_ERROR_STOP=1 -f tests/finance_core_down_fixture.sql

go tool goose -dir migrations postgres "$DATABASE_TEST_URL" down
psql "$DATABASE_TEST_URL" -v ON_ERROR_STOP=1 -Atc \
  "WITH removed(table_name, column_name) AS (VALUES ('accounts', 'version'), ('accounts', 'creation_idempotency_key'), ('accounts', 'creation_payload_hash'), ('categories', 'version'), ('categories', 'creation_idempotency_key'), ('categories', 'creation_payload_hash'), ('transactions', 'version'), ('transactions', 'idempotency_payload_hash')) SELECT count(*) FROM removed r JOIN information_schema.columns c USING (table_name, column_name) WHERE c.table_schema = 'public'" \
  | grep -qx '0'
psql "$DATABASE_TEST_URL" -v ON_ERROR_STOP=1 -Atc \
  "SELECT to_regclass('public.household_invitations') IS NOT NULL" \
  | grep -qx 't'
psql "$DATABASE_TEST_URL" -v ON_ERROR_STOP=1 -Atc \
  "SELECT (SELECT count(*) FROM accounts WHERE id = 'd3000000-0000-4000-8000-000000000001'), (SELECT count(*) FROM categories WHERE id = 'd4000000-0000-4000-8000-000000000001'), (SELECT count(*) FROM transactions WHERE id = 'd5000000-0000-4000-8000-000000000001'), (SELECT count(*) FROM audit_log WHERE id::text LIKE 'd6000000-0000-4000-8000-%')" \
  | grep -qx '1|1|1|3'

go tool goose -dir migrations postgres "$DATABASE_TEST_URL" up-to 3
psql "$DATABASE_TEST_URL" -v ON_ERROR_STOP=1 -Atc \
  "SELECT (SELECT version FROM accounts WHERE id = 'd3000000-0000-4000-8000-000000000001'), (SELECT version FROM categories WHERE id = 'd4000000-0000-4000-8000-000000000001'), (SELECT version FROM transactions WHERE id = 'd5000000-0000-4000-8000-000000000001'), (SELECT idempotency_key FROM transactions WHERE id = 'd5000000-0000-4000-8000-000000000001'), (SELECT idempotency_payload_hash IS NULL FROM transactions WHERE id = 'd5000000-0000-4000-8000-000000000001')" \
  | grep -qx '1|1|1|finance-down-transaction|t'

go tool goose -dir migrations postgres "$DATABASE_TEST_URL" down
psql "$DATABASE_TEST_URL" -v ON_ERROR_STOP=1 -f tests/household_invitations_down_fixture.sql

go tool goose -dir migrations postgres "$DATABASE_TEST_URL" down
psql "$DATABASE_TEST_URL" -v ON_ERROR_STOP=1 -Atc \
  "SELECT to_regclass('public.household_invitations') IS NULL" \
  | grep -qx 't'
psql "$DATABASE_TEST_URL" -v ON_ERROR_STOP=1 -Atc \
  "SELECT count(*) FROM information_schema.columns WHERE table_schema = 'public' AND table_name = 'households' AND column_name = 'creation_idempotency_key'" \
  | grep -qx '0'
psql "$DATABASE_TEST_URL" -v ON_ERROR_STOP=1 -Atc \
  "SELECT count(*) FROM audit_log WHERE entity_type = 'household_invitations'" \
  | grep -qx '0'
psql "$DATABASE_TEST_URL" -v ON_ERROR_STOP=1 -Atc \
  "SELECT (SELECT count(*) FROM accounts WHERE id = 'd3000000-0000-4000-8000-000000000001'), (SELECT count(*) FROM categories WHERE id = 'd4000000-0000-4000-8000-000000000001'), (SELECT count(*) FROM transactions WHERE id = 'd5000000-0000-4000-8000-000000000001'), (SELECT count(*) FROM audit_log WHERE id::text LIKE 'd6000000-0000-4000-8000-%')" \
  | grep -qx '1|1|1|3'

go tool goose -dir migrations postgres "$DATABASE_TEST_URL" up
go tool goose -dir migrations postgres "$DATABASE_TEST_URL" up
go tool goose -dir migrations postgres "$DATABASE_TEST_URL" up
go tool goose -dir migrations postgres "$DATABASE_TEST_URL" status
psql "$DATABASE_TEST_URL" -v ON_ERROR_STOP=1 -f tests/schema_constraints.sql
psql "$DATABASE_TEST_URL" -v ON_ERROR_STOP=1 -f tests/household_invitations_constraints.sql
psql "$DATABASE_TEST_URL" -v ON_ERROR_STOP=1 -f tests/finance_core_idempotency_constraints.sql
psql "$DATABASE_TEST_URL" -v ON_ERROR_STOP=1 -f tests/backup_v5_import_control_constraints.sql
