# internal/observability

Три сигнала телеметрии: логи, метрики, трейсы. Пакет держит их вместе, потому что все три настраиваются одними и теми же параметрами (`service`, `env`, `version`) и должны быть согласованы между собой.

```text
observability/
├── logger.go    # slog JSON + смена уровня на живом процессе
├── metrics.go   # Prometheus-коллекторы и хелперы наблюдения
└── tracing.go   # OTLP-экспортёр и gRPC stats handler
```

## Логи

`NewLogger` возвращает `RuntimeLogger` — `slog.Logger` с JSON-хендлером в stdout и общими полями `service`, `env`, `version` в каждой записи.

Ключевая деталь — уровень хранится в `*slog.LevelVar`:

```go
levelVar := &slog.LevelVar{}
handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: levelVar})
```

`LevelVar` читается атомарно при каждой записи, поэтому `SetLevel("debug")` из HTTP-хендлера действует сразу и во всех горутинах. Именно это делает возможным `/admin/log-level` без перезапуска.

`RuntimeLogger` реализует интерфейс `LogLevelController` из `transport/http/v1/contracts` — транспорт получает возможность менять уровень, не зная про slog.

Почему stdout, а не файл: логи забирает Filebeat из docker-логов контейнера (см. [`shop-platform`](../../../shop-platform)), дальше Kafka → Logstash → Elasticsearch. Приложение не должно знать о ротации файлов.

## Метрики

Инициализация через `MustMetrics()` — синглтон с `sync.Once`. Причина: `prometheus.MustRegister` паникует при повторной регистрации коллектора с тем же именем, а метрики нужны в разных пакетах, включая те, что создаются не единожды.

| Метрика | Тип | Метки |
|---|---|---|
| `auth_service_requests_total` | Counter | `method`, `status` |
| `auth_service_request_duration_seconds` | Histogram | `method` |
| `auth_http_requests_total` | Counter | `method`, `path`, `status` |
| `auth_http_request_duration_seconds` | Histogram | `method`, `path` |
| `auth_grpc_requests_total` | Counter | `method`, `code` |
| `auth_grpc_request_duration_seconds` | Histogram | `method` |

Три уровня наблюдения дают возможность разделять причины: рост latency в `grpc` при спокойном `service` означает проблему в транспорте или сети, а одновременный рост обоих — в логике или базе.

Хелперы:

```go
ObserveServiceRequest(op string, started time.Time, err error)  // status = ok|error
ObserveHTTPRequest(method, path string, status int, started time.Time)
ObserveGRPCRequest(method, code string, started time.Time)
```

Гистограммы используют `prometheus.DefBuckets` — стандартную сетку от 5мс до 10с.

## Трейсы

`InitTracing` настраивает OTLP-экспортёр по gRPC и возвращает функцию остановки:

* **resource** — `service.name`, `service.version`, `deployment.environment` по семантическим соглашениям OTel 1.26, по ним Jaeger группирует спаны;
* **batcher** с таймаутом 2 секунды — спаны уходят пачками, а не по одному;
* **propagator** — `TraceContext` (W3C `traceparent`), тот же формат, что требуют event-контракты в [`shop-contracts/events`](../../../shop-contracts/events);
* **`WithInsecure()`** — внутри compose трафик до Jaeger не шифруется.

Возвращаемый `tp.Shutdown` обязателен к вызову при остановке: батчер держит неотправленные спаны в памяти, без флаша теряется хвост трейсов перед выключением.

`GRPCServerStatsHandler()` отдаёт `otelgrpc.NewServerHandler()` — серверные спаны для всех gRPC-методов создаются автоматически, вручную инструментировать хендлеры не нужно.

## Деградация

Трейсинг — единственный необязательный сигнал. Если коллектор недоступен, `InitTracing` вернёт ошибку, `internal/app` запишет warning и продолжит запуск с `shutdownTracing = nil`. Логи и метрики работают локально и отказать не могут.

## Известные проблемы

* **Нет метрик бизнес-уровня.** Есть счётчики вызовов, но нет, например, `auth_login_failures_total` с причиной отказа или счётчика активных refresh-сессий — а именно они нужны для алертов на подбор пароля.
* **Нет sampler-политики.** `NewTracerProvider` создаётся без явного `WithSampler`, то есть используется `ParentBased(AlwaysSample)`. Под нагрузкой это отправит в Jaeger все спаны без исключения.
* **`SetDefaultLogger` перенаправляет стандартный `log`** в slog на уровне Info. В `main.go` фатальные ошибки старта пишутся через `slog.Error`, но любая зависимость, использующая стандартный `log`, окажется в логах с `"level":"INFO"`.
