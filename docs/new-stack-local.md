# Локальный запуск параллельного каркаса

Новый React + Go + PostgreSQL каркас живет рядом с текущим Python-приложением и
не заменяет его. Старую версию по-прежнему можно запустить из корня командой
`python3 server.py` на `http://127.0.0.1:3212`. Новые сервисы используют другие
порты и не читают `finance.db`:

- React/Vite: `http://127.0.0.1:5173`;
- Go API: `http://127.0.0.1:8080`;
- PostgreSQL: `127.0.0.1:5432`.

Этап 3 добавляет проверку Supabase access token и защищенный контур профиля и
семейных пространств. Финансовых endpoint, importer и основного финансового UI
по-прежнему нет.

## Требования

- Node.js 20.19+ или 22.12+ и npm;
- Go 1.24+;
- Docker с поддержкой Compose — только для локального PostgreSQL.

## Переменные окружения

Все доступные параметры перечислены в корневом `.env.example`; настоящих
credentials в нем нет. Placeholder-значения Supabase намеренно не запускают
авторизацию: frontend показывает безопасный экран настройки, а backend завершает
startup при отсутствующей или placeholder auth-конфигурации.

Для проверки auth shell скопируйте пример и задайте в локальном `.env`, который
исключен из Git, URL проекта, publishable/legacy anon key, точные issuer, audience
и JWKS URL. Service-role key и JWT signing secret браузеру не нужны и запрещены:

```bash
cp .env.example .env
```

Go читает переменные процесса. При использовании локального `.env` загрузите их
перед запуском backend:

```bash
set -a
source ../.env
set +a
go run ./cmd/api
```

## 1. PostgreSQL

Из корня проекта:

```bash
docker compose -f deploy/compose.yaml up -d postgres
docker compose -f deploy/compose.yaml ps
```

Остановить контейнер без удаления локальных данных:

```bash
docker compose -f deploy/compose.yaml stop postgres
```

Конфигурация предназначена только для разработки: пароль демонстрационный,
порт привязан к `127.0.0.1`, production-деплой не предусмотрен.

## 2. Go API

В отдельном терминале:

```bash
cd backend
go run ./cmd/api
```

Проверка при работающем PostgreSQL:

```bash
curl -i http://127.0.0.1:8080/api/health
```

Ожидается HTTP `200` и безопасный ответ без строки подключения:

```json
{"api":{"status":"available"},"database":{"status":"available"},"status":"ok"}
```

Если PostgreSQL остановлен, API продолжает работать и возвращает HTTP `503`:

```json
{"api":{"status":"available"},"database":{"status":"unavailable"},"status":"degraded"}
```

## Миграции PostgreSQL

Goose `v3.24.3` закреплен как tool dependency отдельного модуля `database`.
Перед выполнением установите `DATABASE_URL` на локальную или временную базу:

```bash
cd database
go tool goose -dir migrations postgres "$DATABASE_URL" up
go tool goose -dir migrations postgres "$DATABASE_URL" status
go tool goose -dir migrations postgres "$DATABASE_URL" down
```

Интеграционный сценарий принимает только отдельную переменную
`DATABASE_TEST_URL`, чтобы тестовая база выбиралась явно:

```bash
cd database
DATABASE_TEST_URL="$DATABASE_TEST_URL" ./test-schema.sh
```

## 3. React frontend

В отдельном терминале:

```bash
cd frontend
npm install
npm run dev
```

Vite проксирует `/api` на `http://127.0.0.1:8080`. Экран показывает проверку
конфигурации, вход, регистрацию, восстановление пароля, выход, bootstrap профиля,
список, выбор и создание семейного пространства. Access token получает и хранит
только официальный Supabase Auth client; прикладные запросы идут через Go API.

## Проверки нового каркаса

```bash
cd frontend
npm run typecheck
npm test -- --run
npm run build
```

```bash
cd backend
gofmt -w ./cmd ./internal
go vet ./...
go test ./...
```

Проверки старой версии по-прежнему запускаются из корня и используют временную
SQLite-базу согласно `README.md`.
