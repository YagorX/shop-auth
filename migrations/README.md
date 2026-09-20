# migrations

Схема базы `authdb`. Формат — [`golang-migrate`](https://github.com/golang-migrate/migrate): пары файлов `{версия}_{название}.{up|down}.sql`, версия применённой миграции хранится в таблице `schema_migrations`.

## Файлы

| Файл | Что делает |
|---|---|
| `000001_init.up.sql` | Вся схема: `roles`, `users`, `apps`, `user_sessions`, `audit_logs`, `system_metrics`, индексы, триггеры `updated_at`, сиды ролей и приложений |
| `000001_init.down.sql` | Откат |
| `000002_grant_auth_user_privileges.up.sql` | Выдача прав роли `auth_user`, если она существует |
| `000002_grant_auth_user_privileges.down.sql` | Отзыв прав |
| `0003_seed_roles.sql` | ⚠️ см. «Известные проблемы» |
| `0004_seed_apps.sql` | ⚠️ см. «Известные проблемы» |

## Как применяются

**Локально** — контейнером из compose этого репозитория:

```bash
docker compose up -d postgres
docker compose run --rm migrate
```

**В составе платформы** — отдельным сервисом `auth-migrate`, который ходит в HA-кластер через HAProxy и завершается с кодом 0, после чего запускается `auth-service`:

```yaml
auth-migrate:
  image: migrate/migrate:4
  command: ["-path","/migrations","-database","postgres://postgres:postgres@postgres-haproxy:5432/authdb?sslmode=disable","up"]
```

`auth-service` объявлен через `depends_on: auth-migrate: condition: service_completed_successfully` — сервис не поднимется раньше применённой схемы.

**Из кода** — есть [`cmd/migrator`](../cmd/migrator) для случаев, когда нужен запуск в одном процессе с приложением.

## Идемпотентность

`000001` написана через `CREATE TABLE IF NOT EXISTS`, `CREATE OR REPLACE FUNCTION`, `DROP TRIGGER IF EXISTS` перед `CREATE TRIGGER` и `ON CONFLICT DO NOTHING` для сидов. Повторный прогон на уже существующей схеме не падает.

`000002` обёрнута в `DO $$ ... IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'auth_user') ... END $$` — она безвредна на стенде, где роль `auth_user` не создана. В составе платформы роль создаёт [`shop-platform/infra/postgres/init.sql`](../../shop-platform/infra/postgres/init.sql) до запуска миграций.

Почему провижининг роли не здесь: `CREATE ROLE` — кластерная операция, а не операция схемы, и Patroni не сидит прикладные базы сам. Разделение позволяет накатывать схему пользователем с обычными правами.

## Триггер updated_at

```sql
CREATE OR REPLACE FUNCTION set_updated_at() RETURNS TRIGGER AS $$
BEGIN NEW.updated_at = NOW(); RETURN NEW; END;
$$ LANGUAGE plpgsql;
```

Навешен на `users` и `apps`. Поле поддерживается базой, а не приложением — так оно не зависит от того, какой код и по какому пути делает `UPDATE`.

## Индексы

| Индекс | Назначение |
|---|---|
| `idx_users_role_id` | Джойн с `roles` при проверке `IsAdmin` |
| `idx_user_sessions_user_uuid` | Поиск всех сессий пользователя |
| `idx_user_sessions_expires_at` | Вычистка просроченных сессий |
| `idx_user_sessions_active` | Частичный: `WHERE revoked_at IS NULL`, только живые сессии |
| `idx_audit_*` | По `user_uuid`, `created_at`, `action` |
| `idx_metrics_*` | По `metric_name`, `created_at` |

`idx_user_sessions_active` — частичный индекс: в него попадают только неотозванные сессии. Индекс остаётся маленьким независимо от объёма истории.

## Известные проблемы

1. **Секреты приложений в открытом виде.** `000001` (и дублирующая её `0004`) сидят `apps.secret` значениями `web-secret`, `mobile-secret`, `test-secret`. Это ключи подписи HS256. Должны приходить из секрет-хранилища, а миграция — создавать строки без секрета либо не создавать вовсе.
2. **`0003_seed_roles.sql` и `0004_seed_apps.sql` не соответствуют формату.** Версия записана как `0003`, а не `000003`, и отсутствует суффикс `.up`. `golang-migrate` такие файлы не подхватит. Плюс их содержимое уже выполнено в `000001` — файлы стоит удалить.
3. **Нет очистки `user_sessions`.** Просроченные и отозванные сессии остаются в таблице навсегда. Индекс под это есть (`idx_user_sessions_expires_at`), самой джобы нет.
4. **`audit_logs` и `system_metrics` не используются.** Таблицы, индексы и модель `AuditLog` созданы, но ни одной записи код не делает. Либо реализовать аудит (`LOGIN_OK`, `LOGIN_FAIL`, `REFRESH_OK`, `LOGOUT` — комментарий в схеме уже перечисляет ожидаемые значения), либо убрать таблицы.
5. **`down`-миграции не проверены.** Стоит хотя бы раз прогнать `up → down → up` на чистой базе.
