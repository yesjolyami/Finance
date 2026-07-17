# CI и release-артефакты

Репозиторий содержит два GitHub Actions workflow. Они ничего не развёртывают и
не меняют production-инфраструктуру.

## Автоматический CI

`.github/workflows/ci.yml` запускается для push и pull request. Обязательные
jobs:

- `Legacy safety net` — синтаксис и регрессии локальной Python/SQLite-версии;
- `React and TypeScript` — locked install, frontend-тесты, typecheck и build;
- `PostgreSQL migrations` — полный up/down/up и SQL constraint suites на
  одноразовой PostgreSQL 17;
- `Go API` — интеграционные тесты, race detector, `go vet` и `gofmt`;
- `Production package policy` — offline security verifier, Linux build,
  checksum и manifest.

Для защиты основной ветки в GitHub следует потребовать успешного прохождения
всех пяти jobs. Workflow имеет только permission `contents: read`, checkout не
сохраняет GitHub credentials, а сторонние actions закреплены полными commit SHA.

## Ручная production-сборка

`.github/workflows/release-artifact.yml` запускается только вручную через
`workflow_dispatch`. Перед запуском в GitHub Repository Variables нужно создать:

- `FINANCE_SUPABASE_URL` — точный HTTPS origin проекта Supabase;
- `FINANCE_SUPABASE_PUBLISHABLE_KEY` — только публичный publishable/anon key.

Service-role key, JWT signing secret, database DSN и прочие production-секреты
в frontend build передавать запрещено. При пустых, placeholder или опасных
значениях сборка завершится ошибкой.

Оператор задаёт semantic version и архитектуру `amd64` либо `arm64`. Workflow:

1. повторно требует успешного прохождения всех пяти CI jobs для выбранного SHA;
2. повторяет offline production gate;
3. собирает Linux release с зафиксированными Go 1.26.5, Node 24.14.0 и npm
   11.9.0;
4. проверяет `SHA256SUMS`;
5. создаёт детерминированный `tar.gz` и отдельный SHA-256 файл;
6. сохраняет их как workflow artifact на 14 дней.

Создание GitHub Release, commit/tag, push, деплой, миграции реальной БД и
публикация трафика намеренно не выполняются. После скачивания артефакта оператор
продолжает по [production runbook](production-operations-stage5.md).
