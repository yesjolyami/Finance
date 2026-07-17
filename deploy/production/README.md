# Production package boundary

Этот каталог содержит только offline-шаблоны и инструменты для поддерживаемой
topology `nginx on host -> Go API on 127.0.0.1 -> PostgreSQL`.

Он не выполняет deployment и не содержит реальных доменов, сертификатов,
Supabase configuration или database credentials.

Основные команды:

```bash
./scripts/verify-offline.sh
```

`build-release.sh` и `render-templates.sh` принимают только новый
несуществующий либо уже существующий строго пустой output-каталог вне
repository, home и системных деревьев. Существующий каталог должен принадлежать
effective user и не иметь group/other write bits. Они никогда не очищают
указанный путь.
Для повторного запуска используйте новый staging-каталог; содержимое
существующего каталога оператор удаляет отдельно только после ручной проверки.

```bash
OUTPUT_DIR=/private/tmp/finance-rendered \
FINANCE_DOMAIN=operator-supplied.example \
SUPABASE_HOST=operator-project.supabase.co \
HSTS_MAX_AGE=300 \
./scripts/render-templates.sh
```

Второй пример намеренно не пройдет placeholder policy: оператор обязан подставить
собственный реальный домен. Полный порядок действий, HSTS canary, migration,
backup/restore drill и rollback описаны в
[`docs/production-operations-stage5.md`](../../docs/production-operations-stage5.md).
