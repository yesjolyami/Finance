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

INSERT INTO users (id, auth_subject, display_name) VALUES
    ('a0000000-0000-4000-8000-000000000001', 'a0000000-0000-4000-8000-000000000001', 'Профиль A'),
    ('a0000000-0000-4000-8000-000000000002', 'a0000000-0000-4000-8000-000000000002', 'Профиль B'),
    ('a0000000-0000-4000-8000-000000000003', 'a0000000-0000-4000-8000-000000000003', 'Профиль C');

INSERT INTO households (id, name, created_by_user_id, creation_idempotency_key) VALUES
    ('b0000000-0000-4000-8000-000000000001', 'Household A', 'a0000000-0000-4000-8000-000000000001', 'household-a'),
    ('b0000000-0000-4000-8000-000000000002', 'Household B', 'a0000000-0000-4000-8000-000000000002', 'household-b');

SELECT pg_temp.expect_error(
    ARRAY['23514'],
    $sql$INSERT INTO households (
        id, name, creation_idempotency_key
    ) VALUES (
        'b1000000-0000-4000-8000-000000000001', 'Без создателя', 'orphan-key'
    )$sql$,
    'household idempotency key requires creator'
);

SELECT pg_temp.expect_error(
    ARRAY['23505'],
    $sql$INSERT INTO households (
        id, name, created_by_user_id, creation_idempotency_key
    ) VALUES (
        'b1000000-0000-4000-8000-000000000002', 'Повтор A',
        'a0000000-0000-4000-8000-000000000001', 'household-a'
    )$sql$,
    'duplicate household creation key for creator'
);

INSERT INTO households (
    id, name, created_by_user_id, creation_idempotency_key
) VALUES (
    'b1000000-0000-4000-8000-000000000003', 'Такой же ключ, другой создатель',
    'a0000000-0000-4000-8000-000000000002', 'household-a'
);

INSERT INTO household_members (id, household_id, user_id, role) VALUES
    ('c0000000-0000-4000-8000-000000000001', 'b0000000-0000-4000-8000-000000000001', 'a0000000-0000-4000-8000-000000000001', 'owner'),
    ('c0000000-0000-4000-8000-000000000002', 'b0000000-0000-4000-8000-000000000002', 'a0000000-0000-4000-8000-000000000002', 'owner'),
    ('c0000000-0000-4000-8000-000000000003', 'b0000000-0000-4000-8000-000000000001', 'a0000000-0000-4000-8000-000000000003', 'member');

INSERT INTO household_invitations (
    id, household_id, role, token_hash, request_idempotency_key,
    invited_by_user_id, ttl_seconds, expires_at
) VALUES (
    'd0000000-0000-4000-8000-000000000001',
    'b0000000-0000-4000-8000-000000000001',
    'member', decode(repeat('11', 32), 'hex'), 'invite-a-1',
    'a0000000-0000-4000-8000-000000000001', 86400,
    CURRENT_TIMESTAMP + INTERVAL '1 day'
);

SELECT pg_temp.expect_error(
    ARRAY['23514'],
    $sql$INSERT INTO household_invitations (
        id, household_id, role, token_hash, request_idempotency_key,
        invited_by_user_id, ttl_seconds, expires_at
    ) VALUES (
        'd1000000-0000-4000-8000-000000000001',
        'b0000000-0000-4000-8000-000000000001',
        'owner', decode(repeat('12', 32), 'hex'), 'owner-role',
        'a0000000-0000-4000-8000-000000000001', 86400,
        CURRENT_TIMESTAMP + INTERVAL '1 day'
    )$sql$,
    'owner invitation role'
);

SELECT pg_temp.expect_error(
    ARRAY['23514'],
    $sql$INSERT INTO household_invitations (
        id, household_id, role, token_hash, request_idempotency_key,
        invited_by_user_id, ttl_seconds, expires_at
    ) VALUES (
        'd1000000-0000-4000-8000-000000000002',
        'b0000000-0000-4000-8000-000000000001',
        'member', decode('1234', 'hex'), 'short-hash',
        'a0000000-0000-4000-8000-000000000001', 86400,
        CURRENT_TIMESTAMP + INTERVAL '1 day'
    )$sql$,
    'short invitation hash'
);

SELECT pg_temp.expect_error(
    ARRAY['23503'],
    $sql$INSERT INTO household_invitations (
        id, household_id, role, token_hash, request_idempotency_key,
        invited_by_user_id, ttl_seconds, expires_at
    ) VALUES (
        'd1000000-0000-4000-8000-000000000003',
        'b0000000-0000-4000-8000-000000000001',
        'member', decode(repeat('13', 32), 'hex'), 'cross-inviter',
        'a0000000-0000-4000-8000-000000000002', 86400,
        CURRENT_TIMESTAMP + INTERVAL '1 day'
    )$sql$,
    'cross-household inviter'
);

SELECT pg_temp.expect_error(
    ARRAY['23514'],
    $sql$INSERT INTO household_invitations (
        id, household_id, role, token_hash, request_idempotency_key,
        invited_by_user_id, ttl_seconds, expires_at
    ) VALUES (
        'd1000000-0000-4000-8000-000000000004',
        'b0000000-0000-4000-8000-000000000001',
        'member', decode(repeat('14', 32), 'hex'), 'expired-at-create',
        'a0000000-0000-4000-8000-000000000001', 86400,
        CURRENT_TIMESTAMP - INTERVAL '1 second'
    )$sql$,
    'expired invitation'
);

SELECT pg_temp.expect_error(
    ARRAY['23505'],
    $sql$INSERT INTO household_invitations (
        id, household_id, role, token_hash, request_idempotency_key,
        invited_by_user_id, ttl_seconds, expires_at
    ) VALUES (
        'd1000000-0000-4000-8000-000000000005',
        'b0000000-0000-4000-8000-000000000001',
        'admin', decode(repeat('11', 32), 'hex'), 'duplicate-token',
        'a0000000-0000-4000-8000-000000000001', 86400,
        CURRENT_TIMESTAMP + INTERVAL '1 day'
    )$sql$,
    'duplicate invitation token hash'
);

SELECT pg_temp.expect_error(
    ARRAY['23505'],
    $sql$INSERT INTO household_invitations (
        id, household_id, role, token_hash, request_idempotency_key,
        invited_by_user_id, ttl_seconds, expires_at
    ) VALUES (
        'd1000000-0000-4000-8000-000000000006',
        'b0000000-0000-4000-8000-000000000001',
        'admin', decode(repeat('16', 32), 'hex'), 'invite-a-1',
        'a0000000-0000-4000-8000-000000000001', 86400,
        CURRENT_TIMESTAMP + INTERVAL '1 day'
    )$sql$,
    'duplicate invitation idempotency key'
);

SELECT pg_temp.expect_error(
    ARRAY['23514'],
    $sql$INSERT INTO household_invitations (
        id, household_id, role, token_hash, request_idempotency_key,
        invited_by_user_id, ttl_seconds, expires_at
    ) VALUES (
        'd1000000-0000-4000-8000-000000000007',
        'b0000000-0000-4000-8000-000000000001',
        'member', decode(repeat('17', 32), 'hex'), ' padded ',
        'a0000000-0000-4000-8000-000000000001', 86400,
        CURRENT_TIMESTAMP + INTERVAL '1 day'
    )$sql$,
    'invitation key surrounding whitespace'
);

SELECT pg_temp.expect_error(
    ARRAY['23514'],
    $sql$INSERT INTO household_invitations (
        id, household_id, role, token_hash, request_idempotency_key,
        invited_by_user_id, ttl_seconds, expires_at
    ) VALUES (
        'd1000000-0000-4000-8000-000000000008',
        'b0000000-0000-4000-8000-000000000001',
        'member', decode(repeat('18', 32), 'hex'), '',
        'a0000000-0000-4000-8000-000000000001', 86400,
        CURRENT_TIMESTAMP + INTERVAL '1 day'
    )$sql$,
    'empty invitation key'
);

SELECT pg_temp.expect_error(
    ARRAY['23514'],
    $sql$INSERT INTO household_invitations (
        id, household_id, role, token_hash, request_idempotency_key,
        invited_by_user_id, ttl_seconds, expires_at
    ) VALUES (
        'd1000000-0000-4000-8000-000000000009',
        'b0000000-0000-4000-8000-000000000001',
        'member', decode(repeat('19', 32), 'hex'), 'too-long-ttl',
        'a0000000-0000-4000-8000-000000000001', 2592001,
        CURRENT_TIMESTAMP + make_interval(secs => 2592001)
    )$sql$,
    'invitation ttl over thirty days'
);

SELECT pg_temp.expect_error(
    ARRAY['23514'],
    $sql$UPDATE household_invitations
    SET accepted_at = CURRENT_TIMESTAMP
    WHERE id = 'd0000000-0000-4000-8000-000000000001'$sql$,
    'accepted timestamp without user'
);

SELECT pg_temp.expect_error(
    ARRAY['23503'],
    $sql$UPDATE household_invitations
    SET accepted_at = CURRENT_TIMESTAMP,
        accepted_by_user_id = 'a0000000-0000-4000-8000-000000000002'
    WHERE id = 'd0000000-0000-4000-8000-000000000001'$sql$,
    'cross-household invitation acceptor'
);

SELECT pg_temp.expect_error(
    ARRAY['23514'],
    $sql$UPDATE household_invitations
    SET accepted_at = expires_at + INTERVAL '1 second',
        accepted_by_user_id = 'a0000000-0000-4000-8000-000000000003'
    WHERE id = 'd0000000-0000-4000-8000-000000000001'$sql$,
    'invitation accepted after expiry'
);

UPDATE household_invitations
SET accepted_at = CURRENT_TIMESTAMP,
    accepted_by_user_id = 'a0000000-0000-4000-8000-000000000003'
WHERE id = 'd0000000-0000-4000-8000-000000000001';

SELECT pg_temp.expect_error(
    ARRAY['23514'],
    $sql$UPDATE household_invitations
    SET revoked_at = CURRENT_TIMESTAMP
    WHERE id = 'd0000000-0000-4000-8000-000000000001'$sql$,
    'accepted and revoked invitation'
);

ROLLBACK;

\echo 'Household invitation constraint tests: OK'
