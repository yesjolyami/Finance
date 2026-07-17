-- +goose Up
-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM pg_collation
        WHERE collprovider = 'i'
    ) THEN
        RAISE EXCEPTION 'PostgreSQL must be built with ICU support for category name normalization'
            USING ERRCODE = '0A000';
    END IF;
END;
$$;
-- +goose StatementEnd

CREATE COLLATION finance_category_name_ci (
    provider = icu,
    locale = 'und-u-ks-level2',
    deterministic = false
);

-- +goose StatementBegin
CREATE FUNCTION set_updated_at()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    NEW.updated_at = CURRENT_TIMESTAMP;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION prevent_audit_log_mutation()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'audit_log is append-only' USING ERRCODE = '55000';
END;
$$;
-- +goose StatementEnd

CREATE TABLE users (
    id UUID PRIMARY KEY,
    auth_subject TEXT UNIQUE,
    display_name TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMPTZ,
    CONSTRAINT users_auth_subject_valid CHECK (
        auth_subject IS NULL
        OR (btrim(auth_subject) <> '' AND char_length(auth_subject) <= 255)
    ),
    CONSTRAINT users_display_name_valid CHECK (
        btrim(display_name) <> '' AND char_length(display_name) <= 120
    )
);

CREATE TABLE households (
    id UUID PRIMARY KEY,
    name TEXT NOT NULL,
    currency_code CHAR(3) NOT NULL DEFAULT 'RUB',
    created_by_user_id UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    archived_at TIMESTAMPTZ,
    deleted_at TIMESTAMPTZ,
    CONSTRAINT households_name_valid CHECK (
        btrim(name) <> '' AND char_length(name) <= 120
    ),
    CONSTRAINT households_currency_rub_only CHECK (currency_code = 'RUB'),
    CONSTRAINT households_created_by_fk FOREIGN KEY (created_by_user_id)
        REFERENCES users (id) ON DELETE RESTRICT
);

CREATE TABLE household_members (
    id UUID PRIMARY KEY,
    household_id UUID NOT NULL,
    user_id UUID NOT NULL,
    role TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'active',
    joined_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    removed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT household_members_household_user_unique UNIQUE (household_id, user_id),
    CONSTRAINT household_members_role_valid CHECK (role IN ('owner', 'admin', 'member')),
    CONSTRAINT household_members_status_valid CHECK (status IN ('active', 'removed')),
    CONSTRAINT household_members_status_dates_valid CHECK (
        (status = 'active' AND removed_at IS NULL)
        OR (status = 'removed' AND removed_at IS NOT NULL)
    ),
    CONSTRAINT household_members_household_fk FOREIGN KEY (household_id)
        REFERENCES households (id) ON DELETE RESTRICT,
    CONSTRAINT household_members_user_fk FOREIGN KEY (user_id)
        REFERENCES users (id) ON DELETE RESTRICT
);

CREATE TABLE accounts (
    id UUID PRIMARY KEY,
    household_id UUID NOT NULL,
    name TEXT NOT NULL,
    color CHAR(7) NOT NULL,
    sort_order INTEGER NOT NULL DEFAULT 0,
    account_type TEXT NOT NULL DEFAULT 'regular',
    bank_label TEXT NOT NULL DEFAULT '',
    legacy_owner_label TEXT NOT NULL DEFAULT '',
    owner_user_id UUID,
    currency_code CHAR(3) NOT NULL DEFAULT 'RUB',
    is_system BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    archived_at TIMESTAMPTZ,
    deleted_at TIMESTAMPTZ,
    CONSTRAINT accounts_household_id_unique UNIQUE (household_id, id),
    CONSTRAINT accounts_name_valid CHECK (btrim(name) <> '' AND char_length(name) <= 120),
    CONSTRAINT accounts_color_valid CHECK (color ~ '^#[0-9A-Fa-f]{6}$'),
    CONSTRAINT accounts_sort_order_valid CHECK (sort_order >= 0),
    CONSTRAINT accounts_type_valid CHECK (account_type IN ('regular', 'savings')),
    CONSTRAINT accounts_bank_label_valid CHECK (char_length(bank_label) <= 120),
    CONSTRAINT accounts_legacy_owner_label_valid CHECK (char_length(legacy_owner_label) <= 120),
    CONSTRAINT accounts_currency_rub_only CHECK (currency_code = 'RUB'),
    CONSTRAINT accounts_household_fk FOREIGN KEY (household_id)
        REFERENCES households (id) ON DELETE RESTRICT,
    CONSTRAINT accounts_owner_member_fk FOREIGN KEY (household_id, owner_user_id)
        REFERENCES household_members (household_id, user_id) ON DELETE RESTRICT
);

CREATE TABLE categories (
    id UUID PRIMARY KEY,
    household_id UUID NOT NULL,
    category_type TEXT NOT NULL,
    name TEXT NOT NULL,
    color CHAR(7) NOT NULL,
    sort_order INTEGER NOT NULL DEFAULT 0,
    is_system BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    archived_at TIMESTAMPTZ,
    deleted_at TIMESTAMPTZ,
    CONSTRAINT categories_household_id_type_unique UNIQUE (household_id, id, category_type),
    CONSTRAINT categories_name_valid CHECK (btrim(name) <> '' AND char_length(name) <= 120),
    CONSTRAINT categories_type_valid CHECK (category_type IN ('income', 'expense')),
    CONSTRAINT categories_color_valid CHECK (color ~ '^#[0-9A-Fa-f]{6}$'),
    CONSTRAINT categories_sort_order_valid CHECK (sort_order >= 0),
    CONSTRAINT categories_household_fk FOREIGN KEY (household_id)
        REFERENCES households (id) ON DELETE RESTRICT
);

CREATE UNIQUE INDEX uq_categories_household_type_name_ci
    ON categories (
        household_id,
        category_type,
        (btrim(name) COLLATE finance_category_name_ci)
    );

CREATE TABLE budgets (
    id UUID PRIMARY KEY,
    household_id UUID NOT NULL,
    category_id UUID NOT NULL,
    category_type TEXT NOT NULL DEFAULT 'expense',
    budget_month DATE NOT NULL,
    amount_cents BIGINT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMPTZ,
    CONSTRAINT budgets_household_id_unique UNIQUE (household_id, id),
    CONSTRAINT budgets_expense_category_only CHECK (category_type = 'expense'),
    CONSTRAINT budgets_month_first_day CHECK (EXTRACT(DAY FROM budget_month) = 1),
    CONSTRAINT budgets_amount_valid CHECK (amount_cents BETWEEN 1 AND 9000000000000000),
    CONSTRAINT budgets_household_fk FOREIGN KEY (household_id)
        REFERENCES households (id) ON DELETE RESTRICT,
    CONSTRAINT budgets_category_fk FOREIGN KEY (household_id, category_id, category_type)
        REFERENCES categories (household_id, id, category_type) ON DELETE RESTRICT
);

CREATE UNIQUE INDEX uq_budgets_active_household_category_month
    ON budgets (household_id, category_id, budget_month)
    WHERE deleted_at IS NULL;

CREATE TABLE goals (
    id UUID PRIMARY KEY,
    household_id UUID NOT NULL,
    name TEXT NOT NULL,
    target_amount_cents BIGINT NOT NULL,
    initial_saved_cents BIGINT NOT NULL DEFAULT 0,
    target_date DATE,
    color CHAR(7) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    archived_at TIMESTAMPTZ,
    deleted_at TIMESTAMPTZ,
    CONSTRAINT goals_household_id_unique UNIQUE (household_id, id),
    CONSTRAINT goals_name_valid CHECK (btrim(name) <> '' AND char_length(name) <= 120),
    CONSTRAINT goals_target_amount_valid CHECK (target_amount_cents BETWEEN 1 AND 9000000000000000),
    CONSTRAINT goals_initial_saved_valid CHECK (initial_saved_cents BETWEEN 0 AND 9000000000000000),
    CONSTRAINT goals_color_valid CHECK (color ~ '^#[0-9A-Fa-f]{6}$'),
    CONSTRAINT goals_household_fk FOREIGN KEY (household_id)
        REFERENCES households (id) ON DELETE RESTRICT
);

CREATE TABLE goal_contributions (
    id UUID PRIMARY KEY,
    household_id UUID NOT NULL,
    goal_id UUID NOT NULL,
    amount_cents BIGINT NOT NULL,
    event_date DATE NOT NULL,
    note TEXT NOT NULL DEFAULT '',
    source TEXT NOT NULL DEFAULT 'manual',
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMPTZ,
    CONSTRAINT goal_contributions_household_id_unique UNIQUE (household_id, id),
    CONSTRAINT goal_contributions_amount_valid CHECK (amount_cents BETWEEN 1 AND 9000000000000000),
    CONSTRAINT goal_contributions_note_valid CHECK (char_length(note) <= 1000),
    CONSTRAINT goal_contributions_source_valid CHECK (source IN ('manual', 'import', 'recurring', 'system')),
    CONSTRAINT goal_contributions_household_fk FOREIGN KEY (household_id)
        REFERENCES households (id) ON DELETE RESTRICT,
    CONSTRAINT goal_contributions_goal_fk FOREIGN KEY (household_id, goal_id)
        REFERENCES goals (household_id, id) ON DELETE RESTRICT
);

CREATE TABLE debts (
    id UUID PRIMARY KEY,
    household_id UUID NOT NULL,
    person_label TEXT NOT NULL,
    direction TEXT NOT NULL,
    original_amount_cents BIGINT NOT NULL,
    due_date DATE,
    note TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    archived_at TIMESTAMPTZ,
    deleted_at TIMESTAMPTZ,
    CONSTRAINT debts_household_id_unique UNIQUE (household_id, id),
    CONSTRAINT debts_person_label_valid CHECK (btrim(person_label) <> '' AND char_length(person_label) <= 120),
    CONSTRAINT debts_direction_valid CHECK (direction IN ('owe_me', 'i_owe')),
    CONSTRAINT debts_original_amount_valid CHECK (original_amount_cents BETWEEN 1 AND 9000000000000000),
    CONSTRAINT debts_note_valid CHECK (char_length(note) <= 1000),
    CONSTRAINT debts_household_fk FOREIGN KEY (household_id)
        REFERENCES households (id) ON DELETE RESTRICT
);

CREATE TABLE debt_payments (
    id UUID PRIMARY KEY,
    household_id UUID NOT NULL,
    debt_id UUID NOT NULL,
    amount_cents BIGINT NOT NULL,
    event_date DATE NOT NULL,
    note TEXT NOT NULL DEFAULT '',
    source TEXT NOT NULL DEFAULT 'manual',
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMPTZ,
    CONSTRAINT debt_payments_household_id_unique UNIQUE (household_id, id),
    CONSTRAINT debt_payments_amount_valid CHECK (amount_cents BETWEEN 1 AND 9000000000000000),
    CONSTRAINT debt_payments_note_valid CHECK (char_length(note) <= 1000),
    CONSTRAINT debt_payments_source_valid CHECK (source IN ('manual', 'import', 'recurring', 'system')),
    CONSTRAINT debt_payments_household_fk FOREIGN KEY (household_id)
        REFERENCES households (id) ON DELETE RESTRICT,
    CONSTRAINT debt_payments_debt_fk FOREIGN KEY (household_id, debt_id)
        REFERENCES debts (household_id, id) ON DELETE RESTRICT
);

CREATE TABLE recurring_transactions (
    id UUID PRIMARY KEY,
    household_id UUID NOT NULL,
    transaction_type TEXT NOT NULL,
    account_id UUID NOT NULL,
    to_account_id UUID,
    category_id UUID,
    amount_cents BIGINT NOT NULL,
    note TEXT NOT NULL DEFAULT '',
    frequency TEXT NOT NULL,
    interval_count INTEGER NOT NULL DEFAULT 1,
    next_execution_date DATE NOT NULL,
    end_date DATE,
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    archived_at TIMESTAMPTZ,
    deleted_at TIMESTAMPTZ,
    CONSTRAINT recurring_transactions_household_id_unique UNIQUE (household_id, id),
    CONSTRAINT recurring_transactions_type_valid CHECK (transaction_type IN ('income', 'expense', 'transfer')),
    CONSTRAINT recurring_transactions_amount_valid CHECK (amount_cents BETWEEN 1 AND 9000000000000000),
    CONSTRAINT recurring_transactions_note_valid CHECK (char_length(note) <= 1000),
    CONSTRAINT recurring_transactions_frequency_valid CHECK (frequency IN ('daily', 'weekly', 'monthly', 'yearly')),
    CONSTRAINT recurring_transactions_interval_valid CHECK (interval_count BETWEEN 1 AND 1000),
    CONSTRAINT recurring_transactions_end_date_valid CHECK (end_date IS NULL OR end_date >= next_execution_date),
    CONSTRAINT recurring_transactions_shape_valid CHECK (
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
    ),
    CONSTRAINT recurring_transactions_household_fk FOREIGN KEY (household_id)
        REFERENCES households (id) ON DELETE RESTRICT,
    CONSTRAINT recurring_transactions_account_fk FOREIGN KEY (household_id, account_id)
        REFERENCES accounts (household_id, id) ON DELETE RESTRICT,
    CONSTRAINT recurring_transactions_to_account_fk FOREIGN KEY (household_id, to_account_id)
        REFERENCES accounts (household_id, id) ON DELETE RESTRICT,
    CONSTRAINT recurring_transactions_category_fk FOREIGN KEY (household_id, category_id, transaction_type)
        REFERENCES categories (household_id, id, category_type) ON DELETE RESTRICT
);

CREATE TABLE transactions (
    id UUID PRIMARY KEY,
    household_id UUID NOT NULL,
    transaction_type TEXT NOT NULL,
    account_id UUID NOT NULL,
    to_account_id UUID,
    category_id UUID,
    amount_cents BIGINT NOT NULL,
    event_date DATE NOT NULL,
    note TEXT NOT NULL DEFAULT '',
    is_balance_adjustment BOOLEAN NOT NULL DEFAULT FALSE,
    source TEXT NOT NULL DEFAULT 'manual',
    idempotency_key TEXT,
    created_by_user_id UUID,
    updated_by_user_id UUID,
    deleted_by_user_id UUID,
    deletion_reason TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMPTZ,
    CONSTRAINT transactions_household_id_unique UNIQUE (household_id, id),
    CONSTRAINT transactions_type_valid CHECK (transaction_type IN ('income', 'expense', 'transfer')),
    CONSTRAINT transactions_amount_valid CHECK (amount_cents BETWEEN 1 AND 9000000000000000),
    CONSTRAINT transactions_note_valid CHECK (char_length(note) <= 1000),
    CONSTRAINT transactions_source_valid CHECK (source IN ('manual', 'import', 'recurring', 'system')),
    CONSTRAINT transactions_idempotency_key_valid CHECK (
        idempotency_key IS NULL
        OR (btrim(idempotency_key) <> '' AND char_length(idempotency_key) <= 255)
    ),
    CONSTRAINT transactions_deletion_reason_valid CHECK (
        deletion_reason IS NULL OR char_length(deletion_reason) <= 500
    ),
    CONSTRAINT transactions_shape_valid CHECK (
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
    ),
    CONSTRAINT transactions_balance_adjustment_valid CHECK (
        NOT is_balance_adjustment OR transaction_type IN ('income', 'expense')
    ),
    CONSTRAINT transactions_soft_delete_metadata_valid CHECK (
        (deleted_at IS NULL AND deleted_by_user_id IS NULL AND deletion_reason IS NULL)
        OR deleted_at IS NOT NULL
    ),
    CONSTRAINT transactions_household_fk FOREIGN KEY (household_id)
        REFERENCES households (id) ON DELETE RESTRICT,
    CONSTRAINT transactions_account_fk FOREIGN KEY (household_id, account_id)
        REFERENCES accounts (household_id, id) ON DELETE RESTRICT,
    CONSTRAINT transactions_to_account_fk FOREIGN KEY (household_id, to_account_id)
        REFERENCES accounts (household_id, id) ON DELETE RESTRICT,
    CONSTRAINT transactions_category_fk FOREIGN KEY (household_id, category_id, transaction_type)
        REFERENCES categories (household_id, id, category_type) ON DELETE RESTRICT,
    CONSTRAINT transactions_created_by_member_fk FOREIGN KEY (household_id, created_by_user_id)
        REFERENCES household_members (household_id, user_id) ON DELETE RESTRICT,
    CONSTRAINT transactions_updated_by_member_fk FOREIGN KEY (household_id, updated_by_user_id)
        REFERENCES household_members (household_id, user_id) ON DELETE RESTRICT,
    CONSTRAINT transactions_deleted_by_member_fk FOREIGN KEY (household_id, deleted_by_user_id)
        REFERENCES household_members (household_id, user_id) ON DELETE RESTRICT
);

CREATE UNIQUE INDEX uq_transactions_household_idempotency_key
    ON transactions (household_id, idempotency_key)
    WHERE idempotency_key IS NOT NULL;

CREATE TABLE audit_log (
    id UUID PRIMARY KEY,
    household_id UUID NOT NULL,
    actor_user_id UUID,
    entity_type TEXT NOT NULL,
    entity_id UUID NOT NULL,
    action TEXT NOT NULL,
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    request_id TEXT,
    changes JSONB NOT NULL DEFAULT '{}'::jsonb,
    CONSTRAINT audit_log_household_id_unique UNIQUE (household_id, id),
    CONSTRAINT audit_log_entity_type_valid CHECK (
        entity_type IN (
            'households', 'household_members', 'accounts', 'categories', 'transactions',
            'budgets', 'goals', 'goal_contributions', 'debts', 'debt_payments',
            'recurring_transactions'
        )
    ),
    CONSTRAINT audit_log_action_valid CHECK (
        action IN ('created', 'updated', 'archived', 'deleted', 'restored', 'imported')
    ),
    CONSTRAINT audit_log_request_id_valid CHECK (
        request_id IS NULL OR (btrim(request_id) <> '' AND char_length(request_id) <= 255)
    ),
    CONSTRAINT audit_log_changes_object CHECK (jsonb_typeof(changes) = 'object'),
    CONSTRAINT audit_log_household_fk FOREIGN KEY (household_id)
        REFERENCES households (id) ON DELETE RESTRICT,
    CONSTRAINT audit_log_actor_member_fk FOREIGN KEY (household_id, actor_user_id)
        REFERENCES household_members (household_id, user_id) ON DELETE RESTRICT
);

CREATE INDEX idx_household_members_user_status
    ON household_members (user_id, status);
CREATE INDEX idx_accounts_household_active_sort
    ON accounts (household_id, archived_at, sort_order, name);
CREATE INDEX idx_categories_household_type_active
    ON categories (household_id, category_type, archived_at);
CREATE INDEX idx_budgets_household_month
    ON budgets (household_id, budget_month);
CREATE INDEX idx_goals_household_active
    ON goals (household_id, archived_at, created_at DESC);
CREATE INDEX idx_goal_contributions_goal_date
    ON goal_contributions (household_id, goal_id, event_date DESC);
CREATE INDEX idx_debts_household_active
    ON debts (household_id, archived_at, created_at DESC);
CREATE INDEX idx_debt_payments_debt_date
    ON debt_payments (household_id, debt_id, event_date DESC);
CREATE INDEX idx_recurring_transactions_next_active
    ON recurring_transactions (household_id, next_execution_date)
    WHERE is_active AND archived_at IS NULL AND deleted_at IS NULL;
CREATE INDEX idx_transactions_household_date
    ON transactions (household_id, event_date DESC, created_at DESC);
CREATE INDEX idx_transactions_source_account
    ON transactions (household_id, account_id, event_date DESC);
CREATE INDEX idx_transactions_destination_account
    ON transactions (household_id, to_account_id, event_date DESC)
    WHERE to_account_id IS NOT NULL;
CREATE INDEX idx_transactions_category
    ON transactions (household_id, category_id, event_date DESC)
    WHERE category_id IS NOT NULL;
CREATE INDEX idx_audit_log_household_time
    ON audit_log (household_id, occurred_at DESC);
CREATE INDEX idx_audit_log_entity
    ON audit_log (household_id, entity_type, entity_id, occurred_at DESC);

CREATE TRIGGER users_set_updated_at
    BEFORE UPDATE ON users
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER households_set_updated_at
    BEFORE UPDATE ON households
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER household_members_set_updated_at
    BEFORE UPDATE ON household_members
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER accounts_set_updated_at
    BEFORE UPDATE ON accounts
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER categories_set_updated_at
    BEFORE UPDATE ON categories
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER budgets_set_updated_at
    BEFORE UPDATE ON budgets
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER goals_set_updated_at
    BEFORE UPDATE ON goals
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER goal_contributions_set_updated_at
    BEFORE UPDATE ON goal_contributions
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER debts_set_updated_at
    BEFORE UPDATE ON debts
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER debt_payments_set_updated_at
    BEFORE UPDATE ON debt_payments
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER recurring_transactions_set_updated_at
    BEFORE UPDATE ON recurring_transactions
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER transactions_set_updated_at
    BEFORE UPDATE ON transactions
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER audit_log_prevent_update
    BEFORE UPDATE ON audit_log
    FOR EACH ROW EXECUTE FUNCTION prevent_audit_log_mutation();
CREATE TRIGGER audit_log_prevent_delete
    BEFORE DELETE ON audit_log
    FOR EACH ROW EXECUTE FUNCTION prevent_audit_log_mutation();

-- +goose Down
DROP TABLE IF EXISTS audit_log;
DROP TABLE IF EXISTS transactions;
DROP TABLE IF EXISTS recurring_transactions;
DROP TABLE IF EXISTS debt_payments;
DROP TABLE IF EXISTS debts;
DROP TABLE IF EXISTS goal_contributions;
DROP TABLE IF EXISTS goals;
DROP TABLE IF EXISTS budgets;
DROP TABLE IF EXISTS categories;
DROP TABLE IF EXISTS accounts;
DROP TABLE IF EXISTS household_members;
DROP TABLE IF EXISTS households;
DROP TABLE IF EXISTS users;
DROP COLLATION IF EXISTS finance_category_name_ci;
DROP FUNCTION IF EXISTS prevent_audit_log_mutation();
DROP FUNCTION IF EXISTS set_updated_at();
