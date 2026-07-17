# Миграции

Версионируемые reversible SQL-миграции выполняются goose `v3.24.3`. Инструмент
закреплен в `database/go.mod` и создает служебную таблицу `goose_db_version`.

```bash
cd database
go tool goose -dir migrations postgres "$DATABASE_URL" up
go tool goose -dir migrations postgres "$DATABASE_URL" status
go tool goose -dir migrations postgres "$DATABASE_URL" down
```

Передавать следует только локальную или временную строку подключения. Схема не
связана с пользовательским `finance.db`.

`00002_household_invitations.sql` добавляет только инфраструктуру этапа 3:
idempotency создания household, одноразовые bearer invitations и соответствующий
тип audit event. Ее `Down` сохраняет всю схему `00001`.
Так как `00001` не допускает тип `household_invitations`, rollback сначала удаляет
таблицу invitations и только относящиеся к ней audit events, затем возвращает
старый audit constraint. Остальные audit events и сущности `00001` сохраняются.

`00003_finance_core_idempotency.sql` добавляет к accounts, categories и
transactions optimistic `version`, а также SHA-256 fingerprint metadata для
безопасного replay create-запросов. Ключ и hash задаются только при INSERT:
общий trigger отклоняет любые UPDATE этих полей, включая backfill legacy rows.
Legacy transaction с уже существующим key и без fingerprint остается валидным,
но не считается replayable. `Down` удаляет только объекты `00003`; финансовые и
audit rows сохраняются.

`00004_backup_v5_import_control.sql` добавляет только control-plane метаданные
двухфазного импорта backup v5. Preview хранит tenant/actor binding, SHA-256 hash
одноразового токена, digest входного JSON, выбранный месяц бюджета и срок жизни
не более 15 минут; исходный токен и содержимое backup в PostgreSQL не попадают.
Его binding неизменяем, а `consumed_at` или `revoked_at` допускают ровно один
переход из `NULL`. Просроченный preview можно отозвать при успешном confirm,
чтобы атомарно закрыть остальные outstanding preview выбранного household.

Completed run хранит только ограниченные counts, warning counts и HMAC-значения
с не секретным `hmac_key_id`. Таблица append-only: `UPDATE` и `DELETE` запрещены
триггером. Логическая уникальность имеет scope
`household_id + actor_user_id + hmac_key_id + idempotency_key_hmac`; приложение
ищет replay по всем ID ограниченного настроенного keyring и обязано сохранять
каждый ключ, на который еще ссылаются rows. Raw HMAC keys не хранятся и не
логируются. Audit использует уже существующие `entity_type='households'` и
`action='imported'`, поэтому audit constraint не расширяется.

`Down` удаляет только обе control-plane таблицы, их индексы, триггеры и функции.
Импортированные финансовые rows, `households/imported` audit history и версии
business-сущностей сохраняются; это проверяет used-data Down→Up fixture.

`00005_onboarding_profiles.sql` добавляет настройки профиля для первого запуска:
имя, режим использования, основную валюту и признак завершённого онбординга.
Пользователи с уже существующим пространством автоматически считаются прошедшими
настройку. Миграция также добавляет тип счёта `cash` и разрешает системную
корректировку начального баланса без пользовательской категории.
