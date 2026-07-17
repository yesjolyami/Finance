\set ON_ERROR_STOP on

BEGIN;

CREATE OR REPLACE FUNCTION pg_temp.expect_error(
    expected_states TEXT[],
    statement TEXT,
    test_name TEXT
)
RETURNS VOID
LANGUAGE plpgsql
AS $$
DECLARE
    caught_state TEXT;
BEGIN
    BEGIN
        EXECUTE statement;
    EXCEPTION WHEN OTHERS THEN
        caught_state := SQLSTATE;
    END;

    IF caught_state IS NULL THEN
        RAISE EXCEPTION 'test %: statement unexpectedly succeeded', test_name;
    END IF;
    IF NOT caught_state = ANY(expected_states) THEN
        RAISE EXCEPTION 'test %: expected %, got %', test_name, expected_states, caught_state;
    END IF;
END;
$$;

INSERT INTO users (id, display_name) VALUES
    ('9a000000-0000-4000-8000-000000000001', 'Import actor A'),
    ('9a000000-0000-4000-8000-000000000002', 'Import actor B'),
    ('9a000000-0000-4000-8000-000000000003', 'Import actor A2');

INSERT INTO households (id, name, created_by_user_id) VALUES
    ('9b000000-0000-4000-8000-000000000001', 'Import household A', '9a000000-0000-4000-8000-000000000001'),
    ('9b000000-0000-4000-8000-000000000002', 'Import household B', '9a000000-0000-4000-8000-000000000002');

INSERT INTO household_members (id, household_id, user_id, role) VALUES
    ('9c000000-0000-4000-8000-000000000001', '9b000000-0000-4000-8000-000000000001', '9a000000-0000-4000-8000-000000000001', 'owner'),
    ('9c000000-0000-4000-8000-000000000002', '9b000000-0000-4000-8000-000000000002', '9a000000-0000-4000-8000-000000000002', 'owner'),
    ('9c000000-0000-4000-8000-000000000003', '9b000000-0000-4000-8000-000000000001', '9a000000-0000-4000-8000-000000000003', 'member');

INSERT INTO backup_v5_import_previews (
    id, household_id, actor_user_id, token_hash, backup_digest,
    budget_month, policy_version, expires_at, created_at
) VALUES
    (
        '9d000000-0000-4000-8000-000000000001',
        '9b000000-0000-4000-8000-000000000001',
        '9a000000-0000-4000-8000-000000000001',
        decode(repeat('11', 32), 'hex'), decode(repeat('21', 32), 'hex'),
        DATE '2026-07-01', 'backup-v5-import/1',
        TIMESTAMPTZ '2026-07-15 10:10:00+00', TIMESTAMPTZ '2026-07-15 10:00:00+00'
    ),
    (
        '9d000000-0000-4000-8000-000000000002',
        '9b000000-0000-4000-8000-000000000001',
        '9a000000-0000-4000-8000-000000000001',
        decode(repeat('12', 32), 'hex'), decode(repeat('22', 32), 'hex'),
        DATE '2026-08-01', 'backup-v5-import/1',
        TIMESTAMPTZ '2026-07-15 10:15:00+00', TIMESTAMPTZ '2026-07-15 10:00:00+00'
    ),
    (
        '9d000000-0000-4000-8000-000000000003',
        '9b000000-0000-4000-8000-000000000001',
        '9a000000-0000-4000-8000-000000000001',
        decode(repeat('13', 32), 'hex'), decode(repeat('23', 32), 'hex'),
        DATE '2026-09-01', 'backup-v5-import/1',
        TIMESTAMPTZ '2026-07-15 10:15:00+00', TIMESTAMPTZ '2026-07-15 10:00:00+00'
    ),
    (
        '9d000000-0000-4000-8000-000000000004',
        '9b000000-0000-4000-8000-000000000001',
        '9a000000-0000-4000-8000-000000000001',
        decode(repeat('14', 32), 'hex'), decode(repeat('24', 32), 'hex'),
        DATE '2026-10-01', 'backup-v5-import/1',
        TIMESTAMPTZ '2026-07-15 09:10:00+00', TIMESTAMPTZ '2026-07-15 09:00:00+00'
    );

UPDATE backup_v5_import_previews
SET consumed_at = TIMESTAMPTZ '2026-07-15 10:05:00+00'
WHERE id = '9d000000-0000-4000-8000-000000000002';
UPDATE backup_v5_import_previews
SET revoked_at = TIMESTAMPTZ '2026-07-15 10:06:00+00'
WHERE id = '9d000000-0000-4000-8000-000000000003';
UPDATE backup_v5_import_previews
SET revoked_at = TIMESTAMPTZ '2026-07-15 10:07:00+00'
WHERE id = '9d000000-0000-4000-8000-000000000004';

DO $$
DECLARE
    active_count INTEGER;
    consumed_count INTEGER;
    revoked_count INTEGER;
BEGIN
    SELECT count(*) FILTER (WHERE consumed_at IS NULL AND revoked_at IS NULL),
           count(*) FILTER (WHERE consumed_at IS NOT NULL),
           count(*) FILTER (WHERE revoked_at IS NOT NULL)
    INTO active_count, consumed_count, revoked_count
    FROM backup_v5_import_previews;
    IF active_count <> 1 OR consumed_count <> 1 OR revoked_count <> 2 THEN
        RAISE EXCEPTION 'valid preview state transitions were not stored';
    END IF;
    IF NOT EXISTS (
        SELECT 1
        FROM backup_v5_import_previews
        WHERE id = '9d000000-0000-4000-8000-000000000004'
          AND revoked_at > expires_at
    ) THEN
        RAISE EXCEPTION 'expired preview must remain revocable';
    END IF;
END;
$$;

ALTER TABLE backup_v5_import_previews
    DISABLE TRIGGER backup_v5_import_previews_update_guard;

SELECT pg_temp.expect_error(ARRAY['23502'], $sql$UPDATE backup_v5_import_previews SET id = NULL WHERE id = '9d000000-0000-4000-8000-000000000001'$sql$, 'preview id null');
SELECT pg_temp.expect_error(ARRAY['23502'], $sql$UPDATE backup_v5_import_previews SET household_id = NULL WHERE id = '9d000000-0000-4000-8000-000000000001'$sql$, 'preview household null');
SELECT pg_temp.expect_error(ARRAY['23502'], $sql$UPDATE backup_v5_import_previews SET actor_user_id = NULL WHERE id = '9d000000-0000-4000-8000-000000000001'$sql$, 'preview actor null');
SELECT pg_temp.expect_error(ARRAY['23502'], $sql$UPDATE backup_v5_import_previews SET token_hash = NULL WHERE id = '9d000000-0000-4000-8000-000000000001'$sql$, 'preview token hash null');
SELECT pg_temp.expect_error(ARRAY['23514'], $sql$UPDATE backup_v5_import_previews SET token_hash = decode('01', 'hex') WHERE id = '9d000000-0000-4000-8000-000000000001'$sql$, 'preview token hash short');
SELECT pg_temp.expect_error(ARRAY['23514'], $sql$UPDATE backup_v5_import_previews SET token_hash = decode(repeat('01', 33), 'hex') WHERE id = '9d000000-0000-4000-8000-000000000001'$sql$, 'preview token hash long');
SELECT pg_temp.expect_error(ARRAY['23502'], $sql$UPDATE backup_v5_import_previews SET backup_digest = NULL WHERE id = '9d000000-0000-4000-8000-000000000001'$sql$, 'preview digest null');
SELECT pg_temp.expect_error(ARRAY['23514'], $sql$UPDATE backup_v5_import_previews SET backup_digest = decode('01', 'hex') WHERE id = '9d000000-0000-4000-8000-000000000001'$sql$, 'preview digest short');
SELECT pg_temp.expect_error(ARRAY['23514'], $sql$UPDATE backup_v5_import_previews SET backup_digest = decode(repeat('01', 33), 'hex') WHERE id = '9d000000-0000-4000-8000-000000000001'$sql$, 'preview digest long');
SELECT pg_temp.expect_error(ARRAY['23502'], $sql$UPDATE backup_v5_import_previews SET budget_month = NULL WHERE id = '9d000000-0000-4000-8000-000000000001'$sql$, 'preview budget month null');
SELECT pg_temp.expect_error(ARRAY['23514'], $sql$UPDATE backup_v5_import_previews SET budget_month = DATE '2026-07-02' WHERE id = '9d000000-0000-4000-8000-000000000001'$sql$, 'preview budget month first day');
SELECT pg_temp.expect_error(ARRAY['23502'], $sql$UPDATE backup_v5_import_previews SET policy_version = NULL WHERE id = '9d000000-0000-4000-8000-000000000001'$sql$, 'preview policy null');
SELECT pg_temp.expect_error(ARRAY['23514'], $sql$UPDATE backup_v5_import_previews SET policy_version = 'backup-v5-import/2' WHERE id = '9d000000-0000-4000-8000-000000000001'$sql$, 'preview policy exact');
SELECT pg_temp.expect_error(ARRAY['23502'], $sql$UPDATE backup_v5_import_previews SET expires_at = NULL WHERE id = '9d000000-0000-4000-8000-000000000001'$sql$, 'preview expiry null');
SELECT pg_temp.expect_error(ARRAY['23502'], $sql$UPDATE backup_v5_import_previews SET created_at = NULL WHERE id = '9d000000-0000-4000-8000-000000000001'$sql$, 'preview creation null');
SELECT pg_temp.expect_error(ARRAY['23514'], $sql$UPDATE backup_v5_import_previews SET expires_at = created_at WHERE id = '9d000000-0000-4000-8000-000000000001'$sql$, 'preview positive ttl');
SELECT pg_temp.expect_error(ARRAY['23514'], $sql$UPDATE backup_v5_import_previews SET expires_at = created_at + INTERVAL '15 minutes 1 second' WHERE id = '9d000000-0000-4000-8000-000000000001'$sql$, 'preview hard ttl');
SELECT pg_temp.expect_error(ARRAY['23514'], $sql$UPDATE backup_v5_import_previews SET consumed_at = created_at - INTERVAL '1 second' WHERE id = '9d000000-0000-4000-8000-000000000001'$sql$, 'preview consumed before creation');
SELECT pg_temp.expect_error(ARRAY['23514'], $sql$UPDATE backup_v5_import_previews SET consumed_at = expires_at + INTERVAL '1 second' WHERE id = '9d000000-0000-4000-8000-000000000001'$sql$, 'preview consumed after expiry');
SELECT pg_temp.expect_error(ARRAY['23514'], $sql$UPDATE backup_v5_import_previews SET revoked_at = created_at - INTERVAL '1 second' WHERE id = '9d000000-0000-4000-8000-000000000001'$sql$, 'preview revoked before creation');
SELECT pg_temp.expect_error(ARRAY['23514'], $sql$UPDATE backup_v5_import_previews SET revoked_at = created_at + INTERVAL '1 minute' WHERE id = '9d000000-0000-4000-8000-000000000002'$sql$, 'preview consumed and revoked');

SELECT pg_temp.expect_error(
    ARRAY['23505'],
    $sql$INSERT INTO backup_v5_import_previews (
        id, household_id, actor_user_id, token_hash, backup_digest, budget_month,
        policy_version, expires_at, created_at
    ) VALUES (
        '9d100000-0000-4000-8000-000000000001',
        '9b000000-0000-4000-8000-000000000002',
        '9a000000-0000-4000-8000-000000000002',
        decode(repeat('11', 32), 'hex'), decode(repeat('31', 32), 'hex'),
        DATE '2026-07-01', 'backup-v5-import/1',
        TIMESTAMPTZ '2026-07-15 10:10:00+00', TIMESTAMPTZ '2026-07-15 10:00:00+00'
    )$sql$,
    'preview token globally unique'
);

SELECT pg_temp.expect_error(
    ARRAY['23503'],
    $sql$INSERT INTO backup_v5_import_previews (
        id, household_id, actor_user_id, token_hash, backup_digest, budget_month,
        policy_version, expires_at, created_at
    ) VALUES (
        '9d100000-0000-4000-8000-000000000002',
        '9b000000-0000-4000-8000-000000000001',
        '9a000000-0000-4000-8000-000000000002',
        decode(repeat('15', 32), 'hex'), decode(repeat('32', 32), 'hex'),
        DATE '2026-07-01', 'backup-v5-import/1',
        TIMESTAMPTZ '2026-07-15 10:10:00+00', TIMESTAMPTZ '2026-07-15 10:00:00+00'
    )$sql$,
    'preview actor tenant isolation'
);

ALTER TABLE backup_v5_import_previews
    ENABLE TRIGGER backup_v5_import_previews_update_guard;

SELECT pg_temp.expect_error(
    ARRAY['P0001'],
    $sql$UPDATE backup_v5_import_previews
        SET backup_digest = decode(repeat('99', 32), 'hex')
        WHERE id = '9d000000-0000-4000-8000-000000000001'$sql$,
    'preview binding metadata immutable'
);
UPDATE backup_v5_import_previews
SET consumed_at = created_at + INTERVAL '1 minute'
WHERE id = '9d000000-0000-4000-8000-000000000001';
SELECT pg_temp.expect_error(
    ARRAY['P0001'],
    $sql$UPDATE backup_v5_import_previews
        SET consumed_at = NULL
        WHERE id = '9d000000-0000-4000-8000-000000000001'$sql$,
    'preview terminal transition cannot reverse'
);
SELECT pg_temp.expect_error(
    ARRAY['P0001'],
    $sql$UPDATE backup_v5_import_previews
        SET revoked_at = created_at + INTERVAL '2 minutes'
        WHERE id = '9d000000-0000-4000-8000-000000000001'$sql$,
    'preview terminal transition cannot repeat'
);
DELETE FROM backup_v5_import_previews
WHERE id = '9d000000-0000-4000-8000-000000000003';
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM backup_v5_import_previews
        WHERE id = '9d000000-0000-4000-8000-000000000003'
    ) THEN
        RAISE EXCEPTION 'preview housekeeping delete must remain available';
    END IF;
END;
$$;

INSERT INTO backup_v5_import_runs (
    id, household_id, actor_user_id, hmac_key_id,
    idempotency_key_hmac, request_fingerprint_hmac, policy_version, status,
    accounts_count, categories_count, transactions_count, budgets_count,
    goals_count, goal_contributions_count, debts_count, debt_payments_count,
    legacy_owner_not_linked_count, archive_time_approximated_count,
    goal_exceeds_target_count, debt_overpaid_count,
    system_resource_preserved_count, budget_month_explicit_choice_count,
    completed_at, created_at
) VALUES (
    '9e000000-0000-4000-8000-000000000001',
    '9b000000-0000-4000-8000-000000000001',
    '9a000000-0000-4000-8000-000000000001',
    'import-key-2026-01', decode(repeat('41', 32), 'hex'), decode(repeat('51', 32), 'hex'),
    'backup-v5-import/1', 'completed',
    1, 2, 3, 4, 5, 0, 6, 7,
    1, 2, 3, 4, 3, 1,
    TIMESTAMPTZ '2026-07-15 10:02:00+00', TIMESTAMPTZ '2026-07-15 10:00:00+00'
);

ALTER TABLE backup_v5_import_runs
    DISABLE TRIGGER backup_v5_import_runs_append_only;

SELECT pg_temp.expect_error(ARRAY['23502'], $sql$UPDATE backup_v5_import_runs SET id = NULL WHERE id = '9e000000-0000-4000-8000-000000000001'$sql$, 'run id null');
SELECT pg_temp.expect_error(ARRAY['23502'], $sql$UPDATE backup_v5_import_runs SET household_id = NULL WHERE id = '9e000000-0000-4000-8000-000000000001'$sql$, 'run household null');
SELECT pg_temp.expect_error(ARRAY['23502'], $sql$UPDATE backup_v5_import_runs SET actor_user_id = NULL WHERE id = '9e000000-0000-4000-8000-000000000001'$sql$, 'run actor null');
SELECT pg_temp.expect_error(ARRAY['23502'], $sql$UPDATE backup_v5_import_runs SET hmac_key_id = NULL WHERE id = '9e000000-0000-4000-8000-000000000001'$sql$, 'run key id null');
SELECT pg_temp.expect_error(ARRAY['23514'], $sql$UPDATE backup_v5_import_runs SET hmac_key_id = '' WHERE id = '9e000000-0000-4000-8000-000000000001'$sql$, 'run key id empty');
SELECT pg_temp.expect_error(ARRAY['23514'], $sql$UPDATE backup_v5_import_runs SET hmac_key_id = ' key ' WHERE id = '9e000000-0000-4000-8000-000000000001'$sql$, 'run key id whitespace');
SELECT pg_temp.expect_error(ARRAY['23514'], $sql$UPDATE backup_v5_import_runs SET hmac_key_id = repeat('k', 65) WHERE id = '9e000000-0000-4000-8000-000000000001'$sql$, 'run key id length');
SELECT pg_temp.expect_error(ARRAY['23502'], $sql$UPDATE backup_v5_import_runs SET idempotency_key_hmac = NULL WHERE id = '9e000000-0000-4000-8000-000000000001'$sql$, 'run idempotency hmac null');
SELECT pg_temp.expect_error(ARRAY['23514'], $sql$UPDATE backup_v5_import_runs SET idempotency_key_hmac = decode('01', 'hex') WHERE id = '9e000000-0000-4000-8000-000000000001'$sql$, 'run idempotency hmac short');
SELECT pg_temp.expect_error(ARRAY['23514'], $sql$UPDATE backup_v5_import_runs SET idempotency_key_hmac = decode(repeat('01', 33), 'hex') WHERE id = '9e000000-0000-4000-8000-000000000001'$sql$, 'run idempotency hmac long');
SELECT pg_temp.expect_error(ARRAY['23502'], $sql$UPDATE backup_v5_import_runs SET request_fingerprint_hmac = NULL WHERE id = '9e000000-0000-4000-8000-000000000001'$sql$, 'run fingerprint hmac null');
SELECT pg_temp.expect_error(ARRAY['23514'], $sql$UPDATE backup_v5_import_runs SET request_fingerprint_hmac = decode('01', 'hex') WHERE id = '9e000000-0000-4000-8000-000000000001'$sql$, 'run fingerprint hmac short');
SELECT pg_temp.expect_error(ARRAY['23514'], $sql$UPDATE backup_v5_import_runs SET request_fingerprint_hmac = decode(repeat('01', 33), 'hex') WHERE id = '9e000000-0000-4000-8000-000000000001'$sql$, 'run fingerprint hmac long');
SELECT pg_temp.expect_error(ARRAY['23502'], $sql$UPDATE backup_v5_import_runs SET policy_version = NULL WHERE id = '9e000000-0000-4000-8000-000000000001'$sql$, 'run policy null');
SELECT pg_temp.expect_error(ARRAY['23514'], $sql$UPDATE backup_v5_import_runs SET policy_version = 'backup-v5-import/2' WHERE id = '9e000000-0000-4000-8000-000000000001'$sql$, 'run policy exact');
SELECT pg_temp.expect_error(ARRAY['23502'], $sql$UPDATE backup_v5_import_runs SET status = NULL WHERE id = '9e000000-0000-4000-8000-000000000001'$sql$, 'run status null');
SELECT pg_temp.expect_error(ARRAY['23514'], $sql$UPDATE backup_v5_import_runs SET status = 'pending' WHERE id = '9e000000-0000-4000-8000-000000000001'$sql$, 'run completed status exact');
SELECT pg_temp.expect_error(ARRAY['23502'], $sql$UPDATE backup_v5_import_runs SET completed_at = NULL WHERE id = '9e000000-0000-4000-8000-000000000001'$sql$, 'run completed timestamp null');
SELECT pg_temp.expect_error(ARRAY['23502'], $sql$UPDATE backup_v5_import_runs SET created_at = NULL WHERE id = '9e000000-0000-4000-8000-000000000001'$sql$, 'run creation timestamp null');
SELECT pg_temp.expect_error(ARRAY['23514'], $sql$UPDATE backup_v5_import_runs SET completed_at = created_at - INTERVAL '1 second' WHERE id = '9e000000-0000-4000-8000-000000000001'$sql$, 'run completion ordering');

DO $$
DECLARE
    field_name TEXT;
    upper_bound INTEGER;
BEGIN
    FOR field_name, upper_bound IN
        SELECT * FROM (VALUES
            ('accounts_count', 10000),
            ('categories_count', 20000),
            ('transactions_count', 200000),
            ('budgets_count', 20000),
            ('goals_count', 20000),
            ('goal_contributions_count', 0),
            ('debts_count', 20000),
            ('debt_payments_count', 200000)
        ) AS limits(field_name, upper_bound)
    LOOP
        PERFORM pg_temp.expect_error(
            ARRAY['23502'],
            format('UPDATE backup_v5_import_runs SET %I = NULL WHERE id = %L', field_name, '9e000000-0000-4000-8000-000000000001'),
            field_name || ' null'
        );
        PERFORM pg_temp.expect_error(
            ARRAY['23514'],
            format('UPDATE backup_v5_import_runs SET %I = -1 WHERE id = %L', field_name, '9e000000-0000-4000-8000-000000000001'),
            field_name || ' negative'
        );
        PERFORM pg_temp.expect_error(
            ARRAY['23514'],
            format('UPDATE backup_v5_import_runs SET %I = %s WHERE id = %L', field_name, upper_bound + 1, '9e000000-0000-4000-8000-000000000001'),
            field_name || ' upper bound'
        );
    END LOOP;

    FOR field_name IN
        SELECT * FROM (VALUES
            ('legacy_owner_not_linked_count'),
            ('archive_time_approximated_count'),
            ('goal_exceeds_target_count'),
            ('debt_overpaid_count'),
            ('system_resource_preserved_count'),
            ('budget_month_explicit_choice_count')
        ) AS warnings(field_name)
    LOOP
        PERFORM pg_temp.expect_error(
            ARRAY['23502'],
            format('UPDATE backup_v5_import_runs SET %I = NULL WHERE id = %L', field_name, '9e000000-0000-4000-8000-000000000001'),
            field_name || ' null'
        );
        PERFORM pg_temp.expect_error(
            ARRAY['23514'],
            format('UPDATE backup_v5_import_runs SET %I = -1 WHERE id = %L', field_name, '9e000000-0000-4000-8000-000000000001'),
            field_name || ' negative'
        );
        PERFORM pg_temp.expect_error(
            ARRAY['23514'],
            format(
                'UPDATE backup_v5_import_runs SET %I = %s WHERE id = %L',
                field_name,
                CASE WHEN field_name = 'budget_month_explicit_choice_count' THEN 2 ELSE 300001 END,
                '9e000000-0000-4000-8000-000000000001'
            ),
            field_name || ' upper bound'
        );
    END LOOP;
END;
$$;

SELECT pg_temp.expect_error(ARRAY['23514'], $sql$UPDATE backup_v5_import_runs SET legacy_owner_not_linked_count = accounts_count + 1 WHERE id = '9e000000-0000-4000-8000-000000000001'$sql$, 'legacy owner warning relation');
SELECT pg_temp.expect_error(ARRAY['23514'], $sql$UPDATE backup_v5_import_runs SET archive_time_approximated_count = goals_count + debts_count + 1 WHERE id = '9e000000-0000-4000-8000-000000000001'$sql$, 'archive warning relation');
SELECT pg_temp.expect_error(ARRAY['23514'], $sql$UPDATE backup_v5_import_runs SET goal_exceeds_target_count = goals_count + 1 WHERE id = '9e000000-0000-4000-8000-000000000001'$sql$, 'goal warning relation');
SELECT pg_temp.expect_error(ARRAY['23514'], $sql$UPDATE backup_v5_import_runs SET debt_overpaid_count = debts_count + 1 WHERE id = '9e000000-0000-4000-8000-000000000001'$sql$, 'debt warning relation');
SELECT pg_temp.expect_error(ARRAY['23514'], $sql$UPDATE backup_v5_import_runs SET system_resource_preserved_count = accounts_count + categories_count + 1 WHERE id = '9e000000-0000-4000-8000-000000000001'$sql$, 'system resource warning relation');

UPDATE backup_v5_import_runs
SET transactions_count = 199979,
    debt_payments_count = 100007,
    budgets_count = 20000
WHERE id = '9e000000-0000-4000-8000-000000000001';
UPDATE backup_v5_import_runs
SET transactions_count = 3,
    debt_payments_count = 7,
    budgets_count = 4
WHERE id = '9e000000-0000-4000-8000-000000000001';

SELECT pg_temp.expect_error(
    ARRAY['23514'],
    $sql$UPDATE backup_v5_import_runs
        SET transactions_count = 150000, debt_payments_count = 150001
        WHERE id = '9e000000-0000-4000-8000-000000000001'$sql$,
    'run total count upper bound'
);

SELECT pg_temp.expect_error(
    ARRAY['23505'],
    $sql$INSERT INTO backup_v5_import_runs (
        id, household_id, actor_user_id, hmac_key_id,
        idempotency_key_hmac, request_fingerprint_hmac, policy_version, status,
        accounts_count, categories_count, transactions_count, budgets_count,
        goals_count, goal_contributions_count, debts_count, debt_payments_count,
        legacy_owner_not_linked_count, archive_time_approximated_count,
        goal_exceeds_target_count, debt_overpaid_count,
        system_resource_preserved_count, budget_month_explicit_choice_count,
        completed_at, created_at
    ) VALUES (
        '9e100000-0000-4000-8000-000000000001',
        '9b000000-0000-4000-8000-000000000001',
        '9a000000-0000-4000-8000-000000000001',
        'import-key-2026-01', decode(repeat('41', 32), 'hex'), decode(repeat('52', 32), 'hex'),
        'backup-v5-import/1', 'completed',
        0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
        TIMESTAMPTZ '2026-07-15 10:02:00+00', TIMESTAMPTZ '2026-07-15 10:00:00+00'
    )$sql$,
    'run duplicate idempotency hmac in logical scope'
);

INSERT INTO backup_v5_import_runs (
    id, household_id, actor_user_id, hmac_key_id,
    idempotency_key_hmac, request_fingerprint_hmac, policy_version, status,
    accounts_count, categories_count, transactions_count, budgets_count,
    goals_count, goal_contributions_count, debts_count, debt_payments_count,
    legacy_owner_not_linked_count, archive_time_approximated_count,
    goal_exceeds_target_count, debt_overpaid_count,
    system_resource_preserved_count, budget_month_explicit_choice_count,
    completed_at, created_at
) VALUES
    (
        '9e100000-0000-4000-8000-000000000002',
        '9b000000-0000-4000-8000-000000000001',
        '9a000000-0000-4000-8000-000000000001',
        'import-key-2026-02', decode(repeat('41', 32), 'hex'), decode(repeat('53', 32), 'hex'),
        'backup-v5-import/1', 'completed',
        0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
        TIMESTAMPTZ '2026-07-15 10:02:00+00', TIMESTAMPTZ '2026-07-15 10:00:00+00'
    ),
    (
        '9e100000-0000-4000-8000-000000000003',
        '9b000000-0000-4000-8000-000000000002',
        '9a000000-0000-4000-8000-000000000002',
        'import-key-2026-01', decode(repeat('41', 32), 'hex'), decode(repeat('54', 32), 'hex'),
        'backup-v5-import/1', 'completed',
        0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
        TIMESTAMPTZ '2026-07-15 10:02:00+00', TIMESTAMPTZ '2026-07-15 10:00:00+00'
    ),
    (
        '9e100000-0000-4000-8000-000000000004',
        '9b000000-0000-4000-8000-000000000001',
        '9a000000-0000-4000-8000-000000000003',
        'import-key-2026-01', decode(repeat('41', 32), 'hex'), decode(repeat('55', 32), 'hex'),
        'backup-v5-import/1', 'completed',
        0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
        TIMESTAMPTZ '2026-07-15 10:02:00+00', TIMESTAMPTZ '2026-07-15 10:00:00+00'
    );

SELECT pg_temp.expect_error(
    ARRAY['23503'],
    $sql$INSERT INTO backup_v5_import_runs (
        id, household_id, actor_user_id, hmac_key_id,
        idempotency_key_hmac, request_fingerprint_hmac, policy_version, status,
        accounts_count, categories_count, transactions_count, budgets_count,
        goals_count, goal_contributions_count, debts_count, debt_payments_count,
        legacy_owner_not_linked_count, archive_time_approximated_count,
        goal_exceeds_target_count, debt_overpaid_count,
        system_resource_preserved_count, budget_month_explicit_choice_count,
        completed_at, created_at
    ) VALUES (
        '9e100000-0000-4000-8000-000000000005',
        '9b000000-0000-4000-8000-000000000001',
        '9a000000-0000-4000-8000-000000000002',
        'import-key-2026-01', decode(repeat('61', 32), 'hex'), decode(repeat('62', 32), 'hex'),
        'backup-v5-import/1', 'completed',
        0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
        TIMESTAMPTZ '2026-07-15 10:02:00+00', TIMESTAMPTZ '2026-07-15 10:00:00+00'
    )$sql$,
    'run actor tenant isolation'
);

ALTER TABLE backup_v5_import_runs
    ENABLE TRIGGER backup_v5_import_runs_append_only;

SELECT pg_temp.expect_error(
    ARRAY['P0001'],
    $sql$UPDATE backup_v5_import_runs
        SET completed_at = completed_at
        WHERE id = '9e000000-0000-4000-8000-000000000001'$sql$,
    'completed run update forbidden'
);
SELECT pg_temp.expect_error(
    ARRAY['P0001'],
    $sql$DELETE FROM backup_v5_import_runs
        WHERE id = '9e000000-0000-4000-8000-000000000001'$sql$,
    'completed run delete forbidden'
);

DO $$
DECLARE
    valid_index_count INTEGER;
BEGIN
    SELECT count(*) INTO valid_index_count
    FROM pg_index i
    JOIN pg_class c ON c.oid = i.indexrelid
    WHERE c.relname IN (
        'idx_backup_v5_import_previews_active_expiry',
        'idx_backup_v5_import_previews_terminal_cleanup',
        'idx_backup_v5_import_previews_household_active',
        'idx_backup_v5_import_runs_hmac_key_id'
    )
      AND i.indisvalid
      AND i.indisready;
    IF valid_index_count <> 4 THEN
        RAISE EXCEPTION 'expected four valid import cleanup/lookup indexes, got %', valid_index_count;
    END IF;

    IF NOT EXISTS (
        SELECT 1 FROM pg_indexes
        WHERE schemaname = 'public'
          AND indexname = 'idx_backup_v5_import_previews_active_expiry'
          AND indexdef LIKE '%WHERE ((consumed_at IS NULL) AND (revoked_at IS NULL))%'
    ) THEN
        RAISE EXCEPTION 'active preview cleanup index predicate is missing';
    END IF;

    IF NOT EXISTS (
        SELECT 1 FROM pg_indexes
        WHERE schemaname = 'public'
          AND indexname = 'idx_backup_v5_import_previews_household_active'
          AND indexdef LIKE '%(household_id, expires_at, created_at)%'
          AND indexdef LIKE '%WHERE ((consumed_at IS NULL) AND (revoked_at IS NULL))%'
    ) THEN
        RAISE EXCEPTION 'household active preview concurrency index is missing';
    END IF;
END;
$$;

ROLLBACK;

\echo 'Backup v5 import control constraints: OK'
