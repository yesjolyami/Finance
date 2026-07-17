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
    ('00000000-0000-4000-8000-000000000001', 'Тестовый профиль A'),
    ('00000000-0000-4000-8000-000000000002', 'Тестовый профиль B');

INSERT INTO households (id, name, created_by_user_id) VALUES
    ('10000000-0000-4000-8000-000000000001', 'Тестовый household A', '00000000-0000-4000-8000-000000000001'),
    ('10000000-0000-4000-8000-000000000002', 'Тестовый household B', '00000000-0000-4000-8000-000000000002');

INSERT INTO household_members (id, household_id, user_id, role) VALUES
    ('20000000-0000-4000-8000-000000000001', '10000000-0000-4000-8000-000000000001', '00000000-0000-4000-8000-000000000001', 'owner'),
    ('20000000-0000-4000-8000-000000000002', '10000000-0000-4000-8000-000000000002', '00000000-0000-4000-8000-000000000002', 'owner');

INSERT INTO accounts (
    id, household_id, name, color, account_type, owner_user_id, legacy_owner_label
) VALUES
    ('30000000-0000-4000-8000-000000000001', '10000000-0000-4000-8000-000000000001', 'Основной A', '#112233', 'regular', '00000000-0000-4000-8000-000000000001', 'Владелец A'),
    ('30000000-0000-4000-8000-000000000002', '10000000-0000-4000-8000-000000000001', 'Накопления A', '#445566', 'savings', '00000000-0000-4000-8000-000000000001', 'Владелец A'),
    ('30000000-0000-4000-8000-000000000003', '10000000-0000-4000-8000-000000000002', 'Основной B', '#778899', 'regular', '00000000-0000-4000-8000-000000000002', 'Владелец B');

SELECT pg_temp.expect_error(
    ARRAY['23503'],
    $sql$INSERT INTO accounts (
        id, household_id, name, color, owner_user_id
    ) VALUES (
        '31000000-0000-4000-8000-000000000001',
        '10000000-0000-4000-8000-000000000001', 'Чужой владелец', '#123456',
        '00000000-0000-4000-8000-000000000002'
    )$sql$,
    'cross-household account owner'
);

INSERT INTO categories (id, household_id, category_type, name, color) VALUES
    ('40000000-0000-4000-8000-000000000001', '10000000-0000-4000-8000-000000000001', 'income', 'Доход', '#227744'),
    ('40000000-0000-4000-8000-000000000002', '10000000-0000-4000-8000-000000000001', 'expense', 'Расход', '#992244'),
    ('40000000-0000-4000-8000-000000000003', '10000000-0000-4000-8000-000000000002', 'expense', 'Расход B', '#663399');

INSERT INTO transactions (
    id, household_id, transaction_type, account_id, category_id,
    amount_cents, event_date, source, idempotency_key, created_by_user_id
) VALUES (
    '50000000-0000-4000-8000-000000000001',
    '10000000-0000-4000-8000-000000000001',
    'income',
    '30000000-0000-4000-8000-000000000001',
    '40000000-0000-4000-8000-000000000001',
    100000,
    DATE '2026-01-10',
    'manual',
    'test-income-1',
    '00000000-0000-4000-8000-000000000001'
);

INSERT INTO transactions (
    id, household_id, transaction_type, account_id, to_account_id,
    amount_cents, event_date, source
) VALUES (
    '50000000-0000-4000-8000-000000000002',
    '10000000-0000-4000-8000-000000000001',
    'transfer',
    '30000000-0000-4000-8000-000000000001',
    '30000000-0000-4000-8000-000000000002',
    15000,
    DATE '2026-01-11',
    'manual'
);

SELECT pg_temp.expect_error(
    ARRAY['23514'],
    $sql$INSERT INTO transactions (
        id, household_id, transaction_type, account_id, category_id, amount_cents, event_date
    ) VALUES (
        '51000000-0000-4000-8000-000000000001',
        '10000000-0000-4000-8000-000000000001', 'expense',
        '30000000-0000-4000-8000-000000000001',
        '40000000-0000-4000-8000-000000000002', 0, DATE '2026-01-12'
    )$sql$,
    'zero transaction amount'
);

SELECT pg_temp.expect_error(
    ARRAY['23514'],
    $sql$INSERT INTO budgets (
        id, household_id, category_id, budget_month, amount_cents
    ) VALUES (
        '61000000-0000-4000-8000-000000000099',
        '10000000-0000-4000-8000-000000000001',
        '40000000-0000-4000-8000-000000000002', DATE '2026-03-01', -1
    )$sql$,
    'negative budget amount'
);

SELECT pg_temp.expect_error(
    ARRAY['23514'],
    $sql$INSERT INTO transactions (
        id, household_id, transaction_type, account_id, category_id, amount_cents, event_date
    ) VALUES (
        '51000000-0000-4000-8000-000000000002',
        '10000000-0000-4000-8000-000000000001', 'transfer',
        '30000000-0000-4000-8000-000000000001',
        '40000000-0000-4000-8000-000000000002', 100, DATE '2026-01-12'
    )$sql$,
    'invalid transfer structure'
);

SELECT pg_temp.expect_error(
    ARRAY['23503'],
    $sql$INSERT INTO transactions (
        id, household_id, transaction_type, account_id, category_id, amount_cents, event_date
    ) VALUES (
        '51000000-0000-4000-8000-000000000003',
        '10000000-0000-4000-8000-000000000001', 'expense',
        '30000000-0000-4000-8000-000000000003',
        '40000000-0000-4000-8000-000000000002', 100, DATE '2026-01-12'
    )$sql$,
    'cross-household transaction account'
);

SELECT pg_temp.expect_error(
    ARRAY['23503'],
    $sql$INSERT INTO transactions (
        id, household_id, transaction_type, account_id, category_id, amount_cents, event_date
    ) VALUES (
        '51000000-0000-4000-8000-000000000004',
        '10000000-0000-4000-8000-000000000001', 'expense',
        '30000000-0000-4000-8000-000000000001',
        '40000000-0000-4000-8000-000000000003', 100, DATE '2026-01-12'
    )$sql$,
    'cross-household transaction category'
);

SELECT pg_temp.expect_error(
    ARRAY['23503'],
    $sql$INSERT INTO transactions (
        id, household_id, transaction_type, account_id, category_id, amount_cents, event_date
    ) VALUES (
        '51000000-0000-4000-8000-000000000005',
        '10000000-0000-4000-8000-000000000001', 'income',
        '30000000-0000-4000-8000-000000000001',
        '40000000-0000-4000-8000-000000000002', 100, DATE '2026-01-12'
    )$sql$,
    'category type mismatch'
);

SELECT pg_temp.expect_error(
    ARRAY['23505'],
    $sql$INSERT INTO categories (
        id, household_id, category_type, name, color
    ) VALUES (
        '41000000-0000-4000-8000-000000000001',
        '10000000-0000-4000-8000-000000000001', 'expense', 'рАсХоД', '#123456'
    )$sql$,
    'case-insensitive category duplicate'
);

SELECT pg_temp.expect_error(
    ARRAY['23505'],
    $sql$INSERT INTO categories (
        id, household_id, category_type, name, color
    ) VALUES (
        '41000000-0000-4000-8000-000000000002',
        '10000000-0000-4000-8000-000000000001', 'expense', '  Расход  ', '#123456'
    )$sql$,
    'category duplicate with surrounding whitespace'
);

INSERT INTO budgets (
    id, household_id, category_id, budget_month, amount_cents
) VALUES (
    '60000000-0000-4000-8000-000000000001',
    '10000000-0000-4000-8000-000000000001',
    '40000000-0000-4000-8000-000000000002',
    DATE '2026-01-01',
    50000
);

SELECT pg_temp.expect_error(
    ARRAY['23505'],
    $sql$INSERT INTO budgets (
        id, household_id, category_id, budget_month, amount_cents
    ) VALUES (
        '61000000-0000-4000-8000-000000000001',
        '10000000-0000-4000-8000-000000000001',
        '40000000-0000-4000-8000-000000000002', DATE '2026-01-01', 60000
    )$sql$,
    'duplicate active budget month'
);

SELECT pg_temp.expect_error(
    ARRAY['23503'],
    $sql$INSERT INTO budgets (
        id, household_id, category_id, budget_month, amount_cents
    ) VALUES (
        '61000000-0000-4000-8000-000000000002',
        '10000000-0000-4000-8000-000000000001',
        '40000000-0000-4000-8000-000000000003', DATE '2026-02-01', 60000
    )$sql$,
    'cross-household budget category'
);

SELECT pg_temp.expect_error(
    ARRAY['23503'],
    $sql$INSERT INTO budgets (
        id, household_id, category_id, budget_month, amount_cents
    ) VALUES (
        '61000000-0000-4000-8000-000000000003',
        '10000000-0000-4000-8000-000000000001',
        '40000000-0000-4000-8000-000000000001', DATE '2026-02-01', 60000
    )$sql$,
    'budget requires expense category'
);

SELECT pg_temp.expect_error(
    ARRAY['23514'],
    $sql$INSERT INTO budgets (
        id, household_id, category_id, budget_month, amount_cents
    ) VALUES (
        '61000000-0000-4000-8000-000000000004',
        '10000000-0000-4000-8000-000000000001',
        '40000000-0000-4000-8000-000000000002', DATE '2026-02-02', 60000
    )$sql$,
    'budget month must be first day'
);

SELECT pg_temp.expect_error(
    ARRAY['23503'],
    $sql$INSERT INTO recurring_transactions (
        id, household_id, transaction_type, account_id, category_id,
        amount_cents, frequency, next_execution_date
    ) VALUES (
        '62000000-0000-4000-8000-000000000001',
        '10000000-0000-4000-8000-000000000001', 'expense',
        '30000000-0000-4000-8000-000000000003',
        '40000000-0000-4000-8000-000000000002', 1000, 'monthly', DATE '2026-02-01'
    )$sql$,
    'cross-household recurring account'
);

SELECT pg_temp.expect_error(
    ARRAY['23505'],
    $sql$INSERT INTO transactions (
        id, household_id, transaction_type, account_id, category_id,
        amount_cents, event_date, idempotency_key
    ) VALUES (
        '51000000-0000-4000-8000-000000000006',
        '10000000-0000-4000-8000-000000000001', 'income',
        '30000000-0000-4000-8000-000000000001',
        '40000000-0000-4000-8000-000000000001', 200, DATE '2026-01-13', 'test-income-1'
    )$sql$,
    'duplicate idempotency key'
);

INSERT INTO goals (
    id, household_id, name, target_amount_cents, initial_saved_cents, color
) VALUES
    ('70000000-0000-4000-8000-000000000001', '10000000-0000-4000-8000-000000000001', 'Цель A', 1000000, 50000, '#556677'),
    ('70000000-0000-4000-8000-000000000002', '10000000-0000-4000-8000-000000000002', 'Цель B', 2000000, 0, '#667788');

INSERT INTO goal_contributions (
    id, household_id, goal_id, amount_cents, event_date
) VALUES (
    '71000000-0000-4000-8000-000000000001',
    '10000000-0000-4000-8000-000000000001',
    '70000000-0000-4000-8000-000000000001', 15000, DATE '2026-01-14'
);

SELECT pg_temp.expect_error(
    ARRAY['23503'],
    $sql$INSERT INTO goal_contributions (
        id, household_id, goal_id, amount_cents, event_date
    ) VALUES (
        '71000000-0000-4000-8000-000000000002',
        '10000000-0000-4000-8000-000000000001',
        '70000000-0000-4000-8000-000000000002', 100, DATE '2026-01-14'
    )$sql$,
    'cross-household goal contribution'
);

INSERT INTO debts (
    id, household_id, person_label, direction, original_amount_cents
) VALUES
    ('80000000-0000-4000-8000-000000000001', '10000000-0000-4000-8000-000000000001', 'Контрагент A', 'owe_me', 80000),
    ('80000000-0000-4000-8000-000000000002', '10000000-0000-4000-8000-000000000002', 'Контрагент B', 'i_owe', 90000);

INSERT INTO debt_payments (
    id, household_id, debt_id, amount_cents, event_date
) VALUES (
    '81000000-0000-4000-8000-000000000001',
    '10000000-0000-4000-8000-000000000001',
    '80000000-0000-4000-8000-000000000001', 10000, DATE '2026-01-15'
);

SELECT pg_temp.expect_error(
    ARRAY['23503'],
    $sql$INSERT INTO debt_payments (
        id, household_id, debt_id, amount_cents, event_date
    ) VALUES (
        '81000000-0000-4000-8000-000000000002',
        '10000000-0000-4000-8000-000000000001',
        '80000000-0000-4000-8000-000000000002', 100, DATE '2026-01-15'
    )$sql$,
    'cross-household debt payment'
);

DO $$
DECLARE
    original_amount BIGINT;
BEGIN
    SELECT original_amount_cents INTO original_amount
    FROM debts
    WHERE id = '80000000-0000-4000-8000-000000000001';
    IF original_amount <> 80000 THEN
        RAISE EXCEPTION 'debt payment changed original debt amount';
    END IF;
END;
$$;

UPDATE accounts
SET archived_at = CURRENT_TIMESTAMP
WHERE id = '30000000-0000-4000-8000-000000000001';

UPDATE transactions
SET deleted_at = CURRENT_TIMESTAMP,
    deleted_by_user_id = '00000000-0000-4000-8000-000000000001',
    deletion_reason = 'schema test'
WHERE id = '50000000-0000-4000-8000-000000000001';

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM transactions
        WHERE id = '50000000-0000-4000-8000-000000000001'
          AND deleted_at IS NOT NULL
    ) THEN
        RAISE EXCEPTION 'soft-deleted transaction disappeared';
    END IF;
END;
$$;

SELECT pg_temp.expect_error(
    ARRAY['23503'],
    $sql$DELETE FROM accounts
    WHERE id = '30000000-0000-4000-8000-000000000001'$sql$,
    'hard delete referenced account'
);

SELECT pg_temp.expect_error(
    ARRAY['23503'],
    $sql$INSERT INTO audit_log (
        id, household_id, actor_user_id, entity_type, entity_id, action
    ) VALUES (
        '91000000-0000-4000-8000-000000000001',
        '10000000-0000-4000-8000-000000000001',
        '00000000-0000-4000-8000-000000000002',
        'transactions', '50000000-0000-4000-8000-000000000001', 'updated'
    )$sql$,
    'cross-household audit actor'
);

INSERT INTO audit_log (
    id, household_id, actor_user_id, entity_type, entity_id, action, changes
) VALUES (
    '90000000-0000-4000-8000-000000000001',
    '10000000-0000-4000-8000-000000000001',
    '00000000-0000-4000-8000-000000000001',
    'transactions',
    '50000000-0000-4000-8000-000000000001',
    'deleted',
    '{"reason":"schema test"}'::jsonb
);

SELECT pg_temp.expect_error(
    ARRAY['55000'],
    $sql$UPDATE audit_log SET action = 'updated'
    WHERE id = '90000000-0000-4000-8000-000000000001'$sql$,
    'audit log update'
);

SELECT pg_temp.expect_error(
    ARRAY['55000'],
    $sql$DELETE FROM audit_log
    WHERE id = '90000000-0000-4000-8000-000000000001'$sql$,
    'audit log delete'
);

ROLLBACK;

\echo 'Schema constraint integration tests: OK'
