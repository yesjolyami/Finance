\set ON_ERROR_STOP on

INSERT INTO users (id, auth_subject, display_name)
VALUES (
    'fa000000-0000-4000-8000-000000000001',
    'backup-v5-down-fixture-subject',
    'Backup v5 Down fixture'
);

INSERT INTO households (id, name, created_by_user_id)
VALUES (
    'fb000000-0000-4000-8000-000000000001',
    'Backup v5 Down household',
    'fa000000-0000-4000-8000-000000000001'
);

INSERT INTO household_members (id, household_id, user_id, role)
VALUES (
    'fc000000-0000-4000-8000-000000000001',
    'fb000000-0000-4000-8000-000000000001',
    'fa000000-0000-4000-8000-000000000001',
    'owner'
);

INSERT INTO accounts (id, household_id, name, color, version)
VALUES (
    'fd000000-0000-4000-8000-000000000001',
    'fb000000-0000-4000-8000-000000000001',
    'Imported account', '#123456', 41
);

INSERT INTO categories (id, household_id, category_type, name, color, version)
VALUES (
    'fd100000-0000-4000-8000-000000000001',
    'fb000000-0000-4000-8000-000000000001',
    'expense', 'Imported category', '#654321', 42
);

INSERT INTO transactions (
    id, household_id, transaction_type, account_id, category_id,
    amount_cents, event_date, source, version, created_by_user_id
) VALUES (
    'fd200000-0000-4000-8000-000000000001',
    'fb000000-0000-4000-8000-000000000001',
    'expense', 'fd000000-0000-4000-8000-000000000001',
    'fd100000-0000-4000-8000-000000000001',
    12345, DATE '2026-07-15', 'import', 43,
    'fa000000-0000-4000-8000-000000000001'
);

INSERT INTO budgets (
    id, household_id, category_id, budget_month, amount_cents
) VALUES (
    'fd300000-0000-4000-8000-000000000001',
    'fb000000-0000-4000-8000-000000000001',
    'fd100000-0000-4000-8000-000000000001', DATE '2026-07-01', 50000
);

INSERT INTO goals (
    id, household_id, name, target_amount_cents, initial_saved_cents, color
) VALUES (
    'fd400000-0000-4000-8000-000000000001',
    'fb000000-0000-4000-8000-000000000001',
    'Imported goal', 100000, 10000, '#224466'
);

INSERT INTO debts (
    id, household_id, person_label, direction, original_amount_cents
) VALUES (
    'fd500000-0000-4000-8000-000000000001',
    'fb000000-0000-4000-8000-000000000001',
    'Imported debt', 'i_owe', 75000
);

INSERT INTO debt_payments (
    id, household_id, debt_id, amount_cents, event_date, source
) VALUES (
    'fd600000-0000-4000-8000-000000000001',
    'fb000000-0000-4000-8000-000000000001',
    'fd500000-0000-4000-8000-000000000001',
    5000, DATE '2026-07-15', 'import'
);

INSERT INTO audit_log (
    id, household_id, actor_user_id, entity_type, entity_id, action, request_id, changes
) VALUES (
    'fe000000-0000-4000-8000-000000000001',
    'fb000000-0000-4000-8000-000000000001',
    'fa000000-0000-4000-8000-000000000001',
    'households', 'fb000000-0000-4000-8000-000000000001', 'imported',
    'backup-v5-down-fixture',
    '{"source":"migration-test","policyVersion":"backup-v5-import/1"}'::jsonb
);

INSERT INTO backup_v5_import_previews (
    id, household_id, actor_user_id, token_hash, backup_digest,
    budget_month, policy_version, expires_at, created_at
) VALUES (
    'fe100000-0000-4000-8000-000000000001',
    'fb000000-0000-4000-8000-000000000001',
    'fa000000-0000-4000-8000-000000000001',
    decode(repeat('71', 32), 'hex'), decode(repeat('72', 32), 'hex'),
    DATE '2026-07-01', 'backup-v5-import/1',
    TIMESTAMPTZ '2026-07-15 10:10:00+00', TIMESTAMPTZ '2026-07-15 10:00:00+00'
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
) VALUES (
    'fe200000-0000-4000-8000-000000000001',
    'fb000000-0000-4000-8000-000000000001',
    'fa000000-0000-4000-8000-000000000001',
    'backup-v5-key-2026-01',
    decode(repeat('81', 32), 'hex'), decode(repeat('82', 32), 'hex'),
    'backup-v5-import/1', 'completed',
    1, 1, 1, 1, 1, 0, 1, 1,
    0, 0, 0, 0, 0, 1,
    TIMESTAMPTZ '2026-07-15 10:02:00+00', TIMESTAMPTZ '2026-07-15 10:00:00+00'
);

\echo 'Backup v5 import rollback fixture: OK'
