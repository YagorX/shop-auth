# internal/storage

Доступ к данным и доменные ошибки хранилища.

```text
storage/
├── storage.go     # sentinel-ошибки, общие для всех реализаций
└── postgres/      # рабочая реализация
```

## Доменные ошибки

[`storage.go`](storage.go) объявляет sentinel-значения, по которым сервисный слой различает ситуации через `errors.Is`. Они — часть контракта между хранилищем и логикой, поэтому живут в родительском пакете, а не в `postgres`:

| Ошибка | Когда |
|---|---|
| `ErrUserExist` | Нарушение уникальности `username` или `email` |
| `ErrUserNotFound` | Нет пользователя по email/username/UUID |
| `ErrAppNotFound` | Нет приложения по `app_id` |
| `ErrRefreshSessionExists` | Коллизия `refresh_token_hash` |
| `ErrRefreshSessionNotFound` | Сессии нет, она отозвана или просрочена |
| `ErrInvalidCredentials` | Неверная пара логин/пароль |
| `ErrInvalidToken` | Токен не проходит проверку |

## postgres

`Storage` — тонкая обёртка над `*sql.DB` (драйвер `pgx/v5` в режиме `database/sql`). Никакой ORM: все запросы написаны руками, все параметры передаются через `$N`-плейсхолдеры.

### Работа с NULL

Схема допускает `NULL` в `email`, `role_id`, `revoked_at`, `ip_address`, `user_agent`, `device_id`. Поэтому сканирование идёт через `sql.NullString` / `sql.NullInt64` / `sql.NullTime` с последующим переносом в указатели модели.

При записи используется обратный приём — пустая строка превращается в `NULL`:

```sql
VALUES ($1, $2, $3, NULLIF($4,'')::inet, NULLIF($5,''), NULLIF($6,''), $7)
```

Без `NULLIF` попытка записать `''` в колонку типа `inet` завершилась бы ошибкой приведения типа.

### Ротация refresh-сессии

Единственная операция, которой нужна транзакция: три шага должны быть атомарны.

```mermaid
sequenceDiagram
    participant S as services/auth
    participant T as tx
    participant DB as PostgreSQL

    S->>T: BeginTx
    T->>DB: SELECT user_uuid, expires_at, revoked_at<br/>WHERE hash=old AND device_id AND app_id
    DB-->>T: строка сессии
    T->>T: проверка revoked_at IS NULL и expires_at > now
    T->>DB: INSERT новая сессия (hash=new)
    T->>DB: UPDATE старую: revoked_at=NOW(),<br/>replaced_by_hash=new WHERE revoked_at IS NULL
    DB-->>T: RowsAffected
    alt affected = 0
        T->>DB: ROLLBACK
        Note over T,DB: кто-то отозвал сессию параллельно
    else affected = 1
        T->>DB: COMMIT
    end
```

Проверка `RowsAffected` вместо доверия предыдущему `SELECT` — то, что делает операцию безопасной при параллельных refresh с одним и тем же токеном: выиграет ровно один, второй откатится.

Ключ поиска везде составной — `(refresh_token_hash, device_id, app_id)`. Токен, украденный вместе со значением, но предъявленный с другим `device_id`, сессию не найдёт.

### Определение уникальности

`isUniqueViolation` разворачивает ошибку до `*pgconn.PgError` (именно из `github.com/jackc/pgx/v5/pgconn` — тип из `pgconn` v1 драйвер не возвращает никогда) и сравнивает `Code` с `pgerrcode.UniqueViolation`. Разбора текста ошибки нет: сообщения драйвера не входят в его контракт и меняются между версиями.

### Подключение и пул

[`postgres/connection.go`](postgres/connection.go) отвечает за создание `*sql.DB`; `New` принимает уже готовый пул и о его настройках ничего не знает.

| Что делает | Зачем |
|---|---|
| DSN собирается через `url.URL` | Пароль со спецсимволами (`@`, `:`, `/`) не ломает строку подключения |
| `application_name` в `RuntimeParams` | Соединения сервиса видны в `pg_stat_activity` |
| `statement_timeout` в `RuntimeParams` | Серверный лимит: Postgres сам прервёт зависший запрос, даже если клиент про него забыл |
| `SetMaxOpenConns` / `SetMaxIdleConns` | Потолок на инстанс; `idle == open`, иначе пул закрывает соединения между всплесками и переустанавливает их заново |
| `SetConnMaxLifetime` | Обязателен при HAProxy + Patroni: после failover соединения не должны жить на бывшем primary |
| `pingWithRetry` | Порядок старта контейнеров не гарантирован; паузы удваиваются, ожидание прерывается по отмене контекста |
| `collectors.NewDBStatsCollector` | `go_sql_*` на `/metrics`: `in_use`, `idle`, `wait_count`, `wait_duration`. Растущий `wait_count` означает, что пул исчерпан |

Значения приходят из `postgres`-секции конфига и проверяются в `config.Validate`: `max_open_conns` и `conn_max_lifetime` обязаны быть `> 0`, потому что `0` в `database/sql` значит «без ограничений» и «жить вечно» — ровно те два дефолта, от которых этот файл и защищает.

`HealthChecker` в том же файле обслуживает `/readyz` и делает ping с собственным таймаутом, не наследуя срок жизни HTTP-запроса.

### Фильтр `is_active`

Проверка «пользователь не деактивирован» живёт **в SQL**, а не в Go, — во всех четырёх запросах, которые читают пользователя:

| Запрос | Условие |
|---|---|
| `User()` | `WHERE (email = $1 OR username = $1) AND is_active = true` |
| `UserByID()` | `WHERE id = $1 AND is_active = true` |
| `UserByUUID()` | `WHERE uuid = $1 AND is_active = true` |
| `IsAdminByUUID()` | `WHERE u.uuid = $1 AND u.is_active = true` — запрос отдельный, с JOIN на `roles`, мимо `UserByUUID` |

Смысл в том, что прочитать деактивированного пользователя через эти методы становится невозможно физически. Проверку в Go можно забыть в новом методе, условие в запросе — нет. Для сервиса деактивированный пользователь неотличим от удалённого: оба дают `ErrUserNotFound`.

**Скобки в `User()` обязательны.** `AND` связывает сильнее `OR`, поэтому без них условие читалось бы как `email = $1 OR (username = $1 AND is_active)`, и деактивированный пользователь продолжал бы входить по email. Ошибка тихая: тест «активный логинится» проходит в обоих вариантах. Ловит её только `TestUserSkipsDeactivatedByEitherField`, который ищет по **обоим** полям.

### Тесты хранилища

[`postgres/user_active_test.go`](postgres/user_active_test.go) работает с настоящей базой, потому что проверяет SQL, а не код вокруг него. Адрес берётся из `TEST_POSTGRES_DSN`; без переменной тесты пропускаются, чтобы `go test ./...` оставался запускаемым без стенда.

```bash
docker run -d --rm --name auth-test-pg -e POSTGRES_PASSWORD=postgres -e POSTGRES_DB=authdb -p 55432:5432 postgres:16-alpine
go run ./cmd/migrator --dsn 'postgres://postgres:postgres@localhost:55432/authdb?sslmode=disable' --migrations-path ./migrations
TEST_POSTGRES_DSN='postgres://postgres:postgres@localhost:55432/authdb?sslmode=disable' go test -race ./internal/storage/postgres/
```

Пользователи создаются с уникальным суффиксом в `username` и `email` (оба `UNIQUE`) и удаляются через `t.Cleanup`, поэтому тесты можно гонять параллельно и на общей базе.

## Известные проблемы

* **Пул не масштабируется за 4 инстанса.** `max_open_conns: 25` умножается на число реплик, а `max_connections` у Postgres — 100. При большем числе инстансов нужен PgBouncer в transaction pooling; заодно придётся проверить, что pgx работает с ним в нужном режиме протокола.
* **Запросы не трассируются.** `Connect` отдаёт обычный `*sql.DB`, спанов на запросы нет — в Jaeger видно HTTP- и gRPC-уровень, но не время в базе.
* **`SELECT` в транзакции ротации без `FOR UPDATE`.** Сейчас корректность спасает проверка `RowsAffected`, но явная блокировка строки выражала бы намерение прямее.
* **Нет индекса под запрос логина.** `User()` ищет по `email = $1 OR username = $1`; оба поля `UNIQUE`, так что индексы есть, но планировщику на `OR` может понадобиться `BitmapOr` — стоит проверить `EXPLAIN` на реальном объёме.
* **Нет отдельного способа прочитать деактивированного пользователя.** Фильтр `is_active` зашит во все запросы, поэтому будущей админке (просмотр, реактивация) понадобится отдельный метод без него — дописывать условие в существующие нельзя.
