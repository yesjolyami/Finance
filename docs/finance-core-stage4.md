# Этап 4A: backend финансового ядра

Этот документ фиксирует контракт первого финансового HTTP API. Этап 4A реализует
только accounts, categories, transactions и summary в существующем модульном
Go-приложении. React UI, backup importer, budgets, goals, debts, recurring,
банковские интеграции и production deployment не входят в этап.

## Trust boundary и tenant policy

Все маршруты находятся под существующим криптографически проверяющим Bearer
middleware. `sub` из JWT — единственный actor identity; `household_id`, user ID и
роль никогда не принимаются из JSON body. Repository связывает `users.auth_subject`,
active `household_members` и `household_id` в каждом SQL statement/transaction.

Чужой household, объект другого household, отсутствующий объект и объект household,
в котором actor не имеет active membership, всегда имеют одинаковую внешнюю
семантику `404 not_found`. Это закрывает IDOR и tenant enumeration. `403 forbidden`
возвращается только active участнику известного ему household, если его роль
недостаточна для операции. Composite tenant foreign keys из `00001` остаются
последней линией защиты cross-household ссылок.

## Матрица ролей

| Операция | owner | admin | member |
| --- | --- | --- | --- |
| Читать accounts/categories/transactions/summary | Да | Да | Да |
| Создавать, изменять, архивировать и восстанавливать account | Да | Да | Нет (`403`) |
| Создавать, изменять, архивировать и восстанавливать category | Да | Да | Нет (`403`) |
| Создавать, изменять, soft-delete и восстанавливать transaction | Да | Да | Да |

Removed membership и soft-deleted local user не дают никаких прав и получают
opaque `404`. Системные accounts/categories читаются обычно, но полностью
immutable для PATCH/archive/restore: такие попытки дают
`409 system_resource_immutable`. `isSystem` не принимается из body.

## Общие JSON-контракты

- UUID — canonical lowercase UUID string; malformed object/household UUID дает
  opaque `404`, а malformed UUID в allowlisted filter — `400 invalid_query`.
- Все суммы в копейках — JSON string, никогда float или JSON number.
  Положительная сумма запроса имеет regex `^[1-9][0-9]*$` и диапазон
  `1..9000000000000000`. Ответные signed balances используют canonical
  `^-?(0|[1-9][0-9]*)$`, значение `-0` запрещено.
- PostgreSQL `SUM(BIGINT)` возвращает `NUMERIC`. Repository читает его как decimal
  text, запрещает fraction/exponent и проверяет signed `int64` range до выдачи.
  Выход за range — fail-closed `500 internal_error`, без wrap/усечения.
- `LocalDate` — строго существующая календарная дата `YYYY-MM-DD`. Она хранится и
  возвращается как `DATE` без timezone conversion.
- Цвет — canonical `#RRGGBB`; строки trim-ятся, проверяются по Unicode rune length.
- JSON strict: `application/json`, максимум 64 KiB, неизвестные поля, несколько
  JSON values и отсутствующие обязательные поля отклоняются.
- Ошибки используют существующий envelope
  `{"error":{"code":"...","message":"..."}}`; DB details не выдаются.
- Синтаксическая ошибка body/query — `400`; корректный JSON с нарушенным domain
  contract — `422 validation_failed`; role — `403`; opaque object — `404`;
  idempotency/state/version/Unicode conflict — `409`; неизвестная ошибка — `500`.
- Все ответы `Cache-Control: no-store`, имеют request ID и безопасные заголовки.
- Accounts/categories/transactions имеют `version` — canonical decimal JSON
  **string** `"1".."9223372036854775807"`, без leading zero/exponent. Это исключает
  потерю точности JavaScript после `2^53-1`. Любой PATCH/archive/delete/restore требует ровно один
  `If-Match: "vN"`. Duplicate, weak/malformed/unquoted header — `400`; atomic
  version mismatch — `409 version_conflict`. Успешный entity response выставляет
  `ETag: "vN"`; mutation увеличивает version ровно на один. При максимальном DB
  BIGINT mutation fail-closed дает `409 version_exhausted`, не integer overflow.
- CORS allowlist дополнительно принимает `If-Match` и экспонирует браузеру
  `ETag, X-Request-ID, Idempotency-Replayed`; wildcard/credentials не появляются.

## Маршруты и schemas

Base path: `/api/v1/households/{householdId}/finance`.

Query string разрешен только на документированных finance GET list/summary routes.
Любой query на POST/PATCH, object action или прочем маршруте дает `400 invalid_query`.
Все account/category archive/restore и transaction restore POST требуют
`Content-Type: application/json` и строго один body `{}`; missing body, non-JSON,
unknown field или второй JSON value отклоняются. Transaction delete — единственное
state action с непустым body (`reason`).

В list filters syntactically valid, но nonexistent или foreign-household
`accountId/categoryId` дают одинаковый успешный пустой result, а не различимые
404/403. SQL не делает отдельный existence probe; tenant predicate естественно
возвращает zero rows. Это поведение покрывается IDOR regression tests.

### Accounts

| Method/path | Назначение |
| --- | --- |
| `GET /accounts?state=active&limit=100&cursor=...` | Полный bounded cursor list; `state=active|archived|all`, default `active`; `limit=1..200`, default `100`. |
| `POST /accounts` | Создать account; обязателен один `Idempotency-Key`. |
| `PATCH /accounts/{accountId}` | Strict partial update; обязателен `If-Match`. |
| `POST /accounts/{accountId}/archive` | Выставить `archived_at`; обязателен `If-Match`; повтор — `409`. |
| `POST /accounts/{accountId}/restore` | Очистить `archived_at`; обязателен `If-Match`; повтор — `409`. |

Create body:

```json
{
  "name": "Основной",
  "color": "#5F714D",
  "sortOrder": 10,
  "accountType": "regular",
  "bankLabel": "",
  "legacyOwnerLabel": "",
  "ownerUserId": null
}
```

`accountType` — `regular|savings`; `sortOrder` — `0..1000000`; labels максимум
120 rune. `ownerUserId`, если задан, обязан быть active member того же household.
PATCH принимает минимум одно и только allowlisted поле из create body. Отсутствие
поля означает «не менять»; explicit `null` допустим только для `ownerUserId`.
Unknown/empty PATCH отклоняется. Merge и validation выполняются под row lock;
idempotency metadata, `version`, `isSystem` и tenant поля PATCH-ом не меняются.
Response дополнительно содержит `id`, `currencyCode="RUB"`, `isSystem`,
`version`, `createdAt`, `updatedAt`, `archivedAt`. List order использует immutable
`created_at DESC,id DESC`. `cursor` — versioned base64url максимум 512 bytes с
этими keys и fingerprint `state`; duplicate/unknown/mismatched cursor query
отклоняется. Response: `{"accounts":[...],"nextCursor":"..."}`; последний cursor
null. Repository выбирает `limit+1`, поэтому полный список доступен страницами и
никогда не материализуется unbounded.

Архивирование account не требует нулевого баланса: summary сохраняет такой account
при ненулевом историческом balance и всегда включает его в household total.

### Categories

| Method/path | Назначение |
| --- | --- |
| `GET /categories?type=expense&state=active&limit=100&cursor=...` | `type=income|expense` обязателен; state/limit/cursor как у accounts. |
| `POST /categories` | Создать category; обязателен один `Idempotency-Key`. |
| `PATCH /categories/{categoryId}` | Strict partial name/color/sortOrder; `If-Match`; type неизменяем. |
| `POST /categories/{categoryId}/archive` | Архивировать с `If-Match`; replay состояния — `409`. |
| `POST /categories/{categoryId}/restore` | Восстановить с `If-Match`; replay состояния — `409`. |

Create body:

```json
{"type":"expense","name":"Продукты","color":"#A97A39","sortOrder":20}
```

PATCH содержит минимум одно из `name/color/sortOrder`; null, `type`, unknown и
empty PATCH запрещены. Merge выполняется под row lock. Response содержит `id`,
`type`, `name`, `color`, `sortOrder`, `isSystem`, `version`, timestamps.
Unicode/trim/case uniqueness остается DB-authoritative через ICU index и
отображается как deterministic `409`.
Category order — immutable `created_at DESC,id DESC`; cursor fingerprint включает
`type` и `state`. Response: `{"categories":[...],"nextCursor":"..."}`.

### Transactions

| Method/path | Назначение |
| --- | --- |
| `GET /transactions?...` | Stable cursor page; default только active. |
| `POST /transactions` | Создать income/expense/transfer; обязателен `Idempotency-Key`. |
| `PATCH /transactions/{transactionId}` | Strict partial update под row lock; обязателен `If-Match`. |
| `POST /transactions/{transactionId}/delete` | `If-Match`; soft-delete; body `{"reason":"..."}`, 1..500 rune. |
| `POST /transactions/{transactionId}/restore` | `If-Match`; restore; body `{}`. |

Create body:

```json
{
  "type": "expense",
  "accountId": "00000000-0000-4000-8000-000000000001",
  "toAccountId": null,
  "categoryId": "00000000-0000-4000-8000-000000000002",
  "amountCents": "12345",
  "eventDate": "2026-07-15",
  "note": "",
  "isBalanceAdjustment": false
}
```

PATCH принимает минимум одно allowlisted поле из create body. Nullable references
могут передавать explicit null; отсутствие означает «не менять». Repository
блокирует transaction, строит merged state, проверяет итоговую shape и выполняет
atomic `WHERE version=expected`. Если account/category reference не меняется,
исторический archived reference допустим; назначить новый archived/deleted
reference нельзя. Restore также допускает неизмененный archived reference, но не
deleted reference. Idempotency/fingerprint, source, actor, deletion metadata и
version не принимаются из body.

Income/expense требуют category соответствующего type и `toAccountId=null`;
transfer требует `categoryId=null`, отличный destination account и запрещает
balance adjustment. `source` для этого API всегда `manual`; actor metadata берется
из verified identity. Новые transactions используют только active non-deleted
references. История с archived references остается читаемой и корректно влияет на
баланс. Deleted transaction нельзя PATCH/delete повторно; active нельзя restore —
`409`.

Response содержит body fields, `id`, `source`, `createdByUserId`, timestamps,
`deletedAt`, `deletionReason`, `version`; `amountCents` остается string.

#### Pagination и filters

Разрешены ровно одиночные query parameters:

- `from`, `to`: optional inclusive LocalDate;
- `type=income|expense|transfer`;
- `accountId`: source или destination account;
- `categoryId`;
- `state=active|deleted|all`, default `active`;
- `limit`: default `50`, range `1..200`;
- `cursor`: максимум 512 bytes.

Unknown или duplicate parameter, пустое значение, malformed UUID/date/cursor —
`400 invalid_query`; `from>to` и range шире 366 дней — `422 validation_failed`.
Order фиксирован по immutable keys: `created_at DESC, id DESC`; `eventDate` — только
filter/response field и PATCH не перемещает строку между страницами. Cursor —
base64url versioned JSON с `createdAt/id` и SHA-256 fingerprint нормализованных
filters, включая `state`. Cursor от другого набора filters отклоняется. SQL keyset
predicate использует tuple `<`; выбирается `limit+1`. Existing rows не дублируются
при PATCH между страницами. Concurrent insert с более новым immutable key появится
только при новом обходе с первой страницы и не вклинивается в уже начатый обход.
Response: `{"transactions":[...],"nextCursor":"..."}`; последний cursor — null.

### Summary

Summary разделен на один fixed-size scalar response и два независимо paginated
полных списка. Это дает доказуемый response bound без top-N/потери данных.

| Method/path | Назначение |
| --- | --- |
| `GET /summary?from=2026-07-01&to=2026-07-31` | Полные scalar household total и cash-flow; только `from/to`. |
| `GET /summary/account-balances?to=2026-07-31&limit=100&cursor=...` | Полный paginated account balance list. |
| `GET /summary/expense-by-category?from=2026-07-01&to=2026-07-31&limit=100&cursor=...` | Полный paginated expense category list. |

`from/to` обязательны там, где показаны, одиночны, inclusive и не шире 366 дней.
List `limit` имеет default `100`, range `1..200`; cursor максимум 512 bytes.
Account balance order использует immutable account `created_at DESC,id DESC`, cursor
fingerprint включает `to`. Expense category order использует immutable category
`created_at DESC,id DESC`, fingerprint включает `from/to`. Cursors versioned
base64url; mismatch/duplicate/unknown query — `400`. Repository выбирает `limit+1`.

```json
{
  "from": "2026-07-01",
  "to": "2026-07-31",
  "householdTotalCents": "12345",
  "cashFlow": {"incomeCents":"50000","expenseCents":"37655"}
}
```

Account page:

```json
{
  "accountBalances": [
    {"accountId":"...","name":"Основной","archivedAt":null,"balanceCents":"12345"}
  ],
  "nextCursor": null
}
```

Category page аналогично возвращает
`{"expenseByCategory":[...],"nextCursor":null}`. Поэтому scalar response имеет
constant size, а каждый list response содержит максимум 200 элементов.

Balances считаются по всем non-deleted accounts и всем не удаленным transactions с
`event_date <= to`:

- income на source account: `+amount`;
- expense на source account: `-amount`;
- transfer: source `-amount`, destination `+amount`;
- balance adjustment участвует в balance;
- account pages полностью обходят все active accounts, а также archived account с
  ненулевым balance; archived account с нулем можно не включать;
- `householdTotalCents` — проверенная сумма балансов **всех non-deleted accounts**,
  включая archived. Archive/restore поэтому не меняет total. Внутренний transfer,
  в том числе между active и исторически archived account, меняет два account
  balance, но не household total.

Cash-flow и paginated expense-by-category считаются только внутри `[from,to]`, исключают
soft-deleted transactions, transfers и `is_balance_adjustment=true`. Income и
expense возвращаются положительными абсолютными totals. Categories в истории
могут быть archived; их name остается доступным через FK.

Integration tests фиксируют archive/restore ненулевого account, transfer с
исторически archived account и неизменность household total.

## Idempotency и replay

Create account/category/transaction требует ровно один `Idempotency-Key` длиной
1..255. Leading/trailing whitespace и пустое значение отклоняются, а не trim-ятся
молча. Scope — `(household_id, resource_type, key)`. Permission/active membership
проверяется **до** lookup key: inactive actor получает opaque `404`, active actor с
недостаточной mutation role — `403`, без раскрытия существования replay resource.
Другой active actor с достаточной mutation role и текущим read access получает
обычную same-payload replay semantics. Service строит
canonical normalized payload с явными null/default values и хранит SHA-256 bytes.
Миграция `00003` добавляет `version BIGINT NOT NULL DEFAULT 1` с positive CHECK,
creation key/fingerprint metadata для accounts/categories и fingerprint для
существующего transaction key. Paired key/hash CHECK явно требует non-null 32-byte
hash при non-null key; legacy NULL/NULL rows остаются валидными. Existing transaction
key без hash остается допустимым, но не replayable. Общий DB trigger запрещает
UPDATE creation key/hash после установки; normal entity mutations могут изменять
только version и business fields. Down удаляет trigger/function и только columns,
constraints/indexes 00003, сохраняя used financial rows.

- первый create: `201`, resource и один `audit_log(created)`;
- тот же key + тот же fingerprint: `200`, текущая representation того же resource,
  текущие `version/ETag`, `Idempotency-Replayed: true`, без нового audit event;
- тот же key + другой fingerprint либо legacy transaction key без fingerprint:
  deterministic `409 idempotency_conflict`;
- конкурентные одинаковые create сериализуются unique index/row lookup и создают
  ровно один resource и audit event.

Archive/delete/update не переиспользуют create key. Replay может вернуть уже
измененную/архивированную/soft-deleted текущую representation, но всегда тот же ID.

## Transaction и audit guarantees

Каждая mutation использует одну PostgreSQL transaction. В ней repository:

1. связывает actor `auth_subject` с non-deleted user и active membership;
2. проверяет роль и tenant в том же SQL/transaction;
3. блокирует mutation target (`FOR UPDATE`) и referenced rows, где состояние может
   измениться конкурентно;
4. сверяет expected version, изменяет entity и увеличивает version в том же
   statement; stale version дает `409 version_conflict` без audit;
5. вставляет append-only audit event с правильными `household_id`, actor local UUID,
   entity/action и request ID;
6. commit; ошибка audit приводит к rollback entity mutation.

Audit `changes` хранит только безопасные имена измененных полей/источник `api`, без
JWT, Authorization, request body, notes, deletion reason, account/category names
или иных потенциальных PII. Idempotent replay не пишет второй audit event.

Каждый finance DB call наследует request context. Transport/service добавляет
bounded deadline: максимум 3 секунды для read и 5 секунд для mutation, не продлевая
более короткий upstream deadline. `BeginTx`, Query/Exec/Scan используют этот context;
timeout/cancel приводит к rollback и безопасной внешней ошибке.

`safeLogPath` распознает каждый finance route и пишет только шаблоны вида
`/api/v1/households/{id}/finance/transactions/{id}`, никогда реальные household/
object IDs или query. Transaction, account, category и summary pagination cursors
никогда не логируются. Internal error logging получает фактический request context и
request ID; использование `context.Background()` в error path удаляется. SQL text,
args, body, filters и DB error details в application log не попадают.

## Обязательные regression tests

- version starts at 1; PATCH/archive/delete/restore increment; stale/concurrent
  `If-Match` оставляет одну успешную mutation и один audit event; JSON version
  string parsing отклоняет number, zero, leading zero и значение выше BIGINT;
- malformed/duplicate `If-Match`, ETag/replay/exposed CORS headers;
- system account/category mutation возвращает immutable conflict;
- account с ненулевым balance после archive остается в summary; restore не меняет
  total; historical transfer active↔archived сохраняет household total;
- `state=active|deleted|all` и filter fingerprint; insert/PATCH eventDate между
  страницами не создают duplicate, новый insert виден при новом обходе;
- accounts/categories полностью обходятся при количестве больше limit; insert,
  PATCH и archive между страницами не дают duplicate; mismatched cursor и duplicate
  query отклоняются;
- scalar summary имеет fixed size; account/category summary pages полностью
  обходятся и каждая содержит не более limit; независимые cursor fingerprints
  проверяются;
- valid nonexistent/foreign account/category filter одинаково возвращает empty;
  non-GET query, missing/non-JSON/non-empty archive/restore body отклоняются;
- unchanged archived transaction refs допустимы для PATCH/restore, назначение
  нового archived/deleted ref отклоняется;
- key+NULL hash и UPDATE установленного key/hash отклоняются БД; NULL/NULL legacy
  rows допустимы; used-data Down/Up сохраняет financial rows и business version
  удаляется/возвращается только как объект миграции;
- request timeout/cancel rollback, audit same transaction, real request ID в audit
  и internal log, safe route templates без ID/query/cursors.

## Намеренно отложено

- React financial UI (этап 4B);
- backup v5 importer и любое чтение/изменение `finance.db`;
- budgets, goals, contributions, debts, payments и recurring generator/API;
- invitations/members UI и новые auth flows;
- RLS, мультивалютность, reconciliation и банковские интеграции;
- snapshot transactions и bulk operations (immutable-key cursor остается в 4A);
- production Supabase credentials, email, Docker/deploy, TLS/proxy, CSP/rate limit.
