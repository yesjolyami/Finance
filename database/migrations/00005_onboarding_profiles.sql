-- +goose Up
ALTER TABLE users
    ADD COLUMN usage_mode TEXT NOT NULL DEFAULT 'personal',
    ADD COLUMN onboarding_completed BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN primary_currency_code CHAR(3) NOT NULL DEFAULT 'RUB';

ALTER TABLE users
    ADD CONSTRAINT users_usage_mode_valid CHECK (usage_mode IN ('personal', 'couple', 'family', 'custom')),
    ADD CONSTRAINT users_primary_currency_rub_only CHECK (primary_currency_code = 'RUB');

-- Accounts created before this feature imply that the legacy user has already
-- configured the application. This keeps existing accounts out of onboarding.
UPDATE users u
SET onboarding_completed = TRUE
WHERE EXISTS (
    SELECT 1
    FROM household_members m
    JOIN households h ON h.id = m.household_id AND h.deleted_at IS NULL
    WHERE m.user_id = u.id AND m.status = 'active'
);

ALTER TABLE accounts DROP CONSTRAINT accounts_type_valid;
ALTER TABLE accounts
    ADD CONSTRAINT accounts_type_valid CHECK (account_type IN ('regular', 'savings', 'cash'));

ALTER TABLE transactions DROP CONSTRAINT transactions_shape_valid;
ALTER TABLE transactions
    ADD CONSTRAINT transactions_shape_valid CHECK (
        (
            transaction_type IN ('income', 'expense')
            AND to_account_id IS NULL
            AND (
                (is_balance_adjustment AND category_id IS NULL)
                OR (NOT is_balance_adjustment AND category_id IS NOT NULL)
            )
        )
        OR (
            transaction_type = 'transfer'
            AND category_id IS NULL
            AND to_account_id IS NOT NULL
            AND to_account_id <> account_id
            AND NOT is_balance_adjustment
        )
    );

-- +goose Down
UPDATE accounts SET account_type = 'regular' WHERE account_type = 'cash';

INSERT INTO categories (id, household_id, category_type, name, color, sort_order, is_system)
SELECT gen_random_uuid(), needed.household_id, needed.transaction_type,
       'Начальный баланс', '#5F714D', 1000000, TRUE
FROM (
    SELECT DISTINCT household_id, transaction_type
    FROM transactions
    WHERE is_balance_adjustment AND category_id IS NULL
) needed
ON CONFLICT DO NOTHING;

UPDATE transactions t
SET category_id = (
    SELECT c.id
    FROM categories c
    WHERE c.household_id = t.household_id
      AND c.category_type = t.transaction_type
      AND c.name = 'Начальный баланс'
    ORDER BY c.created_at, c.id
    LIMIT 1
)
WHERE t.is_balance_adjustment AND t.category_id IS NULL;

ALTER TABLE transactions DROP CONSTRAINT transactions_shape_valid;
ALTER TABLE transactions
    ADD CONSTRAINT transactions_shape_valid CHECK (
        (
            transaction_type IN ('income', 'expense')
            AND category_id IS NOT NULL
            AND to_account_id IS NULL
        )
        OR (
            transaction_type = 'transfer'
            AND category_id IS NULL
            AND to_account_id IS NOT NULL
            AND to_account_id <> account_id
        )
    );

ALTER TABLE accounts DROP CONSTRAINT accounts_type_valid;
ALTER TABLE accounts
    ADD CONSTRAINT accounts_type_valid CHECK (account_type IN ('regular', 'savings'));

ALTER TABLE users
    DROP CONSTRAINT users_primary_currency_rub_only,
    DROP CONSTRAINT users_usage_mode_valid,
    DROP COLUMN primary_currency_code,
    DROP COLUMN onboarding_completed,
    DROP COLUMN usage_mode;
