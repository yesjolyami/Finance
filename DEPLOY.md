# Деплой

Этот проект можно запускать локально, на обычном VPS или в панели вроде NomadHost.

> **Ограничение текущей версии:** Python-сервер рассчитан прежде всего на локальный режим. В нем нет пользователей, аутентификации, разграничения доступа, CSRF-защиты, rate limiting и встроенного TLS. Не публикуй порт `3212` напрямую в интернете. Для удаленного доступа обязателен внешний HTTPS reverse proxy с аутентификацией и сетевым ограничением доступа; безопаснее оставить `HOST=127.0.0.1`.

> **Новый React/Go/PostgreSQL контур:** создан проверяемый offline production
> package для host-based topology, но реальный deployment не выполнен.
> Supabase URL и publishable key задаются только при build; service-role
> key и JWT signing secret запрещено передавать frontend. Go API имеет bounded
> process-global/per-subject limiter для единственной replica. Templates nginx,
> systemd, strict CSP, file credentials и backup/restore находятся в
> `deploy/production`; реальные DNS/TLS/secrets/traffic не настраиваются.

> **Production security gate:** код поддерживает только профиль
> `single-proxy-single-replica-v1`: один Go process на loopback за одним
> доверенным HTTPS reverse proxy. Это не готовый deployment — реальные proxy,
> TLS, secret-store mount и публичная публикация здесь не создаются. Обязательства
> topology, Origin/CSRF, rate limits и PostgreSQL описаны в
> `docs/production-security-stage5.md`.

> **Production operations:** точный порядок build/migrate/rollout/rollback,
> operator inputs, HSTS canary, encrypted PostgreSQL backup и isolated restore
> drill описаны в `docs/production-operations-stage5.md`. Raw `DATABASE_URL` в
> production запрещен: Go API читает только owner-only `DATABASE_URL_FILE`.

> **CI/release:** GitHub Actions автоматически проверяет legacy-контур,
> React/TypeScript, Go, PostgreSQL-миграции и production package. Отдельный
> ручной workflow создаёт только закрытый checksum-verified Linux-артефакт и не
> выполняет deploy. Настройка описана в `docs/ci-release.md`.

> **Backup v5 import:** по умолчанию новый Go API запускается с
> `IMPORT_BACKUP_V5_ENABLED=false`, а import-маршруты отвечают fail-closed
> `503`. Production-включение допустимо кодом только вместе с точным security
> profile/ack, одной replica, отдельным import acknowledgement, защищенным
> owner-only keyring file и успешным startup audit retained keys. Реальный
> secret-store и операционный rollout по-прежнему отложены.

## 1. Локальный запуск

```bash
python3 server.py
```

Открыть:

```text
http://127.0.0.1:3212
```

По умолчанию база хранится в `finance.db` рядом с проектом.

## 2. Подготовка репозитория

Если проект уже есть на GitHub:

```bash
git clone https://github.com/yesjolyami/Finance.git
cd Finance
```

Если репозиторий уже открыт локально, достаточно обновить код:

```bash
git pull
```

## 3. Обычный VPS

### Файлы

Скопируй в папку проекта:

- `server.py`
- `app.js`
- `index.html`
- `styles.css`
- `README.md`
- `DEPLOY.md`
- `run.sh` если используешь запуск через shell-скрипт

### Переменные окружения

Рекомендуемые значения:

```bash
HOST=0.0.0.0
PORT=3212
FINANCE_DB=/path/to/finance.db
```

`HOST=0.0.0.0` нужен, чтобы сервер был доступен извне контейнера или VPS.

Используй `0.0.0.0` только внутри закрытой сети или контейнерного контура, где внешний reverse proxy уже завершает HTTPS и проверяет пользователя. Само приложение не защищает API от чтения, изменения или полного импорта данных авторизованным на сетевом уровне клиентом.

### systemd

Создай сервис:

```ini
[Unit]
Description=Монетка
After=network.target

[Service]
WorkingDirectory=/var/www/finance
Environment=HOST=0.0.0.0
Environment=PORT=3212
Environment=FINANCE_DB=/var/www/finance/finance.db
ExecStart=/usr/bin/python3 /var/www/finance/server.py
Restart=always
RestartSec=3

[Install]
WantedBy=multi-user.target
```

Команды запуска:

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now finance
sudo systemctl status finance
```

## 4. NomadHost

Если проект загружается через файловую панель NomadHost:

### Шаги

1. Открой `Files`.
2. Залей файлы проекта в `/home/container`.
3. Убедись, что рядом лежит `server.py`.
4. В `Startup` укажи:

```bash
./run.sh
```

### `run.sh`

Если панель требует shell-скрипт, создай файл `run.sh`:

```sh
#!/bin/sh
python3 server.py
```

И сделай его исполняемым:

```bash
chmod +x run.sh
```

### Если нужен прямой запуск

В некоторых панелях можно просто указать:

```bash
python3 server.py
```

### Важно

- Сервер должен слушать `0.0.0.0`, а не `127.0.0.1`.
- Порт приложения не должен быть открыт напрямую; наружу публикуется только защищенный reverse proxy.
- Если панель сама подставляет команду запуска, убери лишний текст и оставь только запуск приложения.
- `finance.db` можно не хранить в репозитории, если это личная база.

## 5. Проверка после деплоя

Проверь, что страница открывается и API отвечает:

```text
/api/health
```

Если сайт не открывается:

1. Проверь `HOST` и `PORT`.
2. Проверь путь к `finance.db`.
3. Проверь, что файлы лежат в одной папке с `server.py`.
4. Посмотри логи процесса.

Успешный health-check возвращает только `{"ok": true}` и намеренно не сообщает путь к базе или другие внутренние параметры.

## 6. Обновление

Чтобы выкатить новую версию:

```bash
git pull
```

или перезалей файлы через панель хостинга.
