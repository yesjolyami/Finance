# Соответствие backup v5 → PostgreSQL v1

Документ описывает будущую миграцию данных, но не реализует importer. Любой прогон
должен выполняться сначала на временной PostgreSQL и в одной транзакции. Реальный
`finance.db` не является источником этапа 2: importer позднее будет принимать только
явно выбранный JSON backup v5.

## Общие правила преобразования

- importer получает заранее созданные `user_id` и `household_id`; backup v5 не
  содержит tenant или пользователя;
- строковые legacy ID преобразуются в детерминированные UUIDv5 из отдельного
  namespace, `household_id`, типа сущности и legacy ID;
- для каждой ссылки используется одна таблица соответствия в памяти; неизвестная
  ссылка останавливает импорт;
- все денежные значения остаются целыми копейками без округления;
- currency в v1 всегда `RUB`;
- пустые legacy dates становятся `NULL`, непустые проверяются как `DATE`;
- `createdAt` разбирается как `TIMESTAMPTZ`; `updated_at` получает то же значение;
- импортированные records получают `source = 'import'` там, где поле существует;
- повторный импорт использует стабильные UUID и transaction `idempotency_key`, а не
  создает дубликаты.

## Верхний уровень и accounts

| Legacy JSON field | Новая таблица/колонка | Перенос | Default | Потенциальный конфликт |
| --- | --- | --- | --- | --- |
| `version` | importer validation | Проверка значения `5`, не сохраняется | — | Другие версии требуют отдельного adapter |
| `exportedAt` | import metadata/audit | Преобразование в `TIMESTAMPTZ` для audit event | Время запуска только если поле отсутствует в legacy-формате | Неверная timezone останавливает импорт |
| `accounts[].id` | `accounts.id` | Детерминированный UUIDv5 | Нет | Одинаковый legacy ID другого типа получает другой UUID |
| — | `accounts.household_id` | Добавляется из import context | Обязателен | Нельзя импортировать без выбранного household |
| `accounts[].name` | `accounts.name` | Прямой | Нет | Пустое/слишком длинное значение отклоняется |
| `accounts[].color` | `accounts.color` | Прямой после нормализации HEX-регистра | `#6B7280` только для старого backup без цвета | Неверный `#RRGGBB` отклоняется |
| `accounts[].sortOrder` | `accounts.sort_order` | Прямой integer | `0` | Отрицательное значение отклоняется |
| `accounts[].system` | `accounts.is_system` | Прямой boolean | `false` | — |
| `accounts[].kind` | `accounts.account_type` | `regular`/`savings` напрямую | `regular` для старого backup | Иное значение отклоняется |
| `accounts[].bank` | `accounts.bank_label` | Прямой текст | `''` | Длина ограничена |
| `accounts[].owner` | `accounts.legacy_owner_label` | Прямой без потери исходной подписи | `''` | — |
| `accounts[].owner` | `accounts.owner_user_id` | Опциональное преобразование только при однозначном match с household member | `NULL` | Неоднозначное имя не связывается автоматически |
| — | `accounts.currency_code` | Добавляется | `RUB` | Другая валюта не поддерживается v1 |
| `accounts[].createdAt` | `accounts.created_at`, `updated_at` | ISO 8601 → `TIMESTAMPTZ` | Время импорта для legacy без поля | Невалидный timestamp отклоняется |

## Categories и budgets

| Legacy JSON field | Новая таблица/колонка | Перенос | Default | Потенциальный конфликт |
| --- | --- | --- | --- | --- |
| `categories[].id` | `categories.id` | Детерминированный UUIDv5 | Нет | — |
| — | `categories.household_id` | Из import context | Обязателен | Cross-household ссылки запрещены FK |
| `categories[].type` | `categories.category_type` | `income`/`expense` напрямую | Нет | Иное значение отклоняется |
| `categories[].name` | `categories.name` | Преобразование: `btrim` | Нет | Дубликат по ICU Unicode-сопоставлению без учета регистра и внешних пробелов требует rename/merge до записи |
| `categories[].color` | `categories.color` | Прямой HEX | `#6B7280` для старого backup | Неверный формат отклоняется |
| `categories[].system` | `categories.is_system` | Прямой boolean | `false` | — |
| — | `categories.sort_order` | Добавляется | Порядок массива или `0` | Одинаковый порядок допустим |
| `categories[].budgetCents` | `budgets.amount_cents` | Только если значение `> 0` | Нулевая сумма не создает budget | Legacy budget был бессрочным месячным лимитом |
| `exportedAt` + `budgetCents` | `budgets.budget_month` | Первый день месяца `exportedAt`, если пользователь не выбрал иной import month | Явно подтвержденный import month | Существующий active budget требует replace/skip policy |
| `categories[].createdAt` | `categories.created_at`, `updated_at` | Прямой timestamp | Время импорта | — |

Нельзя безусловно размножать legacy budget на все будущие месяцы: это создало бы
данные, которых не было в backup. Importer должен показать выбранный месяц и отчет
о созданных budgets.

## Transactions

| Legacy JSON field | Новая таблица/колонка | Перенос | Default | Потенциальный конфликт |
| --- | --- | --- | --- | --- |
| `transactions[].id` | `transactions.id` | Детерминированный UUIDv5 | Нет | — |
| — | `transactions.household_id` | Из import context | Обязателен | Все references проверяются составными FK |
| `transactions[].type` | `transactions.transaction_type` | Прямой | Нет | Только `income`, `expense`, `transfer` |
| `transactions[].accountId` | `transactions.account_id` | Через account ID map | Нет | Неизвестный account останавливает импорт |
| `transactions[].toAccountId` | `transactions.to_account_id` | Через account ID map; только transfer | `NULL` | Совпадение с source отклоняется |
| `transactions[].categoryId` | `transactions.category_id` | Через category ID map; `NULL` для transfer | Нет для income/expense | Тип category обязан совпасть с transaction |
| `transactions[].amountCents` | `transactions.amount_cents` | Прямой `BIGINT` | Нет | `<= 0` и выход за диапазон отклоняются |
| `transactions[].date` | `transactions.event_date` | Прямой `DATE` | Нет | Невалидная дата отклоняется |
| `transactions[].note` | `transactions.note` | Прямой | `''` | Длина ограничена 1000 символами |
| `transactions[].isBalanceAdjustment` | `transactions.is_balance_adjustment` | Прямой | `false` | Для transfer значение `true` запрещено |
| — | `transactions.source` | Добавляется | `import` | — |
| `transactions[].id` | `transactions.idempotency_key` | `backup-v5:<legacy-id>` | Нет | Повтор внутри household отклоняется unique index |
| `transactions[].createdAt` | `transactions.created_at`, `updated_at` | Прямой timestamp | Время импорта | — |
| — | soft-delete/audit metadata | Не создается для активной записи | `NULL`; отдельный `imported` audit event | — |

## Goals

| Legacy JSON field | Новая таблица/колонка | Перенос | Default | Потенциальный конфликт |
| --- | --- | --- | --- | --- |
| `goals[].id` | `goals.id` | Детерминированный UUIDv5 | Нет | — |
| — | `goals.household_id` | Из import context | Обязателен | — |
| `goals[].name` | `goals.name` | Прямой | Нет | Пустое имя отклоняется |
| `goals[].targetCents` | `goals.target_amount_cents` | Прямой `BIGINT` | Нет | Неположительная сумма отклоняется |
| `goals[].savedCents` | `goals.initial_saved_cents` | Прямой, без искусственной contribution | `0` | Значение может быть выше target и сохраняется |
| `goals[].targetDate` | `goals.target_date` | `''` → `NULL`, иначе `DATE` | `NULL` | Невалидная дата отклоняется |
| `goals[].color` | `goals.color` | Прямой HEX | `#5D704D` | Неверный формат отклоняется |
| `goals[].archived` | `goals.archived_at` | `true` → время импорта; `false` → `NULL` | `NULL` | Backup не хранит точное время архивации |
| `goals[].createdAt` | `goals.created_at`, `updated_at` | Прямой timestamp | Время импорта | — |
| — | `goal_contributions` | Не создаются из `savedCents` | Пусто | Contributions появятся только из явных будущих событий |

## Debts и debt payments

| Legacy JSON field | Новая таблица/колонка | Перенос | Default | Потенциальный конфликт |
| --- | --- | --- | --- | --- |
| `debts[].id` | `debts.id` | Детерминированный UUIDv5 | Нет | — |
| — | `debts.household_id` | Из import context | Обязателен | — |
| `debts[].person` | `debts.person_label` | Прямой текст | Нет | Пустая подпись отклоняется |
| `debts[].direction` | `debts.direction` | `owe_me`/`i_owe` напрямую | Нет | Иное значение отклоняется |
| `debts[].amountCents` | `debts.original_amount_cents` | Прямой, после вставки payments не меняется | Нет | Неположительная сумма отклоняется |
| `debts[].paidCents` | Производное значение | Не записывается | Сумма active payments | Несовпадение с payments — ошибка сверки |
| `debts[].leftCents` | Производное значение | Не записывается | `max(original - paid, 0)` в read model | Несовпадение — ошибка сверки |
| `debts[].dueDate` | `debts.due_date` | `''` → `NULL`, иначе `DATE` | `NULL` | — |
| `debts[].note` | `debts.note` | Прямой | `''` | — |
| `debts[].archived` | `debts.archived_at` | Boolean → timestamp/NULL | `NULL` | Точное legacy время неизвестно |
| `debts[].createdAt` | `debts.created_at`, `updated_at` | Прямой timestamp | Время импорта | — |
| `debtPayments[].id` | `debt_payments.id` | Детерминированный UUIDv5 | Нет | — |
| `debtPayments[].debtId` | `debt_payments.debt_id` | Через debt ID map | Нет | Cross-household/unknown debt отклоняется |
| `debtPayments[].amountCents` | `debt_payments.amount_cents` | Прямой `BIGINT` | Нет | Неположительная сумма отклоняется |
| `debtPayments[].date` | `debt_payments.event_date` | Прямой `DATE` | Нет | — |
| `debtPayments[].note` | `debt_payments.note` | Прямой | `''` | — |
| — | `debt_payments.source` | Добавляется | `import` | — |
| `debtPayments[].createdAt` | `debt_payments.created_at`, `updated_at` | Прямой timestamp | Время импорта | — |

## Поля без источника в backup v5

`users`, `households`, `household_members`, `recurring_transactions` и подробный
`audit_log` не имеют прямого legacy-источника. Контекст пользователя/household будет
создаваться отдельным авторизованным workflow будущего этапа; recurring templates
не выводятся эвристически из похожих transactions.

## Проверка итогового баланса

До фиксации import transaction рассчитываются два набора контрольных значений:

1. Для каждого account и cutoff date:
   `income - expense - outgoing transfers + incoming transfers`.
2. Для household: сумма account balances; каждый transfer обязан дать общий net `0`.
3. Balance adjustments включаются в account balance, но отдельно подсчитываются для
   проверки аналитики cash flow.
4. Сверяются количество и сумма income/expense/transfer по месяцам.
5. Для каждого debt сверяется `sum(debtPayments.amountCents)` с legacy `paidCents`,
   а вычисленный остаток — с `leftCents`.
6. Для goals сверяется `initial_saved_cents = savedCents`; contributions после
   импорта должны отсутствовать.

Любое несовпадение отменяет всю транзакцию импорта и формирует отчет без изменения
целевой базы.
