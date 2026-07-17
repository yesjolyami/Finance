\set ON_ERROR_STOP on

INSERT INTO users (id, auth_subject, display_name)
VALUES (
    'd0000000-0000-4000-8000-000000000001',
    'finance-down-fixture-subject',
    'Finance Down fixture'
);

INSERT INTO households (id, name, created_by_user_id)
VALUES (
    'd1000000-0000-4000-8000-000000000001',
    'Finance Down household',
    'd0000000-0000-4000-8000-000000000001'
);

INSERT INTO household_members (id, household_id, user_id, role)
VALUES (
    'd2000000-0000-4000-8000-000000000001',
    'd1000000-0000-4000-8000-000000000001',
    'd0000000-0000-4000-8000-000000000001',
    'owner'
);

INSERT INTO accounts (
    id, household_id, name, color, version,
    creation_idempotency_key, creation_payload_hash
) VALUES (
    'd3000000-0000-4000-8000-000000000001',
    'd1000000-0000-4000-8000-000000000001',
    'Finance Down account', '#123456', 4,
    'finance-down-account', decode(repeat('a1', 32), 'hex')
);

INSERT INTO categories (
    id, household_id, category_type, name, color, version,
    creation_idempotency_key, creation_payload_hash
) VALUES (
    'd4000000-0000-4000-8000-000000000001',
    'd1000000-0000-4000-8000-000000000001',
    'expense', 'Finance Down category', '#654321', 5,
    'finance-down-category', decode(repeat('b2', 32), 'hex')
);

INSERT INTO transactions (
    id, household_id, transaction_type, account_id, category_id,
    amount_cents, event_date, idempotency_key, idempotency_payload_hash, version,
    created_by_user_id
) VALUES (
    'd5000000-0000-4000-8000-000000000001',
    'd1000000-0000-4000-8000-000000000001',
    'expense',
    'd3000000-0000-4000-8000-000000000001',
    'd4000000-0000-4000-8000-000000000001',
    12345, DATE '2026-07-15',
    'finance-down-transaction', decode(repeat('c3', 32), 'hex'), 6,
    'd0000000-0000-4000-8000-000000000001'
);

INSERT INTO audit_log (
    id, household_id, actor_user_id, entity_type, entity_id, action, changes
) VALUES
    (
        'd6000000-0000-4000-8000-000000000001',
        'd1000000-0000-4000-8000-000000000001',
        'd0000000-0000-4000-8000-000000000001',
        'accounts', 'd3000000-0000-4000-8000-000000000001', 'created',
        '{"source":"migration-test"}'::jsonb
    ),
    (
        'd6000000-0000-4000-8000-000000000002',
        'd1000000-0000-4000-8000-000000000001',
        'd0000000-0000-4000-8000-000000000001',
        'categories', 'd4000000-0000-4000-8000-000000000001', 'created',
        '{"source":"migration-test"}'::jsonb
    ),
    (
        'd6000000-0000-4000-8000-000000000003',
        'd1000000-0000-4000-8000-000000000001',
        'd0000000-0000-4000-8000-000000000001',
        'transactions', 'd5000000-0000-4000-8000-000000000001', 'created',
        '{"source":"migration-test"}'::jsonb
    );

\echo 'Finance core rollback fixture: OK'
