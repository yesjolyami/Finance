-- +goose Up
CREATE TABLE backup_v5_import_previews (
    id UUID PRIMARY KEY,
    household_id UUID NOT NULL,
    actor_user_id UUID NOT NULL,
    token_hash BYTEA NOT NULL,
    backup_digest BYTEA NOT NULL,
    budget_month DATE NOT NULL,
    policy_version TEXT NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    consumed_at TIMESTAMPTZ,
    revoked_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT backup_v5_import_previews_token_hash_unique UNIQUE (token_hash),
    CONSTRAINT backup_v5_import_previews_token_hash_valid CHECK (
        octet_length(token_hash) = 32
    ),
    CONSTRAINT backup_v5_import_previews_digest_valid CHECK (
        octet_length(backup_digest) = 32
    ),
    CONSTRAINT backup_v5_import_previews_budget_month_valid CHECK (
        EXTRACT(DAY FROM budget_month) = 1
    ),
    CONSTRAINT backup_v5_import_previews_policy_valid CHECK (
        policy_version = 'backup-v5-import/1'
    ),
    CONSTRAINT backup_v5_import_previews_ttl_valid CHECK (
        expires_at > created_at
        AND expires_at <= created_at + INTERVAL '15 minutes'
    ),
    CONSTRAINT backup_v5_import_previews_consumed_valid CHECK (
        consumed_at IS NULL
        OR (consumed_at >= created_at AND consumed_at <= expires_at)
    ),
    CONSTRAINT backup_v5_import_previews_revoked_valid CHECK (
        revoked_at IS NULL
        OR revoked_at >= created_at
    ),
    CONSTRAINT backup_v5_import_previews_terminal_state_valid CHECK (
        consumed_at IS NULL OR revoked_at IS NULL
    ),
    CONSTRAINT backup_v5_import_previews_household_fk FOREIGN KEY (household_id)
        REFERENCES households (id) ON DELETE RESTRICT,
    CONSTRAINT backup_v5_import_previews_actor_member_fk FOREIGN KEY (
        household_id,
        actor_user_id
    ) REFERENCES household_members (household_id, user_id) ON DELETE RESTRICT
);

CREATE INDEX idx_backup_v5_import_previews_active_expiry
    ON backup_v5_import_previews (expires_at, created_at)
    WHERE consumed_at IS NULL AND revoked_at IS NULL;

CREATE INDEX idx_backup_v5_import_previews_terminal_cleanup
    ON backup_v5_import_previews (
        (COALESCE(consumed_at, revoked_at)),
        created_at
    )
    WHERE consumed_at IS NOT NULL OR revoked_at IS NOT NULL;

CREATE INDEX idx_backup_v5_import_previews_household_active
    ON backup_v5_import_previews (household_id, expires_at, created_at)
    WHERE consumed_at IS NULL AND revoked_at IS NULL;

-- +goose StatementBegin
CREATE FUNCTION backup_v5_import_preview_update_guard()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.id IS DISTINCT FROM OLD.id
        OR NEW.household_id IS DISTINCT FROM OLD.household_id
        OR NEW.actor_user_id IS DISTINCT FROM OLD.actor_user_id
        OR NEW.token_hash IS DISTINCT FROM OLD.token_hash
        OR NEW.backup_digest IS DISTINCT FROM OLD.backup_digest
        OR NEW.budget_month IS DISTINCT FROM OLD.budget_month
        OR NEW.policy_version IS DISTINCT FROM OLD.policy_version
        OR NEW.expires_at IS DISTINCT FROM OLD.expires_at
        OR NEW.created_at IS DISTINCT FROM OLD.created_at
    THEN
        RAISE EXCEPTION 'backup v5 import preview binding metadata is immutable'
            USING ERRCODE = 'P0001';
    END IF;

    IF OLD.consumed_at IS NOT NULL OR OLD.revoked_at IS NOT NULL THEN
        RAISE EXCEPTION 'backup v5 import preview terminal state is immutable'
            USING ERRCODE = 'P0001';
    END IF;

    IF NOT (
        (NEW.consumed_at IS NOT NULL AND NEW.revoked_at IS NULL)
        OR (NEW.consumed_at IS NULL AND NEW.revoked_at IS NOT NULL)
    ) THEN
        RAISE EXCEPTION 'backup v5 import preview requires one terminal transition'
            USING ERRCODE = 'P0001';
    END IF;

    RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER backup_v5_import_previews_update_guard
BEFORE UPDATE ON backup_v5_import_previews
FOR EACH ROW
EXECUTE FUNCTION backup_v5_import_preview_update_guard();

CREATE TABLE backup_v5_import_runs (
    id UUID PRIMARY KEY,
    household_id UUID NOT NULL,
    actor_user_id UUID NOT NULL,
    hmac_key_id TEXT NOT NULL,
    idempotency_key_hmac BYTEA NOT NULL,
    request_fingerprint_hmac BYTEA NOT NULL,
    policy_version TEXT NOT NULL,
    status TEXT NOT NULL,
    accounts_count INTEGER NOT NULL,
    categories_count INTEGER NOT NULL,
    transactions_count INTEGER NOT NULL,
    budgets_count INTEGER NOT NULL,
    goals_count INTEGER NOT NULL,
    goal_contributions_count INTEGER NOT NULL,
    debts_count INTEGER NOT NULL,
    debt_payments_count INTEGER NOT NULL,
    legacy_owner_not_linked_count INTEGER NOT NULL,
    archive_time_approximated_count INTEGER NOT NULL,
    goal_exceeds_target_count INTEGER NOT NULL,
    debt_overpaid_count INTEGER NOT NULL,
    system_resource_preserved_count INTEGER NOT NULL,
    budget_month_explicit_choice_count INTEGER NOT NULL,
    completed_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT backup_v5_import_runs_idempotency_unique UNIQUE (
        household_id,
        actor_user_id,
        hmac_key_id,
        idempotency_key_hmac
    ),
    CONSTRAINT backup_v5_import_runs_hmac_key_id_valid CHECK (
        hmac_key_id ~ '^[A-Za-z0-9._-]{1,64}$'
    ),
    CONSTRAINT backup_v5_import_runs_idempotency_hmac_valid CHECK (
        octet_length(idempotency_key_hmac) = 32
    ),
    CONSTRAINT backup_v5_import_runs_fingerprint_hmac_valid CHECK (
        octet_length(request_fingerprint_hmac) = 32
    ),
    CONSTRAINT backup_v5_import_runs_policy_valid CHECK (
        policy_version = 'backup-v5-import/1'
    ),
    CONSTRAINT backup_v5_import_runs_status_valid CHECK (
        status = 'completed'
    ),
    CONSTRAINT backup_v5_import_runs_accounts_count_valid CHECK (
        accounts_count BETWEEN 0 AND 10000
    ),
    CONSTRAINT backup_v5_import_runs_categories_count_valid CHECK (
        categories_count BETWEEN 0 AND 20000
    ),
    CONSTRAINT backup_v5_import_runs_transactions_count_valid CHECK (
        transactions_count BETWEEN 0 AND 200000
    ),
    CONSTRAINT backup_v5_import_runs_budgets_count_valid CHECK (
        budgets_count BETWEEN 0 AND 20000
    ),
    CONSTRAINT backup_v5_import_runs_goals_count_valid CHECK (
        goals_count BETWEEN 0 AND 20000
    ),
    CONSTRAINT backup_v5_import_runs_goal_contributions_count_valid CHECK (
        goal_contributions_count = 0
    ),
    CONSTRAINT backup_v5_import_runs_debts_count_valid CHECK (
        debts_count BETWEEN 0 AND 20000
    ),
    CONSTRAINT backup_v5_import_runs_debt_payments_count_valid CHECK (
        debt_payments_count BETWEEN 0 AND 200000
    ),
    CONSTRAINT backup_v5_import_runs_legacy_owner_warning_count_valid CHECK (
        legacy_owner_not_linked_count BETWEEN 0 AND 300000
    ),
    CONSTRAINT backup_v5_import_runs_archive_warning_count_valid CHECK (
        archive_time_approximated_count BETWEEN 0 AND 300000
    ),
    CONSTRAINT backup_v5_import_runs_goal_warning_count_valid CHECK (
        goal_exceeds_target_count BETWEEN 0 AND 300000
    ),
    CONSTRAINT backup_v5_import_runs_debt_warning_count_valid CHECK (
        debt_overpaid_count BETWEEN 0 AND 300000
    ),
    CONSTRAINT backup_v5_import_runs_system_warning_count_valid CHECK (
        system_resource_preserved_count BETWEEN 0 AND 300000
    ),
    CONSTRAINT backup_v5_import_runs_budget_warning_count_valid CHECK (
        budget_month_explicit_choice_count BETWEEN 0 AND 1
    ),
    CONSTRAINT backup_v5_import_runs_total_count_valid CHECK (
        accounts_count
        + categories_count
        + transactions_count
        + goals_count
        + debts_count
        + debt_payments_count <= 300000
    ),
    CONSTRAINT backup_v5_import_runs_warning_relations_valid CHECK (
        legacy_owner_not_linked_count <= accounts_count
        AND archive_time_approximated_count <= goals_count + debts_count
        AND goal_exceeds_target_count <= goals_count
        AND debt_overpaid_count <= debts_count
        AND system_resource_preserved_count <= accounts_count + categories_count
    ),
    CONSTRAINT backup_v5_import_runs_completed_time_valid CHECK (
        completed_at >= created_at
    ),
    CONSTRAINT backup_v5_import_runs_household_fk FOREIGN KEY (household_id)
        REFERENCES households (id) ON DELETE RESTRICT,
    CONSTRAINT backup_v5_import_runs_actor_member_fk FOREIGN KEY (
        household_id,
        actor_user_id
    ) REFERENCES household_members (household_id, user_id) ON DELETE RESTRICT
);

-- The application keeps a bounded HMAC keyring. It computes one candidate per
-- retained key ID and looks up the unique tenant/actor/key-ID/HMAC tuple. The key
-- ID is non-secret; raw HMAC keys never enter PostgreSQL.
CREATE INDEX idx_backup_v5_import_runs_hmac_key_id
    ON backup_v5_import_runs (hmac_key_id);

-- +goose StatementBegin
CREATE FUNCTION backup_v5_import_run_append_only_guard()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'completed backup v5 import results are append-only'
        USING ERRCODE = 'P0001';
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER backup_v5_import_runs_append_only
BEFORE UPDATE OR DELETE ON backup_v5_import_runs
FOR EACH ROW
EXECUTE FUNCTION backup_v5_import_run_append_only_guard();

-- +goose Down
-- This migration owns control metadata only. Imported financial rows and the
-- existing households/imported audit history deliberately have no reverse FK to
-- these tables and must survive rollback.
DROP TABLE IF EXISTS backup_v5_import_runs;
DROP TABLE IF EXISTS backup_v5_import_previews;
DROP FUNCTION IF EXISTS backup_v5_import_run_append_only_guard();
DROP FUNCTION IF EXISTS backup_v5_import_preview_update_guard();
