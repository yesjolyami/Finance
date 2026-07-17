\set ON_ERROR_STOP on
BEGIN;

INSERT INTO users (id, auth_subject, display_name)
VALUES ('ab000000-0000-4000-8000-000000000001', 'ab000000-0000-4000-8000-000000000001', 'Матвей');

DO $$
DECLARE
    profile users%ROWTYPE;
BEGIN
    SELECT * INTO profile FROM users WHERE id = 'ab000000-0000-4000-8000-000000000001';
    IF profile.usage_mode <> 'personal' OR profile.onboarding_completed OR profile.primary_currency_code <> 'RUB' THEN
        RAISE EXCEPTION 'unexpected onboarding defaults';
    END IF;
END;
$$;

DO $$
BEGIN
    BEGIN
        INSERT INTO users (id, auth_subject, display_name, usage_mode)
        VALUES ('ab000000-0000-4000-8000-000000000002', 'ab000000-0000-4000-8000-000000000002', 'Ошибка', 'invalid');
        RAISE EXCEPTION 'invalid usage mode was accepted';
    EXCEPTION WHEN check_violation THEN
        NULL;
    END;
END;
$$;

INSERT INTO households (id, name, created_by_user_id)
VALUES ('ab100000-0000-4000-8000-000000000001', 'Личные финансы', 'ab000000-0000-4000-8000-000000000001');

INSERT INTO household_members (id, household_id, user_id, role)
VALUES ('ab200000-0000-4000-8000-000000000001', 'ab100000-0000-4000-8000-000000000001', 'ab000000-0000-4000-8000-000000000001', 'owner');

INSERT INTO accounts (id, household_id, name, color, account_type)
VALUES ('ab300000-0000-4000-8000-000000000001', 'ab100000-0000-4000-8000-000000000001', 'Наличные', '#5F714D', 'cash');

INSERT INTO transactions (
    id, household_id, transaction_type, account_id, amount_cents,
    event_date, note, is_balance_adjustment, created_by_user_id
)
VALUES (
    'ab400000-0000-4000-8000-000000000001', 'ab100000-0000-4000-8000-000000000001',
    'income', 'ab300000-0000-4000-8000-000000000001', 150000,
    CURRENT_DATE, 'Начальный баланс', TRUE, 'ab000000-0000-4000-8000-000000000001'
);

DO $$
BEGIN
    BEGIN
        INSERT INTO transactions (
            id, household_id, transaction_type, account_id, amount_cents,
            event_date, is_balance_adjustment
        ) VALUES (
            'ab400000-0000-4000-8000-000000000002', 'ab100000-0000-4000-8000-000000000001',
            'income', 'ab300000-0000-4000-8000-000000000001', 100,
            CURRENT_DATE, FALSE
        );
        RAISE EXCEPTION 'ordinary transaction without category was accepted';
    EXCEPTION WHEN check_violation THEN
        NULL;
    END;
END;
$$;

ROLLBACK;
\echo 'Onboarding profile constraints: OK'
