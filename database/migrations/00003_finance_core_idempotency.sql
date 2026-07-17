-- +goose Up
ALTER TABLE accounts
    ADD COLUMN version BIGINT NOT NULL DEFAULT 1,
    ADD COLUMN creation_idempotency_key TEXT,
    ADD COLUMN creation_payload_hash BYTEA;

ALTER TABLE accounts
    ADD CONSTRAINT accounts_version_positive CHECK (version > 0),
    ADD CONSTRAINT accounts_creation_metadata_valid CHECK (
        (
            creation_idempotency_key IS NULL
            AND creation_payload_hash IS NULL
        )
        OR (
            creation_idempotency_key IS NOT NULL
            AND creation_payload_hash IS NOT NULL
            AND creation_idempotency_key = btrim(creation_idempotency_key)
            AND creation_idempotency_key <> ''
            AND char_length(creation_idempotency_key) <= 255
            AND octet_length(creation_payload_hash) = 32
        )
    );

CREATE UNIQUE INDEX uq_accounts_household_creation_idempotency_key
    ON accounts (household_id, creation_idempotency_key)
    WHERE creation_idempotency_key IS NOT NULL;

ALTER TABLE categories
    ADD COLUMN version BIGINT NOT NULL DEFAULT 1,
    ADD COLUMN creation_idempotency_key TEXT,
    ADD COLUMN creation_payload_hash BYTEA;

ALTER TABLE categories
    ADD CONSTRAINT categories_version_positive CHECK (version > 0),
    ADD CONSTRAINT categories_creation_metadata_valid CHECK (
        (
            creation_idempotency_key IS NULL
            AND creation_payload_hash IS NULL
        )
        OR (
            creation_idempotency_key IS NOT NULL
            AND creation_payload_hash IS NOT NULL
            AND creation_idempotency_key = btrim(creation_idempotency_key)
            AND creation_idempotency_key <> ''
            AND char_length(creation_idempotency_key) <= 255
            AND octet_length(creation_payload_hash) = 32
        )
    );

CREATE UNIQUE INDEX uq_categories_household_creation_idempotency_key
    ON categories (household_id, creation_idempotency_key)
    WHERE creation_idempotency_key IS NOT NULL;

ALTER TABLE transactions
    ADD COLUMN version BIGINT NOT NULL DEFAULT 1,
    ADD COLUMN idempotency_payload_hash BYTEA;

ALTER TABLE transactions
    ADD CONSTRAINT transactions_version_positive CHECK (version > 0),
    ADD CONSTRAINT transactions_idempotency_payload_hash_valid CHECK (
        idempotency_payload_hash IS NULL
        OR (
            idempotency_key IS NOT NULL
            AND idempotency_key = btrim(idempotency_key)
            AND idempotency_key <> ''
            AND char_length(idempotency_key) <= 255
            AND octet_length(idempotency_payload_hash) = 32
        )
    );

-- Creation metadata is insert-only. This also rejects NULL-to-value backfills on
-- legacy rows; the stage 4 API creates replayable resources with one INSERT.
-- +goose StatementBegin
CREATE FUNCTION prevent_finance_creation_metadata_update()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_TABLE_NAME IN ('accounts', 'categories') THEN
        IF (to_jsonb(NEW) -> 'creation_idempotency_key') IS DISTINCT FROM
                (to_jsonb(OLD) -> 'creation_idempotency_key')
            OR (to_jsonb(NEW) -> 'creation_payload_hash') IS DISTINCT FROM
                (to_jsonb(OLD) -> 'creation_payload_hash') THEN
            RAISE EXCEPTION 'finance creation metadata is immutable'
                USING ERRCODE = '55000';
        END IF;
    ELSIF TG_TABLE_NAME = 'transactions' THEN
        IF NEW.idempotency_key IS DISTINCT FROM OLD.idempotency_key
            OR NEW.idempotency_payload_hash IS DISTINCT FROM OLD.idempotency_payload_hash THEN
            RAISE EXCEPTION 'finance creation metadata is immutable'
                USING ERRCODE = '55000';
        END IF;
    ELSE
        RAISE EXCEPTION 'unexpected table for finance creation metadata trigger'
            USING ERRCODE = '55000';
    END IF;

    RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER accounts_prevent_creation_metadata_update
    BEFORE UPDATE ON accounts
    FOR EACH ROW EXECUTE FUNCTION prevent_finance_creation_metadata_update();

CREATE TRIGGER categories_prevent_creation_metadata_update
    BEFORE UPDATE ON categories
    FOR EACH ROW EXECUTE FUNCTION prevent_finance_creation_metadata_update();

CREATE TRIGGER transactions_prevent_creation_metadata_update
    BEFORE UPDATE ON transactions
    FOR EACH ROW EXECUTE FUNCTION prevent_finance_creation_metadata_update();

-- +goose Down
DROP TRIGGER IF EXISTS transactions_prevent_creation_metadata_update ON transactions;
DROP TRIGGER IF EXISTS categories_prevent_creation_metadata_update ON categories;
DROP TRIGGER IF EXISTS accounts_prevent_creation_metadata_update ON accounts;
DROP FUNCTION IF EXISTS prevent_finance_creation_metadata_update();

DROP INDEX IF EXISTS uq_categories_household_creation_idempotency_key;
DROP INDEX IF EXISTS uq_accounts_household_creation_idempotency_key;

ALTER TABLE transactions
    DROP CONSTRAINT IF EXISTS transactions_idempotency_payload_hash_valid,
    DROP CONSTRAINT IF EXISTS transactions_version_positive,
    DROP COLUMN IF EXISTS idempotency_payload_hash,
    DROP COLUMN IF EXISTS version;

ALTER TABLE categories
    DROP CONSTRAINT IF EXISTS categories_creation_metadata_valid,
    DROP CONSTRAINT IF EXISTS categories_version_positive,
    DROP COLUMN IF EXISTS creation_payload_hash,
    DROP COLUMN IF EXISTS creation_idempotency_key,
    DROP COLUMN IF EXISTS version;

ALTER TABLE accounts
    DROP CONSTRAINT IF EXISTS accounts_creation_metadata_valid,
    DROP CONSTRAINT IF EXISTS accounts_version_positive,
    DROP COLUMN IF EXISTS creation_payload_hash,
    DROP COLUMN IF EXISTS creation_idempotency_key,
    DROP COLUMN IF EXISTS version;
