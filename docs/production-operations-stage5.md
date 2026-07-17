# Production packaging и operations readiness — этап 5E2

Документ фиксирует проверяемый production-контур, но не означает, что сервис
опубликован. Поддерживается только один host: один nginx завершает TLS и
проксирует на один Go API process на `127.0.0.1`; PostgreSQL доступен API по
защищенному DSN. Несколько API replicas запрещены.

## Что оператор обязан предоставить

- реальный DNS hostname приложения;
- operator-managed ACME/certbot certificate и private key;
- точный Supabase project hostname, HTTPS issuer/JWKS и frontend publishable
  либо legacy anon key; service-role и JWT signing secrets запрещены;
- PostgreSQL DSN в owner-only credential file, с TLS `sslmode=require`,
  `verify-ca` или предпочтительно `verify-full` для внешней БД;
- непривилегированного системного пользователя и группу `finance`;
- при включении import: owner-only HMAC keyring, active key ID и production
  import acknowledgement;
- age recipient/identity, libpq service/pass files, backup retention и отдельную
  restore-drill database;
- monitoring/alerting destinations и ответственных за incident response.

Шаблоны содержат `__...__` placeholders. Их нельзя копировать в active
configuration напрямую: `render-templates.sh` и `verify-offline.sh` отклоняют
неразрешенные либо оставшиеся placeholders.

## Артефакты и воспроизводимая сборка

`deploy/production/build-release.sh`:

- требует explicit `RELEASE_VERSION`, `SOURCE_DATE_EPOCH`, absolute
  `OUTPUT_DIR`, exact HTTPS Supabase URL и только publishable/anon key;
- всегда собирает frontend с `VITE_API_BASE_URL=""`, то есть same-origin API;
- выполняет pinned `npm ci` по lockfile и Vite production build;
- собирает Linux `CGO_ENABLED=0` Go binary с `-trimpath`, отключенным VCS
  stamping и пустым build ID;
- копирует versioned migrations 00001–00004 и pinned goose module;
- создает `SHA256SUMS`, deterministic `manifest.json` и runtime dependency
  inventory;
- сканирует release tree на private keys, service-role/secret keys и DSN с
  credentials.

Build script проверяет exact Go 1.26.5, Node 24.14.0 и npm 11.9.0. Оператор
должен запускать его в immutable runner image с этими версиями. Репозиторий
фиксирует module/npm graph; digest самого runner image остается внешним
операционным входом и должен фиксироваться CI.

`OUTPUT_DIR` обязан быть новым несуществующим либо существующим строго пустым
каталогом, принадлежащим effective user и без group/other write bits.
Каталог и каждый существующий компонент пути не могут быть symlink. Корень,
home, repository и системные деревья отклоняются. Build/render scripts не
выполняют recursive cleanup и не заменяют существующее содержимое: для каждого
запуска создается новый staging path.

## Host layout и permissions

Рекомендуемый layout:

```text
/opt/finance/releases/<version>/     root:root 0755, read-only
/opt/finance/current -> releases/... root:root symlink
/etc/finance/finance-api.env         root:finance 0640
/etc/finance/secrets/                finance:finance 0700
  database-url                       finance:finance 0400
  import-hmac-keyring.json           finance:finance 0400
/etc/nginx/sites-available/finance   root:root 0644
/etc/nginx/snippets/finance-security-headers.conf root:root 0644
/var/backups/finance/                backup operator 0700
```

Go проверяет database credential и import keyring как regular non-symlink
owner-owned files с `0400` либо `0600`. `DATABASE_URL` в production запрещен;
обязателен `DATABASE_URL_FILE`. Файл содержит ровно одну строку DSN, допускается
один завершающий LF; NUL, CR, дополнительные строки и outer whitespace
отклоняются. Path и содержимое не попадают в error/log.

systemd unit запускает API без shell, от dedicated user, с `UMask=0077`,
loopback-only bind, `NoNewPrivileges`, `PrivateTmp`, `ProtectSystem=strict`,
`ProtectHome`, empty capabilities, bounded files/tasks/memory/CPU и
`TimeoutStopSec=90s`. `/opt/finance` и `/etc/finance` доступны только для чтения;
API не получает writable application directory.

## TLS, nginx и CSP

nginx template:

- принимает HTTP только для точного configured hostname и перенаправляет на
  фиксированный `https://<configured-host>$request_uri` кодом 308, не отражая
  входящий `Host`;
- имеет default HTTP vhost с fail-closed ответом и default HTTPS vhost,
  отклоняющий неизвестный SNI во время TLS handshake;
- принимает TLS 1.2/1.3 с operator-managed certbot paths;
- не публикует Go port и проксирует только на `127.0.0.1:8080`;
- удаляет `Forwarded`, все `X-Forwarded-*` и `X-Real-IP`; API их не использует;
- применяет per-IP connection/request limits и отдельную import zone;
- ограничивает обычный API body 1 MiB, import raw body ровно 32 MiB;
- для import отключает request buffering/transform и использует bounded
  connect/read/send timeout 5/130/130 секунд;
- import regex-location отдельно подключает security-header snippet и
  `Cache-Control: no-store`, поскольку nginx `add_header` inheritance зависит от
  location;
- не включает request decompression; Go дополнительно отклоняет любой
  `Content-Encoding`;
- отдает hashed `/assets/` как immutable, а `index.html`, API и error page —
  `no-store`;
- скрывает server tokens и directory listing.

Security headers вынесены в единый snippet и повторно include-ятся в locations,
где nginx `add_header` иначе отменил бы inheritance. CSP не содержит
`unsafe-inline` или `unsafe-eval`:

```text
default-src 'none';
base-uri 'self';
connect-src 'self' https://<supabase-host> wss://<supabase-host>;
font-src 'self';
form-action 'self';
frame-ancestors 'none';
img-src 'self' data:;
manifest-src 'self';
object-src 'none';
script-src 'self';
style-src 'self';
worker-src 'self'
```

React inline style props удалены: доли расходов используют semantic
`<progress>`, цвета — allowlisted `data-color` selectors. `eval`,
`dangerouslySetInnerHTML` и inline style regression сканируются.

HSTS нельзя включать до проверки реального TLS, redirect и renew path. После
проверки certificate chain/renewal сначала рендерится `max-age=300`, выполняется
smoke и наблюдение; затем отдельным осознанным изменением —
`max-age=31536000`. `includeSubDomains` и `preload` намеренно не включены:
их можно рассматривать только после отдельного аудита всех subdomains.

ACME HTTP-01 challenge обслуживается только известным hostname из
`/var/lib/letsencrypt/.well-known/acme-challenge/`; default vhost challenge не
обслуживает. Первый сертификат получают до активации TLS-vhost через DNS-01,
certbot standalone либо отдельную минимальную bootstrap-конфигурацию, после чего
ставят полный template и проверяют `certbot renew --dry-run`. Обычное HTTP-01
renewal использует documented webroot; нельзя временно добавлять redirect через
`$host`.

## Pre-deploy migration

API никогда не применяет migrations автоматически. До запуска нового binary:

1. сделать encrypted PostgreSQL backup;
2. остановить mutation traffic либо перевести nginx в maintenance;
3. запустить `scripts/migrate.sh` от отдельного operator account;
4. script использует `DATABASE_URL_FILE` через `GOOSE_DBSTRING` environment,
   запускает release-bundled pinned goose `v3.24.3`, выполняет `status`, `up`, затем exact
   `version=4`;
5. production API при старте независимо делает bounded ping и exact applied
   migration check before listener.

DSN не передается в argv и не печатается. Migration directory и Go module
versioned вместе с release.

## Backup и restore drill

`backup.sh` использует `PGSERVICEFILE`, `PGPASSFILE` и non-secret `PGSERVICE`.
`pg_dump` custom stream немедленно шифруется `age`; plaintext file на disk не
создается. Destination создается atomically с `0600`, retention bounded
1–365 дней. Secret values не входят в argv/log.

Перед backup/restore scripts проверяют operational files без раскрытия paths:
`PGPASSFILE` и age identity должны быть regular non-symlink, принадлежать
effective user и иметь ровно `0400`/`0600`; libpq service и публичный age
recipient не могут быть writable для group/other. Файлы ограничены по размеру.

`restore-drill.sh` требует:

- отдельный libpq service, отличный от `finance_production`;
- `RESTORE_DRILL_ACK=ack-isolated-empty-restore-database`;
- explicit `RESTORE_EXPECTED_DATABASE` из allowlist, обязательно с суффиксом
  `_restore_drill`;
- точное совпадение `SELECT current_database()` с ожидаемым именем;
- отсутствие любых user/application/goose tables до запуска `pg_restore`.

Только после этих проверок encrypted stream передается из `age` прямо в
`pg_restore`. Затем проверяются goose binary `v3.24.3`, exact migration version
`4`, четыре applied versions и четыре SQL constraint suites. DSN/password не
передаются в argv и не печатаются. Переименование libpq service alias не
обходит проверку реальной целевой БД. Drill никогда не запускается против
production database и не касается `finance.db`.

Restore drill выполняется минимум ежеквартально и после изменения backup
provider/keys. Фиксируются дата, artifact checksum, PostgreSQL version,
duration и безопасный pass/fail без финансовых данных.

## Rollout

1. Подтвердить single-replica topology, ответственных и maintenance window.
2. Настроить DNS, но не направлять traffic до готовности TLS.
3. Создать certbot/ACME certificate безопасным bootstrap flow, установить
   known-host template и проверить renewal dry-run.
4. Настроить в Supabase точные Site URL, redirect URLs и allowed origins.
5. Создать `finance` user/group, directories и permissions из layout.
6. Создать database credential file; raw `DATABASE_URL` не задавать.
7. Собрать release offline, сверить manifest/checksums и secret scan.
8. Отрендерить non-secret env/nginx templates; `nginx -t` и
   `systemd-analyze verify`.
9. Сделать encrypted backup и выполнить migration procedure до version 4.
10. Переключить `/opt/finance/current`, daemon-reload, запустить API.
11. Проверить journal только на safe startup classes, затем local loopback
    health.
12. Перезагрузить nginx и запустить `smoke.sh`; он проверяет exact redirect
    target, headers и health. Опциональный auth token принимается только из
    owner-only regular non-symlink файла `0400`/`0600`, одной bounded JWT-строкой
    с base64url-dot charset; invalid token file отклоняется до создания curl
    config.
13. Включить traffic, наблюдать 4xx/5xx/429, latency, DB pool, disk и cert expiry.
14. После TLS canary увеличить HSTS до годового значения.
15. Import оставлять выключенным, пока отдельно не предоставлены keyring,
    acknowledgement, backup/restore readiness и операционное разрешение.

## Rollback

- frontend/static и Go binary откатываются переключением `current` на
  предыдущий checksum-verified release и restart;
- после появления данных запрещено выполнять goose `down` как обычный rollback;
- forward-compatible DB fix делается новой migration;
- DB restore — отдельная аварийная процедура в новую БД с проверкой, затем
  осознанное переключение credential file; production database не
  перезаписывается вслепую.

## Operational incidents

- **JWKS outage:** cached valid keys работают только в принятой bounded policy;
  новые/unknown keys fail closed. Не отключать signature verification.
- **429/load:** проверить nginx per-IP и process limiter metrics/log classes;
  не увеличивать replicas выше 1. Сначала уменьшить abuse/дорогие запросы.
- **DB outage:** API startup/health fail closed; не обходить migration/ping.
- **Disk full:** остановить rollout/import, освободить безопасный backup/log
  volume, проверить PostgreSQL consistency; не удалять active DB files вручную.
- **Certificate renewal:** alert до expiry, certbot dry-run, `nginx -t` и reload;
  private key permissions не ослаблять.
- **Keyring rotation:** добавить новый active key, сохранить все referenced old
  keys, restart + startup audit; удалять old key только после доказанной
  retention/migration policy.
- **Suspected token/secret leak:** отключить traffic, rotate affected Supabase,
  DB/import credentials, сохранить redacted logs, проверить audit log и import
  runs, затем восстановить через standard rollout.

## Остаточные риски

- process-local limiter и single replica ограничивают availability/scaling;
- TLS, DNS, per-IP perimeter, monitoring, backup encryption/retention и restore
  discipline остаются ответственностью оператора;
- Supabase project policy, email security и MFA enforcement не управляются этим
  repository;
- CSP зависит от точной подстановки Supabase host и должна повторно проверяться
  после добавления любых внешних ресурсов;
- toolchain/container image для build должен быть закреплен внешним CI/runner;
- финальный внешний security audit и реальный browser/TLS smoke еще не выполнены.
