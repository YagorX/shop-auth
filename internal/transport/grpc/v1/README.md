# internal/transport/grpc/v1

Транспортный слой gRPC. Отвечает за три вещи и ни за что больше: валидацию входа, вызов сервисного слоя, перевод доменных ошибок в gRPC-коды.

Бизнес-правил здесь нет — при чтении методов это видно по структуре: проверка, вызов, маппинг.

## Регистрация

```go
const ServiceName = "proto.auth.v1.AuthService"

func Register(gRPC *grpc.Server, auth Auth)
```

`ServiceName` экспортируется, потому что его использует health-сервер в [`app/grpc`](../../../app/grpc) для выставления статуса, а `shop-gateway` — как имя сервиса в health-проверке при readiness.

`Auth` — интерфейс, который транспорт ожидает от сервисного слоя. Зависимость направлена от транспорта к логике, не наоборот.

## Маппинг ошибок

Единственное место, где доменные ошибки превращаются в коды протокола:

| Доменная ошибка | gRPC code | Сообщение клиенту |
|---|---|---|
| `ErrUserExist` | `AlreadyExists` | `user already exists` |
| `ErrInvalidCredentials` | `Unauthenticated` | `invalid email or password` |
| `ErrUserNotFound` | `NotFound` | `user not found` |
| `ErrAppNotFound` | `NotFound` | `app not found` |
| `ErrInvalidToken` | `Unauthenticated` | `invalid token` |
| `ErrRefreshSessionNotFound` | `Unauthenticated` | `invalid refresh token` |
| `ErrOverloaded` (Login, Register) | `ResourceExhausted` + `RetryInfo` 1 с | `server is busy, retry later` |
| всё прочее | `Internal` | `internal error` |

Наружу не уходит текст исходной ошибки — только фиксированная строка. Детали остаются в логах, клиент не получает информации о внутреннем устройстве.

Неверный пароль — `Unauthenticated`, а не `InvalidArgument`: запрос корректен, не подтвердилась личность. Gateway переводит этот код в HTTP 401, как обещает OpenAPI; `InvalidArgument` (HTTP 400) остаётся за формально кривыми запросами.

`ResourceExhausted` приходит, когда семафор на bcrypt не дал слот за `password_hasher.max_wait`. В gRPC нет заголовка Retry-After, поэтому пауза передаётся стандартной деталью статуса `google.rpc.RetryInfo`; gateway перекладывает её в HTTP `Retry-After`.

## Валидация

Вынесена в отдельные функции `validateRegister`, `validateLogin` и т.д. Проверяется только формальная корректность запроса:

* обязательные строки непусты;
* `user_uuid` парсится как UUID;
* `app_id` присутствует.

Всё, что требует обращения к данным (существует ли пользователь, подходит ли пароль), — задача сервисного слоя.

## Метрики

Каждый unary-вызов учитывается интерцептором из [`app/grpc`](../../../app/grpc), а не самими хендлерами:

```text
auth_grpc_requests_total{method="/proto.auth.v1.AuthService/Login", code="OK"}
auth_grpc_request_duration_seconds{method="/proto.auth.v1.AuthService/Login"}
```

Метка `code` берётся через `status.Code(err).String()`, поэтому в метриках видно распределение по кодам, а не только успех/ошибка.

## Тесты

[`server_test.go`](server_test.go) проверяет именно транспортные обязанности: что невалидный запрос отсекается до вызова сервиса и что каждая доменная ошибка даёт ожидаемый код. Сервисный слой при этом подменяется тестовой реализацией интерфейса `Auth`.
