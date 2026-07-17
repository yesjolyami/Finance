# Production security gate — этап 5E1

Этот документ фиксирует поддерживаемый security profile до появления реального
production deployment. Он не является инструкцией по публикации сервиса и не
создает reverse proxy, container image или публичный endpoint.

## Threat model

Trust boundary:

```text
Browser -> единственный trusted HTTPS reverse proxy -> один Go API process -> PostgreSQL
                                      |
                                      +-> Supabase JWKS по HTTPS
```

Основные угрозы текущего API:

- прямой доступ к Go listener в обход TLS, per-IP connection limits и body limits;
- подмена `Origin`, duplicate headers и browser CSRF/confused-deputy запросы;
- перегрузка JWT/JWKS verification до прикладной авторизации;
- abuse одним валидным subject после успешной проверки JWT;
- запуск нескольких replicas с независимыми process-local limiters;
- запуск с неполной схемой PostgreSQL, недоступной БД или небезопасным DSN;
- утечка Bearer JWT, import token, idempotency key, DSN, keyring path/key ID,
  backup digest или финансовых значений через error/log/health;
- небезопасный import keyring: symlink, слишком широкие permissions, отсутствующий
  retained rotation key;
- частичная публикация production-конфигурации с local defaults/placeholders.

Tenant IDOR, privilege escalation, JWT algorithm confusion, import replay и
транзакционная целостность покрыты предыдущими этапами и остаются обязательными
инвариантами.

## Поддерживаемая topology v1

Production поддерживает только:

- `PRODUCTION_SECURITY_PROFILE=single-proxy-single-replica-v1`;
- `PRODUCTION_SECURITY_ACK=ack-single-proxy-single-replica-v1`;
- `API_REPLICA_COUNT=1`;
- явный loopback `HTTP_HOST=127.0.0.1` или `::1`;
- один внешний HTTPS reverse proxy как единственную точку входа.

Go API не читает и не доверяет `X-Forwarded-For`, `Forwarded`,
`X-Forwarded-Proto` или `X-Real-IP`. Process-global pre-auth limiter защищает
JWT perimeter целиком, а per-client-IP perimeter обязан реализовать proxy.
Несколько API replicas запрещены fail-closed, пока не появится distributed
limiter и единая координация concurrency.

Reverse proxy обязан:

- завершать TLS, перенаправлять HTTP на HTTPS и не публиковать Go listener;
- подключаться к loopback listener на том же host/network namespace;
- удалять входящие `Forwarded`/`X-Forwarded-*` и задавать собственные значения
  только для proxy logs; Go API их не использует;
- иметь bounded connection/per-IP request limits, header/body limits не слабее
  Go API и отдельные лимиты для auth/import;
- передавать request body без прозрачной decompression; import принимает только
  raw JSON и отклоняет любой `Content-Encoding`;
- сохранять streaming и timeout не меньше 120 секунд для import, но никогда не
  использовать unbounded upstream timeout.

## Production configuration

В production без default обязательны `HTTP_HOST`, `HTTP_PORT`, `DATABASE_URL_FILE`,
`FRONTEND_ORIGINS`, auth URLs, security profile/ack и replica count.

- frontend origins — только точные HTTPS origins без path, wildcard, userinfo,
  query, fragment или placeholder;
- issuer/JWKS — только HTTPS URL без credentials/query/fragment/placeholder;
- raw `DATABASE_URL` в production запрещен; DSN читается из bounded regular
  non-symlink owner-only `DATABASE_URL_FILE`;
- внешний PostgreSQL не может использовать `sslmode=disable`;
- DSN, URL values, credentials и paths никогда не включаются в ошибки или логи;
- API делает bounded startup ping и проверяет точную migration version, но не
  применяет миграции автоматически.

## Browser Origin и CSRF

API использует явный `Authorization: Bearer` и frontend отправляет
`credentials=omit`; ambient cookies отсутствуют. Это существенно уменьшает CSRF,
но не отменяет browser-origin policy и confused-deputy риск.

В production каждый `POST`, `PATCH`, `PUT` или `DELETE` под `/api/v1` обязан иметь
ровно один `Origin`, точно совпадающий с `FRONTEND_ORIGINS`. Missing, duplicate,
`null` и cross-origin отклоняются до auth/body processing. Preflight также
проверяется до auth. `GET /api/health` и non-browser read-only operational checks
не требуют Origin.

Отдельного CLI mutation bypass в profile v1 нет. Если он понадобится, он должен
получить отдельный механизм аутентификации/сетевой policy, а не неявное отсутствие
Origin.

## Rate limits

Один process является глобальной точкой:

- pre-auth perimeter — 600 попыток/минуту на process и не более 64 одновременно
  исполняющихся `/api/v1` запросов до JWT/JWKS;
- после JWT — 300 попыток/минуту и не более 16 одновременно на verified `sub`;
- subject map ограничена 8192 ключами и удаляет истекшие inactive entries;
- import дополнительно сохраняет 15-минутное окно: 5 preview и 3 non-replay
  confirm на `(actor, household)`, один active import на actor и один active
  confirm на household.

Все maps имеют hard maximum и очистку истекших inactive entries. При отказе API
возвращает bounded JSON `429` и `Retry-After`. Health не сообщает состояние,
ключи или остатки limiter.

Per-IP limits остаются обязанностью trusted proxy: Go не выводит client IP из
непроверенных forwarded headers.

## Backup v5 import в production

Помимо общего security profile production import требует:

- `IMPORT_BACKUP_V5_ENABLED=true`;
- `IMPORT_PRODUCTION_ACK=ack-backup-v5-single-replica-v1`;
- абсолютный keyring path;
- regular non-symlink file, принадлежащий effective process user;
- owner-only permissions без group/other permissions;
- успешный startup `AuditKeyring`, включая все referenced rotation key IDs.

Raw keys, key IDs и file path не логируются. Старый key нельзя удалять, пока на
его non-secret ID ссылается completed import run.

## PostgreSQL и восстановление

- pool, idle time и connection lifetime ограничены;
- production listener не стартует до bounded ping и точной migration version;
- миграции выполняются отдельной операцией и никогда автоматически API process;
- PostgreSQL backup должен быть зашифрован, доступ ограничен, retention задан;
- restore drill выполняется регулярно в одноразовую БД с migration/status,
  reconciliation и application smoke tests;
- backup/restore procedures не используют и не изменяют legacy `finance.db`.

## Логи и incident response

HTTP logs содержат только server request ID, method, нормализованный route,
status и duration. Security rejection добавляет только стабильный `error_class`
и server request ID. Запрещены headers, bodies, claims, raw token/key, DSN,
keyring path/key ID, digest, entity names/notes и финансовые суммы.

При rotation:

1. добавить новый key с новым ID, сохранив все referenced old keys;
2. выбрать active ID и перезапустить одну replica;
3. startup audit должен пройти;
4. удалить old key можно только после отдельной retention/migration процедуры,
   доказывающей отсутствие ссылок.

## Реализовано в packaging gate 5E2

Host-based nginx/systemd templates, strict CSP, file-based database credential,
release manifest/checksums, pre-deploy migrations и encrypted backup/restore
drill описаны в `docs/production-operations-stage5.md`. Это по-прежнему offline
package: реальные DNS, TLS, Supabase, PostgreSQL credentials и traffic не
настраиваются репозиторием.

## Намеренно отложено

- реальное создание host/nginx/systemd/TLS configuration;
- container/Kubernetes topology (она не поддерживается profile v1);
- distributed limiter и более одной replica;
- WAF/global abuse detection;
- external secret-store automation и автоматическая rotation;
- публичное включение сервиса и финальный rollout.
