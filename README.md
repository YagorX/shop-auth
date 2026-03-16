# shop-auth

`shop-auth` — сервис аутентификации и авторизации для mini-shop.

Код сервиса расположен в подпроекте `sso/`. Именно он собирается в Docker и подключается в общий `shop-platform`.

## Что реализовано

1. gRPC API для auth flow:
   - `Register`
   - `Login`
   - `ValidateToken`
   - `Refresh`
   - `Logout`
   - `IsAdmin`
2. HTTP operational endpoints:
   - `GET /health`
   - `GET /ready`
   - `GET /metrics`
   - `GET/POST /admin/log-level`
3. JWT access tokens.
4. `bcrypt` hash для паролей.
5. `sha256` hash для refresh token storage.
6. PostgreSQL storage и refresh session rotation.
7. gRPC server TLS с обязательной проверкой client certificate в Docker-окружении.
8. JSON logs, Prometheus metrics и OpenTelemetry traces.

## Security model

1. Пароль никогда не хранится в открытом виде.
2. Access token формируется как JWT.
3. Refresh token не хранится как raw value: в БД сохраняется только hash.
4. `shop-gateway` подключается к `shop-auth` по gRPC mTLS.
5. Сервер `shop-auth` проверяет client certificate gateway.

## Operational model

1. HTTP порт `8082` используется для `/health`, `/ready`, `/metrics`.
2. gRPC порт `44044` используется для бизнес-методов auth.
3. Readiness зависит от готовности PostgreSQL и внутренних зависимостей сервиса.

## Конфигурация

Основные файлы:

1. `sso/config/local.yaml`
2. `sso/config/config.docker.yaml`

Ключевые группы настроек:

1. `grpc.*`
2. `http.*`
3. `postgres.*`
4. `token_ttl`
5. `refresh_ttl`
6. `tls.*`
7. `otlp.*`

В Docker-конфигурации включены:

1. TLS на gRPC server
2. обязательный client certificate
3. OTLP экспорт в Jaeger

## Запуск

Локально:

```bash
cd sso
go run ./cmd/sso --config config/local.yaml
```

Через общий compose:

```bash
cd ../shop-platform/deploy
docker compose up -d --build auth-postgres auth-migrate auth-service
```

## Проверка

HTTP:

```bash
curl http://localhost:8082/health
curl http://localhost:8082/ready
curl http://localhost:8082/metrics
```

В составе платформы auth flow обычно проверяется через `shop-gateway`, а не прямым внешним вызовом к gRPC порту.

## Что показывает этот сервис

Этот сервис демонстрирует не только auth-бизнес-логику, но и нормальную эксплуатационную зрелость:

1. безопасное хранение credential-related данных;
2. refresh token rotation;
3. mTLS-ready внутренний API;
4. полноценные readiness/metrics/logs/traces;
5. интеграцию в общую платформу через `shop-gateway`.
