# Этап 5A: безопасный импорт backup v5

Этот документ фиксирует design gate будущего импортера. На этапе 5A importer,
HTTP-маршруты, миграции и UI **не реализуются**. Источник данных — только явно
выбранный пользователем JSON-файл формата v5. `finance.db` не читается, не
копируется и не используется.

Базовые источники контракта:

- [`backup-v5.md`](backup-v5.md) — wire format существующего экспорта;
- [`backup-v5-to-postgres.md`](backup-v5-to-postgres.md) — принятый mapping в
  PostgreSQL;
- [`database-schema.md`](database-schema.md) — tenant constraints и финансовые
  инварианты.

## Границы первой версии

Импорт доступен только активному `owner` выбранного household. Роль проверяется по
verified JWT subject и tenant-bound SQL; `household_id` и actor никогда не берутся
из backup или request body.

Safest v1 policy:

1. Импорт разрешен только в полностью пустой household.
2. Importer никогда не делает replace, merge, upsert поверх пользовательских
   финансовых данных и не удаляет их.
3. Preview не пишет финансовые записи.
4. Confirm вставляет весь набор одной PostgreSQL-транзакцией или не вставляет
   ничего.
5. Повтор успешного confirm возможен только как idempotent replay сохраненного
   результата, без повторной вставки.

Пустой household не содержит ни одной строки этого `household_id` в `accounts`,
`categories`, `transactions`, `budgets`, `goals`, `goal_contributions`, `debts`,
`debt_payments` и `recurring_transactions`, включая archived/soft-deleted строки.
Наличие `household`, membership и audit-записей не делает его непустым. Проверка
выполняется и в preview, и повторно под блокировкой household row в confirm.

## Trust boundary и поток данных

```mermaid
sequenceDiagram
    participant B as Browser
    participant A as Go API
    participant P as PostgreSQL

    B->>A: POST preview + JWT + JSON v5 + budget month
    A->>P: owner/empty checks, read-only normalization helpers
    A->>A: bounded parse, mapping, totals, SHA-256
    A->>P: store token hash + digest + binding only
    A-->>B: counts, totals, warnings, raw one-time token
    B->>A: POST confirm + same JSON + token + Idempotency-Key
    A->>A: parse again and compare raw-byte digest
    A->>P: lock household; completed-run lookup; then empty/token for new run
    A->>P: insert all rows, verify totals, append audit
    A->>P: commit
    A-->>B: import result or idempotent replay
```

React передает Supabase access token только в `Authorization`. Backup идет только
через Go API; browser не обращается к PostgreSQL/PostgREST напрямую. Supabase
service-role key, DB credentials и signing secrets не попадают в browser.

## Строгий JSON v5

### Transport

- только `POST` и `Content-Type: application/json`; параметр `charset` либо
  отсутствует, либо в точности означает UTF-8;
- gzip, zip, multipart, URL, server-side file path и remote fetch не принимаются;
- `Content-Encoding` не поддерживается: это исключает compressed/zip bomb class;
- `Content-Length` больше лимита отклоняется до чтения, chunked body ограничивается
  тем же counted reader;
- bytes обязаны быть valid UTF-8; UTF-8 BOM отклоняется;
- после единственного root object допускается только JSON whitespace. Второй JSON
  value и любые иные trailing bytes отклоняются.

### Schema policy

Importer принимает только canonical backup v5:

- root — object;
- `version` обязателен и является JSON integer ровно `5`, не строкой и не `5.0`;
- обязательны ровно `exportedAt`, `accounts`, `categories`, `transactions`,
  `goals`, `debts`, `debtPayments`;
- все шесть entity fields — массивы, в том числе когда они пусты;
- fallback старых backup без accounts/goals/debts на этом endpoint запрещен;
- duplicate keys отклоняются на любом уровне, включая одинаковые неизвестные keys;
- unknown fields отклоняются на root и во всех entity objects;
- каждое обязательное entity field присутствует ровно один раз и имеет точный JSON
  type из `backup-v5.md`;
- JSON monetary values и `sortOrder` разбираются через lexical integer/`big.Int`,
  без `float64`, exponent и fractional notation.

Это намеренно строже legacy Python import. Другой version или старый неполный
формат потребует отдельного явно названного adapter и нового design review.

### Hard limits

Defaults первой версии являются hard caps, а не подсказками:

| Ограничение | Default |
| --- | ---: |
| Raw request body | 32 MiB |
| JSON nesting depth | 12 |
| Любая строка до field validation | 4 KiB UTF-8 |
| Legacy ID | 1..255 UTF-8 bytes, без outer whitespace/C0 controls |
| Accounts | 10 000 |
| Categories | 20 000 |
| Transactions | 200 000 |
| Goals | 20 000 |
| Debts | 20 000 |
| Debt payments | 200 000 |
| Сумма всех entity objects | 300 000 |

Более раннее достижение любого лимита прекращает decode. Реализация использует
streaming token decoder для depth/duplicate/count checks и bounded in-memory
normalized model. Временные plaintext-файлы, OS temp, object storage и очереди с
raw backup запрещены. Дополнительный memory budget процесса должен fail closed до
OOM; увеличение caps возможно только после load test.

Field limits после NFC normalization соответствуют PostgreSQL/service contract:
names/labels — по правилам соответствующей таблицы, transaction note — до 1000
Unicode code points, payment/debt notes — по schema, color — `#RRGGBB`. Opaque
legacy ID, token, digest, request ID и `Idempotency-Key` Unicode-normalization не
подвергаются.

## Двухфазный preview → confirm

### Preview

`POST /api/v1/households/{householdId}/imports/backup-v5/preview`

Headers:

- `Authorization: Bearer ...`;
- `Content-Type: application/json`;
- `Import-Budget-Month: YYYY-MM-01` — обязателен всегда, должен быть первым днем
  календарного месяца.

Client-supplied `X-Request-ID` не принимается. Существующий middleware генерирует
request ID на сервере и передает его только через request context/response header.

Body — raw JSON backup v5, не wrapper. API вычисляет SHA-256 **точных raw bytes**.
Семантически равный JSON с другими whitespace/key order требует нового preview.

Успешный preview возвращает `200`:

```json
{
  "backupDigest": "sha256:0123456789abcdef...",
  "expiresAt": "2026-07-15T12:10:00Z",
  "confirmationToken": "<returned once>",
  "budgetMonth": "2026-07-01",
  "counts": {
    "accounts": 2,
    "categories": 10,
    "transactions": 125,
    "budgets": 4,
    "goals": 2,
    "goalContributions": 0,
    "debts": 1,
    "debtPayments": 3
  },
  "totals": {
    "incomeCents": "100000",
    "expenseCents": "72500",
    "transferCents": "15000",
    "householdBalanceCents": "27500"
  },
  "warnings": []
}
```

Money в response — canonical decimal strings. Preview не возвращает notes, names,
legacy IDs, token hashes или per-person debt content.

Raw confirmation token содержит не менее 256 random bits из `crypto/rand`,
base64url без padding. В БД хранится только fixed 32-byte SHA-256 token hash,
`actor_user_id`, `household_id`, raw-byte backup digest, budget month, policy
version, `expires_at` и timestamps. TTL default — 10 минут, hard max
15 минут. Token возвращается один раз и запрещен в URL/query/log/audit.

Preview metadata — единственная запись dry-run; финансовые таблицы и audit_log не
меняются. Истекшие/использованные preview metadata удаляются фоновым housekeeping
без raw content.

Authorization, authoritative empty check и token metadata insert выполняются одной
короткой control transaction. Она блокирует household row `FOR KEY SHARE`, затем
проверяет non-deleted actor + active owner membership, пустоту всех financial tables
и вставляет preview metadata. `FOR KEY SHARE` совместим с обычными finance
mutations, поэтому preview не обещает, что household останется пустым после ответа;
confirm всегда повторяет empty check под более сильной блокировкой.

### Confirm

`POST /api/v1/households/{householdId}/imports/backup-v5/confirm`

Headers:

- те же Authorization, Content-Type и `Import-Budget-Month`;
- `Import-Preview-Token: <raw token>`;
- ровно один `Idempotency-Key`, 1..255 bytes, без leading/trailing whitespace.

Body — точные bytes исходного JSON. API заново выполняет полный bounded parse,
normalization, reference validation и totals. Digest, actor, household, budget
month и importer policy version должны совпасть с preview binding.

Внешняя проверка token возвращает одну безопасную семантику
`preview_token_invalid`; клиент не узнает, был ли token неверным, чужим,
просроченным, отозванным или уже использованным.

Первый successful confirm возвращает `201`, replay — `200` с
`Idempotency-Replayed: true`. JSON обоих ответов имеет одинаковую bounded schema:

```json
{
  "importRunId": "00000000-0000-4000-8000-000000000001",
  "status": "completed",
  "policyVersion": "backup-v5-import/1",
  "completedAt": "2026-07-15T12:05:00Z",
  "counts": {
    "accounts": 2,
    "categories": 10,
    "transactions": 125,
    "budgets": 4,
    "goals": 2,
    "goalContributions": 0,
    "debts": 1,
    "debtPayments": 3
  },
  "warningCounts": {
    "legacyOwnerNotLinked": 1,
    "archiveTimeApproximated": 0,
    "goalExceedsTarget": 0,
    "debtOverpaid": 0,
    "systemResourcePreserved": 2,
    "budgetMonthExplicitChoice": 1
  }
}
```

Все count fields — JSON integers в пределах общих caps. Response не содержит
household/entity/legacy IDs кроме opaque `importRunId`, names, notes, amounts,
monetary totals, backup digest, budget month, token или idempotency key.

## Token, concurrency и idempotency

Для реализации потребуются отдельные control-plane таблицы preview/import run;
точная reversible migration проектируется на implementation stage. Они не хранят
raw backup или финансовые строки.

До открытия DB transaction confirm всегда выполняет bounded reparse, normalization,
reference/totals validation и raw-byte SHA-256. Затем вычисляются два opaque
HMAC-SHA-256 значения под dedicated import keyring из production secret store:

- key lookup HMAC от exact `Idempotency-Key`;
- request fingerprint HMAC от length-prefixed `(raw digest, budget month,
  policy version)`.

Completed run не хранит backup digest, raw key или token. HMAC нужен только для
same-key/same-payload replay и не возвращается клиенту/в logs.

Production config будущей реализации:

- `IMPORT_HMAC_ACTIVE_KEY_ID` — stable non-secret identifier по regex
  `[A-Za-z0-9._-]{1,64}`;
- `IMPORT_HMAC_KEYRING_FILE` — путь к read-only secret-store mount с mapping
  `key_id → base64url raw key`; каждый raw key содержит минимум 256 random bits;
- raw keys отсутствуют в repository, `.env.example`, database, health, errors и
  logs. Config/log выводит максимум key ID.

Importer-enabled startup fail closed, если active ID отсутствует, keyring/key
поврежден, active key короче 256 bits, database недоступна для key audit или любой
`hmac_key_id`, на который все еще ссылается completed run, отсутствует в keyring.
Новые runs используют только active key ID. Для replay API вычисляет key-lookup
HMAC-кандидат каждым retained key, находит row по его stored `hmac_key_id` и затем
вычисляет/сравнивает request fingerprint именно ключом этого row.

Controlled rotation двухфазна: сначала весь fleet получает old+new keyring при
старом active ID, затем весь fleet переключается на новый active ID. Удалять old
key разрешено только после отдельной retention/migration policy, которая доказала
отсутствие ссылок. При default indefinite retention completed runs все referenced
old keys также сохраняются бессрочно. Смешанный fleet, где экземпляр не знает
still-referenced key ID, не допускается readiness/startup gate.

Exact confirm ordering внутри одной PostgreSQL transaction:

1. household row `FOR UPDATE` вместе с non-deleted actor + active owner predicate;
2. lookup import run по candidate tuples
   `(household_id, actor_user_id, hmac_key_id, idempotency_key_hmac)`;
3. если найден `completed` run:
   - fingerprint совпадает — commit/close read path и вернуть сохраненный `200`
     replay **до** empty-household, token consumed или token expiry checks;
   - fingerprint отличается — `409 idempotency_conflict`, также до token checks;
4. существующий non-completed/corrupt state, который невозможен при штатной
   atomic transaction, дает fail-closed `409 import_state_conflict`;
5. только если completed run отсутствует — authoritative empty-household recheck;
6. lookup preview по token hash `FOR UPDATE`, затем exact binding/TTL/not-consumed
   validation;
7. insert нового import run reservation с unique idempotency scope;
8. deterministic financial inserts, reconciliation, safe audit, completed result
   update и удаление всех preview metadata этого household;
9. commit; только после него вернуть `201`.

Household `FOR UPDATE` сериализует same-household confirm до idempotency lookup.
Поэтому второй concurrent request того же key сначала ждет первый: после его commit
он видит completed row на шаге 2 и возвращает replay, не проверяя уже consumed token
и уже non-empty household. Если первый rollback-нулся, completed row отсутствует,
preview metadata не удалена, и второй продолжает шаги 5–9. Reservation,
financial data и completed result никогда не видны частично, поскольку создаются и
завершаются в одной transaction.

Разные valid preview tokens одного household также сериализуются household lock:
максимум один импортирует, остальные получают `409 household_not_empty`. После
успешного commit used и все outstanding preview metadata household (token hashes и
short-lived backup digests) удаляются в той же transaction; replay опирается только
на completed run HMAC/result.

Обычные finance mutations берут household row `FOR KEY SHARE`; confirm
`FOR UPDATE` с ними конфликтует. Если finance create получил lock и записал данные
первым, confirm дождется его commit, увидит non-empty household на шаге 5 и
откатится. Если confirm получил `FOR UPDATE` первым, finance mutation ждет полного
import commit/rollback; importer не смешивается с ней в своей transaction.

Persisted completed import run содержит ровно:

- `import_run_id` UUID;
- tenant-bound `household_id` и `actor_user_id` для authorization/FK, но они не
  возвращаются в response;
- non-secret stable `hmac_key_id`;
- fixed 32-byte `idempotency_key_hmac` и `request_fingerprint_hmac`;
- `status` с единственным committed значением `completed`;
- fixed `policy_version`;
- восемь non-negative bounded entity counts;
- шесть fixed warning counts из allowlist;
- `completed_at` и system `created_at`.

В completed run запрещены monetary totals/amounts, names, notes, financial/legacy
entity IDs, raw idempotency key, backup digest, budget month, preview token/hash и
raw/normalized backup. Persisted fields полностью воспроизводят minimal response
schema выше; HMAC fields используются только для lookup/compare.

Ошибка/cancellation до commit откатывает удаление preview metadata, import run,
audit и все financial inserts и позволяет retry тем же token/key до expiry. Потеря
HTTP-response после commit восстанавливается exact ordering replay path.

## Детерминированные идентификаторы

Фиксированный public UUIDv5 namespace первой версии:

`8a7193b0-05ae-4b3f-b50d-3065d04c6843`

UUID name bytes:

```text
finance-backup-v5 NUL canonical-household-uuid NUL entity-type NUL exact-legacy-id
```

Entity type — одно из `account`, `category`, `budget`, `transaction`, `goal`,
`debt`, `debt-payment`. C0 controls, включая NUL, в legacy ID запрещены. Одинаковый
legacy ID разных типов получает разные UUID; тот же backup в другом household также
получает другие UUID. Алгоритм и namespace после выпуска importer не меняются.

Canonical UUIDv5 fixtures (обязательны как отдельные unit tests):

| Entity | Household | Legacy name component | Expected UUID |
| --- | --- | --- | --- |
| account | `11111111-1111-4111-8111-111111111111` | `main` | `b48f4451-9067-5540-b3ec-fd8eeeb9e19c` |
| budget | `11111111-1111-4111-8111-111111111111` | `food` + NUL + `2026-07-01` | `caff427e-f755-56cf-897b-353379622370` |

Budget fixture использует полные name bytes
`finance-backup-v5` + NUL + household + NUL + `budget` + NUL + category legacy ID
+ NUL + budget month. Это внутренний composite component; запрет NUL во входном
legacy ID сохраняется.

ID обязаны быть уникальны внутри соответствующего массива. References сравниваются
с exact legacy ID до UUID conversion. Case folding/trim legacy ID запрещены.
Unknown, ambiguous и cross-type reference останавливает preview.

Transaction `idempotency_key` получает bounded значение
`backup-v5:<deterministic-transaction-uuid>` и 32-byte fingerprint canonical
normalized imported transaction. Accounts/categories creation idempotency columns
остаются `NULL`: atomic import-run и deterministic primary keys обеспечивают их
повтор. Importer не backfill-ит legacy idempotency metadata через UPDATE.

## Полный mapping

### Accounts

- `id` → deterministic account UUID;
- `household_id` → route context;
- `name` → NFC + trim, затем field/rune validation;
- `color` → uppercase `#RRGGBB`;
- `sortOrder`, `system`, `kind`, `bank`, `owner` → `sort_order`, `is_system`,
  `account_type`, `bank_label`, `legacy_owner_label`;
- `owner_user_id` всегда `NULL` в importer v1. Автоматическое сопоставление текста
  owner с member запрещено как неоднозначное и PII-sensitive;
- currency всегда `RUB`;
- `createdAt` → `created_at=updated_at` после strict timestamp parse;
- `version=1`, archive/delete отсутствуют.

### Categories и budgets

- category ID/household/type/name/color/system переносятся по принятому mapping;
- `sort_order` — zero-based position category в исходном массиве;
- DB ICU normalization function используется read-only в preview для поиска
  Unicode/case/trim duplicates; PostgreSQL unique constraint остается authority;
- `budgetCents=0` не создает budget;
- `budgetCents>0` разрешен только expense category и создает одну active budget на
  `Import-Budget-Month`;
- budget ID — UUIDv5 с entity type `budget` и name bytes category legacy ID + NUL +
  budget month; amount остается integer cents;
- budget timestamps — confirm transaction time UTC.

Budget month никогда не выводится молча из server timezone. UI обязан показать
explicit month; header содержит strict LocalDate первого дня. Suggested UI default
может быть месяцем `exportedAt` в его собственном offset, но пользователь явно
подтверждает его до preview.

### Transactions

- type/account/toAccount/category shape проверяется до SQL;
- income/expense требуют category того же типа и `toAccountId=null`;
- transfer требует два разных account, `categoryId=null` и
  `isBalanceAdjustment=false`;
- `amountCents` — integer `1..9_000_000_000_000_000`;
- `date` — strict Gregorian LocalDate `YYYY-MM-DD`, без timezone conversion;
- note — NFC + trim policy service contract, до 1000 code points;
- `source='import'`, actor берется из auth context;
- deterministic timestamps, ID, idempotency key/fingerprint как описано выше;
- все записи active, `version=1`, deletion metadata `NULL`.

### Goals и contributions

- goal ID/name/target/saved/targetDate/color/archived/createdAt переносятся согласно
  принятому mapping;
- `targetCents` строго положителен, `savedCents` неотрицателен; saved выше target
  допустим с warning;
- пустой `targetDate` → `NULL`;
- `archived=true` → `archived_at=confirm_time`, потому что точного legacy времени
  нет; это отражается warning code;
- `savedCents` → `initial_saved_cents`;
- `goal_contributions` не создаются: preview count обязан быть `0`, а post-import
  reconciliation подтверждает их отсутствие.

### Debts и payments

- debt ID/person/direction/original amount/due date/note/archive/createdAt
  переносятся согласно mapping;
- `paidCents` и `leftCents` не записываются;
- payment ID и debt reference детерминированы; amount/date/note переносятся,
  `source='import'`;
- для каждого debt importer считает `paid=sum(active payments)` и
  `left=max(original-paid,0)` и требует exact совпадения с legacy `paidCents` и
  `leftCents`;
- overpayment допустим только когда legacy derived fields согласованы; preview
  добавляет warning;
- unknown debt reference, duplicate payment ID или aggregate overflow — error.

### Поля без источника

Importer не создает users, households, memberships и recurring templates. Он не
генерирует contributions из goal saved amount, recurring rules из похожих
transactions или account-member links из legacy owner label.

## Деньги, даты и контрольные суммы

Ни один этап не использует IEEE-754/JSON number conversion. Входные integer tokens
разбираются через arbitrary precision, проверяются против field domain и только
потом приводятся к `int64`. Individual financial amount ограничен
`9_000_000_000_000_000` cents.

Preview использует checked arbitrary-precision accumulators и отклоняет dataset,
если любой account balance, household total, monthly aggregate, debt payment sum
или промежуточное signed значение не помещается в PostgreSQL signed `BIGINT` либо
нарушает domain constraint. Это не позволяет импортировать набор, после которого
обычный summary будет постоянно fail closed.

До записи рассчитываются:

1. balance каждого account: income − expense − outgoing transfer + incoming
   transfer, включая balance adjustments;
2. household balance как сумма account balances; net каждого transfer равен нулю;
3. income/expense/transfer counts и sums по календарному месяцу;
4. cash-flow без transfers и balance adjustments;
5. budget count/sum выбранного месяца;
6. goal `initial_saved_cents` и нулевое число contributions;
7. debt paid/left reconciliation.

Внутри confirm transaction после вставки те же values считаются SQL `NUMERIC`,
сканируются как decimal text, проверяются без float и сравниваются exact с preview
model. Сверяются также counts и deterministic IDs. Любое расхождение вызывает
rollback. Import-run хранит только bounded counts, aggregate checksum/result и
SHA-256 digest, не names/notes/legacy IDs.

`exportedAt` и каждый `createdAt` обязаны быть strict RFC3339 timestamp с explicit
`Z` или numeric offset и преобразуются в UTC `TIMESTAMPTZ`. Naive local timestamps,
DST guesses и server timezone запрещены. Financial dates и budget month остаются
LocalDate и никогда не проходят timezone conversion.

## Warnings и ошибки preview

Warnings имеют stable code и count, не содержат пользовательский текст. Начальный
allowlist:

- `legacy_owner_not_linked`;
- `archive_time_approximated`;
- `goal_exceeds_target`;
- `debt_overpaid`;
- `system_resource_preserved`;
- `budget_month_explicit_choice`.

Broken references, duplicate IDs/names, derived debt mismatch, transaction shape,
invalid dates/colors/types, amount/aggregate overflow и DB constraint prediction —
errors, а не warnings. Preview с errors не выпускает confirmation token.

## Commit и audit

Все inserts выполняются explicit SQL/pgx без ORM в одном modular-monolith API.
Mutation deadline default 60 секунд с hard max 120 секунд; более короткий caller
deadline не продлевается. Client cancellation, shutdown или DB error вызывают
rollback. Никакая background goroutine не продолжает импорт после отмены request.

После успешной reconciliation добавляется одна aggregate append-only audit entry:

- household и actor — verified context;
- используются уже разрешенные schema values: `entity_type='households'` и
  `action='imported'`; новый audit enum/CHECK value не добавляется;
- `entity_id` в точности равен route household UUID и совместим с существующим
  audit CHECK;
- только server-generated request ID из context, importer version и безопасные
  counts;
- без raw token, backup digest, legacy IDs, names, notes, amounts, body, filename,
  JWT или claims.

Per-row audit не создается: он раздувает audit_log и повышает риск утечки backup
content. Import-run metadata и одна aggregate `households/imported` audit entry
доказывают атомарный факт импорта. Control-plane migration не изменяет audit CHECK,
поэтому ее Down не требует удаления этой history row.

## HTTP statuses и error envelope

Используется существующий safe envelope `{ "error": { "code", "message" } }` без
parser/SQL/internal details.

| Status | Семантика |
| --- | --- |
| `200` | Успешный preview или idempotent confirm replay. |
| `201` | Первый successful confirm. |
| `400` | Header/content type/JSON syntax/duplicate/unknown/trailing/schema shape. |
| `401` | Missing/invalid/expired JWT. |
| `403` | Active member не owner. |
| `404` | Foreign/nonexistent household; одинаковая opaque semantics. |
| `409` | Household non-empty, idempotency conflict или importer state conflict. |
| `410` | Generic invalid/expired/consumed/wrong-bound preview token. |
| `413` | Raw body, string, depth или entity count limit. |
| `422` | Reference/domain/integrity/totals/overflow validation. |
| `429` | Import rate limit. |
| `500` | Safe internal failure; transaction rolled back. |
| `503` | Database unavailable before transaction. |

Malformed path UUID остается opaque `404`. Query parameters запрещены на обоих
routes. Missing/duplicate required headers — `400`. Confirm никогда не принимает
token из URL/query/cookie.

## CORS, rate limits и logging

Existing exact origin allowlist сохраняется. Preflight идет до auth, но разрешает
только configured origins, методы `POST, OPTIONS` и headers:

- `Authorization`;
- `Content-Type`;
- `Idempotency-Key`;
- `Import-Preview-Token`;
- `Import-Budget-Month`.

Expose: `X-Request-ID`, `Idempotency-Replayed`. `Vary: Origin` обязателен; wildcard
и credentials запрещены. `X-Request-ID` является только response header: browser не
может подставить его в request, importer использует server-generated context value.

Safest default process-local limits: не более 5 preview и 3 first-confirm attempts
на `(actor, household)` за 15 минут, плюс максимум один active parse/import на actor
и один confirm на household. Distributed/global rate limiting остается production
dependency, но отсутствие внешнего limiter не отключает local bounds.

`safeLogPath` пишет только шаблоны preview/confirm. Запрещено логировать:

- raw/normalized body и отдельные JSON fields;
- filename, notes, names, legacy IDs и monetary totals;
- Authorization, preview token, idempotency key и backup digest;
- budget month, query, parser fragment и SQL args.

Допустимы method, safe route template, status, latency, bounded byte/count buckets и
request ID. Error log получает реальный request context, но только stable internal
error category.

## Threat model

| Угроза | Мера |
| --- | --- |
| Stolen JWT | Криптографическая JWT verification, owner-only SQL, short auth lifetime. |
| IDOR/tenant enumeration | Actor+household predicate в SQL, foreign/missing = одинаковый 404. |
| Malformed JSON/parser abuse | UTF-8, depth/body/string/count limits, duplicate/unknown rejection. |
| Zip/decompression bomb | Только uncompressed JSON; Content-Encoding/archives rejected. |
| Token theft/replay | 256-bit token, DB hash only, actor/household/digest/month binding, short TTL, single-use. |
| Double/partial import | Household lock, empty recheck, one DB transaction, persistent idempotency result. |
| Confused deputy | Tenant/actor только из verified context; no IDs from body. |
| Backup leakage | TLS, no logs/temp/object storage, no response echo, bounded in-memory lifetime. |
| Resource exhaustion | Hard caps, deadlines, concurrency/rate gates, cancellation rollback. |
| Unicode collision | NFC field normalization plus DB ICU duplicate check/constraint. |
| Numeric corruption | Lexical integer + big.Int/NUMERIC text; no float; exact reconciliation. |

Backup содержит sensitive plaintext financial data. Production route нельзя
включать без TLS на trusted reverse proxy. Proxy обязан передавать exact request
body и `Content-Encoding` без прозрачной request decompression/recompression;
compressed request должен дойти до Go и быть отклонен. Proxy дополнительно ставит
raw-body limit не выше API cap. Browser должен читать файл только после explicit
user selection и не сохранять его content в localStorage, IndexedDB, analytics,
crash reports или service-worker cache. Go не создает temp plaintext files. Полное
гарантированное zeroization managed Go memory невозможно; buffers освобождаются
сразу после response/transaction и не сохраняются между фазами.

## Rollback и recovery

- До commit rollback автоматический и полный.
- После commit автоматического «undo import» endpoint в v1 нет: он конфликтовал бы
  с последующими пользовательскими изменениями и audit history.
- Если response потерян, клиент повторяет exact file/token/key и получает replay.
- Если preview истек до commit, пользователь запускает новый preview.
- Если confirm завершился ошибкой, тот же token/key можно повторить до expiry,
  поскольку preview metadata не была удалена.
- Если успешный импорт оказался выбран не в тот household, safest recovery — не
  изменять импортированные строки автоматически; оператор/пользователь создает
  новый пустой household и проходит отдельный reviewed deletion/household recovery
  workflow. Такой workflow не является частью importer.
- Control metadata не содержит backup. Completed import run сохраняется для
  idempotency/audit; preview rows очищаются после 24 часов.
- Down будущей control-plane migration удаляет только preview/import tables, их
  indexes/constraints/functions. Он не удаляет и не изменяет accounts, categories,
  transactions, budgets, goals, contributions, debts, payments, households или
  `audit_log`. Между financial/audit rows и import-run не создается reverse FK,
  который потребовал бы cascade/delete history при Down.

## Обязательные тесты implementation stage

### Parser и limits

1. Valid canonical v5 проходит preview.
2. Invalid UTF-8, BOM, wrong charset/content encoding, empty/multiple root values,
   non-whitespace trailing bytes отклоняются.
3. Duplicate keys на root/nested object и unknown fields отклоняются.
4. Version string/5.0/4/6, missing arrays и legacy fallback отклоняются.
5. Body/depth/string/every count/total count boundary: max accepted, max+1 rejected.
6. Integer exponent/fraction/overflow и field rune/byte limits отклоняются без
   float conversion.

### Mapping и dry-run

7. Account и отдельный budget canonical UUID fixture дают exact UUID из таблицы;
   fixtures стабильны между процессами, а household/entity type меняют UUID.
8. Duplicate/exact/Unicode-colliding category names и every unknown reference
   отклоняются до token issue.
9. Income/expense/transfer shapes, category type and account references проверены.
10. Budgets создаются только для positive expense budget на explicit month;
    contributions/recurring/account-owner links не создаются.
11. Goal/debt/payment/archive defaults и warnings соответствуют contract.
12. Preview counts/totals/checksums совпадают с independent big.Int fixture;
    transfer net zero, balance adjustments и monthly cash-flow проверены.
13. Legacy paid/left mismatch и aggregate/account/household overflow отклоняются.
14. Preview не изменяет ни одну финансовую таблицу/audit_log и не сохраняет raw
    body, names, IDs, notes или amounts в metadata.

### Auth, token и concurrency

15. Missing JWT = 401; member/admin = 403; owner другого/missing household = opaque
    404; deleted user/inactive membership fail closed.
    Preview owner+empty+metadata выполняются atomic control transaction под
    household `FOR KEY SHARE`.
16. Token hash-only storage, 256-bit entropy, TTL boundary and binding to actor,
    household, raw digest, budget month and policy version.
    HMAC config tests проверяют missing/invalid active key, missing referenced key,
    DB-unavailable startup failure, restart с тем же keyring и replay через old
    stored key ID после controlled active-key rotation.
17. Wrong/expired/revoked/consumed/cross-bound tokens имеют один safe error и не
    появляются в logs/audit.
18. Два concurrent confirm одного token/key: one commit + one replay; replay после
    lost response проходит до empty/token checks даже при non-empty household и
    consumed/expired token; разные keys не создают второй import.
19. Два owners/tokens одного household: household lock оставляет один import.

### Transaction, idempotency и recovery

20. Empty check учитывает archived/deleted и все financial tables. Отдельный real
    PostgreSQL race test запускает обычный finance create (`FOR KEY SHARE`) против
    confirm (`FOR UPDATE`): либо create commits первым и confirm видит non-empty,
    либо importer commits первым; строки не смешиваются в import transaction.
21. Clean confirm inserts every mapped entity, `source=import`, expected versions,
    one safe aggregate audit event and completed import-run. Audit использует
    существующие `entity_type='households'`, `action='imported'`, а `entity_id`
    равен household UUID; audit CHECK не меняется.
22. SQL constraint/audit failure, context cancellation и shutdown в каждой фазе
    insert дают полный rollback, включая удаление preview metadata.
23. Retry after rollback succeeds; retry after lost success response returns stored
    minimal result without rows/audit duplication; persisted run содержит exact
    allowlisted fields/HMAC/counts и не содержит totals/content/digest/token.
24. Same idempotency key/different digest or month = deterministic 409.
25. Post-insert SQL NUMERIC reconciliation matches preview; forced mismatch rolls
    back.
26. Completed import cannot be repeated with a new token/key because household is
    non-empty.

### HTTP и operations

27. Strict methods/body/query/headers, 32 MiB counted reader and safe status mapping.
28. CORS exact allowlist/preflight/Vary/allowed+exposed headers; no wildcard или
    credentials. Client `X-Request-ID` не разрешен/не принимается, server response
    `X-Request-ID` exposed.
29. Captured logs contain no JWT, token, key, digest, filename, JSON fragments,
    notes, names, legacy IDs, month or monetary values; safe route has no IDs.
30. Rate/concurrency limits return bounded 429 and release slots after
    cancellation/panic recovery.
31. Used-data migration regression: Up создает control metadata, fixture выполняет
    успешный импорт и `households/imported` audit, Down удаляет только preview/run
    metadata, но сохраняет все financial и audit rows byte/row-equivalent; повторный
    Up создает пустые control tables и не меняет сохраненную историю.
32. Trusted-proxy acceptance test доказывает отсутствие transparent request
    decompression и согласованный raw-body limit; compressed body отклоняется API.

## Acceptance gate будущей реализации

- design-approved reversible control-plane migration и SQL constraints для
  preview/import runs; used-data Down→Up сохраняет financial/audit history;
- unit/fuzz tests strict parser, duplicate detector, limits, deterministic IDs,
  money/date mapping;
- real disposable PostgreSQL integration, включая concurrency/race/cancel/audit;
- HTTP/auth/CORS/log-redaction tests без real credentials/network;
- `go test -race -count=1 -p 1 ./...`, `go vet`, build в `/private/tmp`;
- frontend importer UI будет отдельным gate и не входит в backend implementation;
- legacy Python/JS, frontend typecheck/test/build, migration up/down/up, secret
  scan, `git diff --check` и protected hashes;
- `finance.db` не читается и не изменяется ни одним importer test.

## Открытые решения и safe defaults

Следующие пункты остаются открытыми для review, но реализация не должна выбирать
менее безопасный вариант молча:

1. **Hard caps после profiling.** Default — 32 MiB/300 000 objects и таблица выше;
   увеличение требует memory/load evidence, уменьшение backward-compatible.
2. **Import metadata migration.** Default — две минимальные control-plane tables,
   hash-only preview и persistent completed run; не использовать stateless signed
   token, потому что он не обеспечивает race-safe single use сам по себе.
3. **Audit semantics.** Default окончательно выбран: один aggregate event
   `entity_type='households'`, `action='imported'`, `entity_id=household UUID` без
   amounts/content; audit CHECK migration не изменяет.
4. **Retention.** Default — preview metadata purge через 24 часа, completed runs
   сохраняются бессрочно для idempotency/audit, raw backup не сохраняется никогда.
5. **Budget month UX.** Default — explicit first-of-month header, suggested value
   только из `exportedAt` offset; отсутствие подтвержденного month блокирует preview.
6. **System flags.** Default — сохранять legacy `system=true` и показать warning;
   importer не переопределяет семантику принятого backup.
7. **Owner labels.** Default — только `legacy_owner_label`, `owner_user_id=NULL`; no
   automatic identity matching.
8. **Post-commit undo.** Default — отсутствует. Любой массовый delete/household
   recovery требует отдельного threat model и design gate.
9. **Production distributed limits.** Default API имеет local limiter; включение
   production endpoint дополнительно требует TLS, trusted proxy, global limiter,
   secret store и observability redaction review.

До принятия этого design gate миграции, importer service/repository, HTTP routes и
React upload UI не создаются.
