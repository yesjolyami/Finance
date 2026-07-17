\set ON_ERROR_STOP on

INSERT INTO users (id, auth_subject, display_name)
VALUES (
    'f1000000-0000-4000-8000-000000000001',
    'down-fixture-subject',
    'Down fixture user'
);

INSERT INTO households (id, name, created_by_user_id)
VALUES (
    'f2000000-0000-4000-8000-000000000001',
    'Down fixture household',
    'f1000000-0000-4000-8000-000000000001'
);

INSERT INTO household_members (id, household_id, user_id, role)
VALUES (
    'f3000000-0000-4000-8000-000000000001',
    'f2000000-0000-4000-8000-000000000001',
    'f1000000-0000-4000-8000-000000000001',
    'owner'
);

INSERT INTO household_invitations (
    id,
    household_id,
    role,
    token_hash,
    request_idempotency_key,
    invited_by_user_id,
    ttl_seconds,
    created_at,
    expires_at
) VALUES (
    'f4000000-0000-4000-8000-000000000001',
    'f2000000-0000-4000-8000-000000000001',
    'member',
    decode(repeat('ab', 32), 'hex'),
    'down-fixture-request',
    'f1000000-0000-4000-8000-000000000001',
    3600,
    '2026-01-01 00:00:00+00',
    '2026-01-01 01:00:00+00'
);

INSERT INTO audit_log (
    id,
    household_id,
    actor_user_id,
    entity_type,
    entity_id,
    action,
    changes
) VALUES (
    'f5000000-0000-4000-8000-000000000001',
    'f2000000-0000-4000-8000-000000000001',
    'f1000000-0000-4000-8000-000000000001',
    'household_invitations',
    'f4000000-0000-4000-8000-000000000001',
    'created',
    '{"fixture":"down-after-use"}'::jsonb
);

\echo 'Invitation rollback fixture: OK'
