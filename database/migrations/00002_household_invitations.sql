-- +goose Up
ALTER TABLE households
    ADD COLUMN creation_idempotency_key TEXT;

ALTER TABLE households
    ADD CONSTRAINT households_creation_idempotency_key_valid CHECK (
        creation_idempotency_key IS NULL
        OR (
            created_by_user_id IS NOT NULL
            AND
            creation_idempotency_key = btrim(creation_idempotency_key)
            AND creation_idempotency_key <> ''
            AND char_length(creation_idempotency_key) <= 255
        )
    );

CREATE UNIQUE INDEX uq_households_creator_idempotency_key
    ON households (created_by_user_id, creation_idempotency_key)
    WHERE creation_idempotency_key IS NOT NULL;

CREATE TABLE household_invitations (
    id UUID PRIMARY KEY,
    household_id UUID NOT NULL,
    role TEXT NOT NULL,
    token_hash BYTEA NOT NULL,
    request_idempotency_key TEXT NOT NULL,
    invited_by_user_id UUID NOT NULL,
    ttl_seconds INTEGER NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    accepted_at TIMESTAMPTZ,
    accepted_by_user_id UUID,
    revoked_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT household_invitations_household_id_unique UNIQUE (household_id, id),
    CONSTRAINT household_invitations_token_hash_unique UNIQUE (token_hash),
    CONSTRAINT household_invitations_request_unique UNIQUE (
        household_id,
        invited_by_user_id,
        request_idempotency_key
    ),
    CONSTRAINT household_invitations_role_valid CHECK (role IN ('admin', 'member')),
    CONSTRAINT household_invitations_token_hash_valid CHECK (octet_length(token_hash) = 32),
    CONSTRAINT household_invitations_request_key_valid CHECK (
        request_idempotency_key = btrim(request_idempotency_key)
        AND request_idempotency_key <> ''
        AND char_length(request_idempotency_key) <= 255
    ),
    CONSTRAINT household_invitations_expiry_valid CHECK (
        ttl_seconds BETWEEN 300 AND 2592000
        AND expires_at = created_at + make_interval(secs => ttl_seconds)
    ),
    CONSTRAINT household_invitations_acceptance_valid CHECK (
        (accepted_at IS NULL AND accepted_by_user_id IS NULL)
        OR (
            accepted_at IS NOT NULL
            AND accepted_by_user_id IS NOT NULL
            AND accepted_at >= created_at
            AND accepted_at <= expires_at
        )
    ),
    CONSTRAINT household_invitations_revocation_valid CHECK (
        revoked_at IS NULL OR revoked_at >= created_at
    ),
    CONSTRAINT household_invitations_terminal_state_valid CHECK (
        accepted_at IS NULL OR revoked_at IS NULL
    ),
    CONSTRAINT household_invitations_household_fk FOREIGN KEY (household_id)
        REFERENCES households (id) ON DELETE RESTRICT,
    CONSTRAINT household_invitations_inviter_member_fk FOREIGN KEY (
        household_id,
        invited_by_user_id
    ) REFERENCES household_members (household_id, user_id) ON DELETE RESTRICT,
    CONSTRAINT household_invitations_acceptor_member_fk FOREIGN KEY (
        household_id,
        accepted_by_user_id
    ) REFERENCES household_members (household_id, user_id) ON DELETE RESTRICT
);

CREATE INDEX idx_household_invitations_active
    ON household_invitations (household_id, expires_at, created_at DESC)
    WHERE accepted_at IS NULL AND revoked_at IS NULL;

ALTER TABLE audit_log
    DROP CONSTRAINT audit_log_entity_type_valid;

ALTER TABLE audit_log
    ADD CONSTRAINT audit_log_entity_type_valid CHECK (
        entity_type IN (
            'households', 'household_members', 'household_invitations',
            'accounts', 'categories', 'transactions', 'budgets', 'goals',
            'goal_contributions', 'debts', 'debt_payments',
            'recurring_transactions'
        )
    );

-- +goose Down
DROP TABLE IF EXISTS household_invitations;

-- Rolling back stage 3 removes invitation-specific audit events together with
-- the invitation objects they describe. The trigger is disabled only for this
-- bounded cleanup and is restored before the v1 constraint is reinstated.
ALTER TABLE audit_log DISABLE TRIGGER audit_log_prevent_delete;
DELETE FROM audit_log WHERE entity_type = 'household_invitations';
ALTER TABLE audit_log ENABLE TRIGGER audit_log_prevent_delete;

ALTER TABLE audit_log
    DROP CONSTRAINT audit_log_entity_type_valid;

ALTER TABLE audit_log
    ADD CONSTRAINT audit_log_entity_type_valid CHECK (
        entity_type IN (
            'households', 'household_members', 'accounts', 'categories', 'transactions',
            'budgets', 'goals', 'goal_contributions', 'debts', 'debt_payments',
            'recurring_transactions'
        )
    );

DROP INDEX IF EXISTS uq_households_creator_idempotency_key;
ALTER TABLE households DROP CONSTRAINT IF EXISTS households_creation_idempotency_key_valid;
ALTER TABLE households DROP COLUMN IF EXISTS creation_idempotency_key;
