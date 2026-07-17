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
    ('e0000000-0000-4000-8000-000000000001', 'Finance fixture A'),
    ('e0000000-0000-4000-8000-000000000002', 'Finance fixture B');

INSERT INTO households (id, name, created_by_user_id) VALUES
    ('e1000000-0000-4000-8000-000000000001', 'Finance household A', 'e0000000-0000-4000-8000-000000000001'),
    ('e1000000-0000-4000-8000-000000000002', 'Finance household B', 'e0000000-0000-4000-8000-000000000002');

INSERT INTO household_members (id, household_id, user_id, role) VALUES
    ('e2000000-0000-4000-8000-000000000001', 'e1000000-0000-4000-8000-000000000001', 'e0000000-0000-4000-8000-000000000001', 'owner'),
    ('e2000000-0000-4000-8000-000000000002', 'e1000000-0000-4000-8000-000000000002', 'e0000000-0000-4000-8000-000000000002', 'owner');

INSERT INTO accounts (
    id, household_id, name, color, creation_idempotency_key, creation_payload_hash
) VALUES
    (
        'e3000000-0000-4000-8000-000000000001',
        'e1000000-0000-4000-8000-000000000001',
        'Account A', '#112233', 'account-key', decode(repeat('11', 32), 'hex')
    ),
    (
        'e3000000-0000-4000-8000-000000000002',
        'e1000000-0000-4000-8000-000000000002',
        'Account B', '#445566', 'account-key', decode(repeat('12', 32), 'hex')
    );

INSERT INTO categories (
    id, household_id, category_type, name, color,
    creation_idempotency_key, creation_payload_hash
) VALUES
    (
        'e4000000-0000-4000-8000-000000000001',
        'e1000000-0000-4000-8000-000000000001',
        'expense', 'Category A', '#778899',
        'category-key', decode(repeat('21', 32), 'hex')
    ),
    (
        'e4000000-0000-4000-8000-000000000002',
        'e1000000-0000-4000-8000-000000000002',
        'expense', 'Category B', '#AABBCC',
        'category-key', decode(repeat('22', 32), 'hex')
    );

INSERT INTO transactions (
    id, household_id, transaction_type, account_id, category_id,
    amount_cents, event_date, idempotency_key, idempotency_payload_hash
) VALUES (
    'e5000000-0000-4000-8000-000000000001',
    'e1000000-0000-4000-8000-000000000001',
    'expense', 'e3000000-0000-4000-8000-000000000001',
    'e4000000-0000-4000-8000-000000000001', 100, DATE '2026-07-15',
    'transaction-key', decode(repeat('31', 32), 'hex')
);

DO $$
DECLARE
    account_version BIGINT;
    category_version BIGINT;
    transaction_version BIGINT;
BEGIN
    SELECT version INTO account_version FROM accounts
    WHERE id = 'e3000000-0000-4000-8000-000000000001';
    SELECT version INTO category_version FROM categories
    WHERE id = 'e4000000-0000-4000-8000-000000000001';
    SELECT version INTO transaction_version FROM transactions
    WHERE id = 'e5000000-0000-4000-8000-000000000001';

    IF account_version <> 1 OR category_version <> 1 OR transaction_version <> 1 THEN
        RAISE EXCEPTION 'finance entity version default must be one';
    END IF;
END;
$$;

SELECT pg_temp.expect_error(
    ARRAY['23514'],
    $sql$INSERT INTO accounts (
        id, household_id, name, color, creation_idempotency_key
    ) VALUES (
        'e3100000-0000-4000-8000-000000000001',
        'e1000000-0000-4000-8000-000000000001', 'Missing hash', '#123456', 'missing-hash'
    )$sql$,
    'account key requires non-null hash'
);

SELECT pg_temp.expect_error(
    ARRAY['23514'],
    $sql$INSERT INTO categories (
        id, household_id, category_type, name, color, creation_idempotency_key
    ) VALUES (
        'e4100000-0000-4000-8000-000000000001',
        'e1000000-0000-4000-8000-000000000001', 'income', 'Missing hash', '#123456',
        'missing-hash'
    )$sql$,
    'category key requires non-null hash'
);

SELECT pg_temp.expect_error(
    ARRAY['23514'],
    $sql$INSERT INTO accounts (
        id, household_id, name, color, creation_payload_hash
    ) VALUES (
        'e3100000-0000-4000-8000-000000000002',
        'e1000000-0000-4000-8000-000000000001', 'Hash without key', '#123456',
        decode(repeat('41', 32), 'hex')
    )$sql$,
    'account hash without key'
);

SELECT pg_temp.expect_error(
    ARRAY['23514'],
    $sql$INSERT INTO categories (
        id, household_id, category_type, name, color, creation_payload_hash
    ) VALUES (
        'e4100000-0000-4000-8000-000000000002',
        'e1000000-0000-4000-8000-000000000001', 'income', 'Hash without key', '#123456',
        decode(repeat('42', 32), 'hex')
    )$sql$,
    'category hash without key'
);

SELECT pg_temp.expect_error(
    ARRAY['23514'],
    $sql$INSERT INTO accounts (
        id, household_id, name, color, creation_idempotency_key, creation_payload_hash
    ) VALUES (
        'e3100000-0000-4000-8000-000000000003',
        'e1000000-0000-4000-8000-000000000001', 'Short hash', '#123456',
        'short-hash', decode('0102', 'hex')
    )$sql$,
    'account hash length'
);

SELECT pg_temp.expect_error(
    ARRAY['23514'],
    $sql$INSERT INTO categories (
        id, household_id, category_type, name, color,
        creation_idempotency_key, creation_payload_hash
    ) VALUES (
        'e4100000-0000-4000-8000-000000000003',
        'e1000000-0000-4000-8000-000000000001', 'income', 'Padded key', '#123456',
        ' padded ', decode(repeat('43', 32), 'hex')
    )$sql$,
    'category key surrounding whitespace'
);

SELECT pg_temp.expect_error(
    ARRAY['23514'],
    $sql$INSERT INTO accounts (
        id, household_id, name, color,
        creation_idempotency_key, creation_payload_hash
    ) VALUES (
        'e3100000-0000-4000-8000-000000000004',
        'e1000000-0000-4000-8000-000000000001', 'Empty key', '#123456',
        '', decode(repeat('44', 32), 'hex')
    )$sql$,
    'account empty key'
);

SELECT pg_temp.expect_error(
    ARRAY['23514'],
    $sql$INSERT INTO accounts (
        id, household_id, name, color,
        creation_idempotency_key, creation_payload_hash
    ) VALUES (
        'e3100000-0000-4000-8000-000000000005',
        'e1000000-0000-4000-8000-000000000001', 'Long key', '#123456',
        repeat('x', 256), decode(repeat('45', 32), 'hex')
    )$sql$,
    'account key too long'
);

SELECT pg_temp.expect_error(
    ARRAY['23505'],
    $sql$INSERT INTO accounts (
        id, household_id, name, color,
        creation_idempotency_key, creation_payload_hash
    ) VALUES (
        'e3100000-0000-4000-8000-000000000006',
        'e1000000-0000-4000-8000-000000000001', 'Duplicate key', '#123456',
        'account-key', decode(repeat('46', 32), 'hex')
    )$sql$,
    'duplicate account key in household'
);

SELECT pg_temp.expect_error(
    ARRAY['23505'],
    $sql$INSERT INTO categories (
        id, household_id, category_type, name, color,
        creation_idempotency_key, creation_payload_hash
    ) VALUES (
        'e4100000-0000-4000-8000-000000000004',
        'e1000000-0000-4000-8000-000000000001', 'income', 'Duplicate category key', '#123456',
        'category-key', decode(repeat('47', 32), 'hex')
    )$sql$,
    'duplicate category key in household'
);

-- A legacy transaction key without a fingerprint remains schema-valid.
INSERT INTO transactions (
    id, household_id, transaction_type, account_id, category_id,
    amount_cents, event_date, idempotency_key
) VALUES (
    'e5000000-0000-4000-8000-000000000002',
    'e1000000-0000-4000-8000-000000000001',
    'expense', 'e3000000-0000-4000-8000-000000000001',
    'e4000000-0000-4000-8000-000000000001', 200, DATE '2026-07-16',
    'legacy-key-without-hash'
);

SELECT pg_temp.expect_error(
    ARRAY['23514'],
    $sql$INSERT INTO transactions (
        id, household_id, transaction_type, account_id, category_id,
        amount_cents, event_date, idempotency_payload_hash
    ) VALUES (
        'e5100000-0000-4000-8000-000000000001',
        'e1000000-0000-4000-8000-000000000001',
        'expense', 'e3000000-0000-4000-8000-000000000001',
        'e4000000-0000-4000-8000-000000000001', 300, DATE '2026-07-17',
        decode(repeat('51', 32), 'hex')
    )$sql$,
    'transaction hash without key'
);

SELECT pg_temp.expect_error(
    ARRAY['23514'],
    $sql$INSERT INTO transactions (
        id, household_id, transaction_type, account_id, category_id,
        amount_cents, event_date, idempotency_key, idempotency_payload_hash
    ) VALUES (
        'e5100000-0000-4000-8000-000000000002',
        'e1000000-0000-4000-8000-000000000001',
        'expense', 'e3000000-0000-4000-8000-000000000001',
        'e4000000-0000-4000-8000-000000000001', 300, DATE '2026-07-17',
        ' padded ', decode(repeat('52', 32), 'hex')
    )$sql$,
    'transaction replay key surrounding whitespace'
);

SELECT pg_temp.expect_error(
    ARRAY['23514'],
    $sql$INSERT INTO transactions (
        id, household_id, transaction_type, account_id, category_id,
        amount_cents, event_date, idempotency_key, idempotency_payload_hash
    ) VALUES (
        'e5100000-0000-4000-8000-000000000003',
        'e1000000-0000-4000-8000-000000000001',
        'expense', 'e3000000-0000-4000-8000-000000000001',
        'e4000000-0000-4000-8000-000000000001', 300, DATE '2026-07-17',
        'short-transaction-hash', decode('0102', 'hex')
    )$sql$,
    'transaction fingerprint length'
);

SELECT pg_temp.expect_error(
    ARRAY['23514'],
    $sql$UPDATE accounts SET version = 0
    WHERE id = 'e3000000-0000-4000-8000-000000000001'$sql$,
    'account version zero'
);

SELECT pg_temp.expect_error(
    ARRAY['23514'],
    $sql$UPDATE categories SET version = -1
    WHERE id = 'e4000000-0000-4000-8000-000000000001'$sql$,
    'category version negative'
);

SELECT pg_temp.expect_error(
    ARRAY['23514'],
    $sql$UPDATE transactions SET version = 0
    WHERE id = 'e5000000-0000-4000-8000-000000000001'$sql$,
    'transaction version zero'
);

SELECT pg_temp.expect_error(
    ARRAY['55000'],
    $sql$UPDATE accounts SET creation_idempotency_key = 'changed-key'
    WHERE id = 'e3000000-0000-4000-8000-000000000001'$sql$,
    'account creation key immutable'
);

SELECT pg_temp.expect_error(
    ARRAY['55000'],
    $sql$UPDATE categories SET creation_payload_hash = decode(repeat('61', 32), 'hex')
    WHERE id = 'e4000000-0000-4000-8000-000000000001'$sql$,
    'category creation hash immutable'
);

SELECT pg_temp.expect_error(
    ARRAY['55000'],
    $sql$UPDATE transactions SET idempotency_key = 'changed-key'
    WHERE id = 'e5000000-0000-4000-8000-000000000001'$sql$,
    'transaction idempotency key immutable'
);

SELECT pg_temp.expect_error(
    ARRAY['55000'],
    $sql$UPDATE transactions
    SET idempotency_payload_hash = decode(repeat('64', 32), 'hex')
    WHERE id = 'e5000000-0000-4000-8000-000000000001'$sql$,
    'transaction fingerprint immutable'
);

SELECT pg_temp.expect_error(
    ARRAY['55000'],
    $sql$UPDATE transactions SET idempotency_payload_hash = decode(repeat('62', 32), 'hex')
    WHERE id = 'e5000000-0000-4000-8000-000000000002'$sql$,
    'legacy transaction fingerprint backfill forbidden'
);

INSERT INTO accounts (id, household_id, name, color) VALUES (
    'e3000000-0000-4000-8000-000000000003',
    'e1000000-0000-4000-8000-000000000001', 'Legacy account', '#ABCDEF'
);

SELECT pg_temp.expect_error(
    ARRAY['55000'],
    $sql$UPDATE accounts
    SET creation_idempotency_key = 'backfill',
        creation_payload_hash = decode(repeat('63', 32), 'hex')
    WHERE id = 'e3000000-0000-4000-8000-000000000003'$sql$,
    'legacy account metadata backfill forbidden'
);

UPDATE accounts
SET name = 'Account A updated', version = version + 1
WHERE id = 'e3000000-0000-4000-8000-000000000001';

UPDATE categories
SET sort_order = 10, version = version + 1
WHERE id = 'e4000000-0000-4000-8000-000000000001';

UPDATE transactions
SET note = 'normal update', version = version + 1
WHERE id = 'e5000000-0000-4000-8000-000000000001';

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM accounts
        WHERE id = 'e3000000-0000-4000-8000-000000000001'
          AND name = 'Account A updated' AND version = 2
    ) OR NOT EXISTS (
        SELECT 1 FROM categories
        WHERE id = 'e4000000-0000-4000-8000-000000000001'
          AND sort_order = 10 AND version = 2
    ) OR NOT EXISTS (
        SELECT 1 FROM transactions
        WHERE id = 'e5000000-0000-4000-8000-000000000001'
          AND note = 'normal update' AND version = 2
    ) THEN
        RAISE EXCEPTION 'normal business and version update failed';
    END IF;
END;
$$;

ROLLBACK;

\echo 'Finance core idempotency constraint tests: OK'
