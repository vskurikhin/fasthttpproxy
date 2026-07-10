# ChangeLog — PR-6: Стриминг-прокси с метриками и fasthttpproxy

## Изменения

### 1. `cmd/http/main.go` — заглушка (новый файл)
- Пустой `package main` с `func main() {}`
- Начальная заготовка для будущего HTTP-клиента

### 2. `cmd/proxy/main.go` — переписан
- Удалён `proxyHandler` (80 строк HostClient + DoDeadline)
- Добавлен `pool.SetDial(fasthttpproxy.FasthttpHTTPDialerDualStackTimeout("", 30*time.Second))`
- Handler заменён на `proxy.Handler()` из `internal/proxy`
- Импорты: добавлены `internal/pool`, `internal/proxy`, удалён `strings`

### 3. `cmd/proxy_metrics/main.go` — новая точка входа с метриками (новый файл)
- Запускает два сервера: `:7070` (Prometheus `/metrics`) и `:8080` (прокси)
- Использует `fasthttpadaptor.NewFastHTTPHandler(promhttp.Handler())` для метрик
- Использует `proxy.Handler()` из `internal/proxy`
- Импорт `prometheus/client_golang/prometheus/promhttp`

### 4. `cmd/proxy_metrics/main_test.go` — тест (новый файл)
- Проверяет, что `proxy.Handler()` не nil

### 5. `go.mod` — обновление зависимостей
- Добавлен `github.com/prometheus/client_golang v1.23.2` (прямая)
- Добавлены косвенные: `beorn7/perks`, `cespare/xxhash`, `kr/text`, `munnerz/goautoneg`,
  `prometheus/client_model`, `prometheus/common`, `prometheus/procfs`, `google/protobuf`
- Обновлены: `brotli` (v1.2.1→v1.2.2), `compress` (v1.18.6→v1.19.0),
  `crypto` (v0.53.0→v0.54.0), `net` (v0.56.0→v0.57.0), `sys` (v0.46.0→v0.47.0),
  `text` (v0.38.0→v0.40.0)
- Добавлены косвенные: `creack/pty`, `davecgh/go-spew`, `google/go-cmp`, `kr/pretty`,
  `kylelemons/godebug`, `pmezard/go-difflib`, `rogpeppe/go-internal`,
  `stretchr/testify`, `go.uber.org/goleak`, `go.yaml.in/yaml`, `google/protobuf`,
  `gopkg.in/check.v1`, `gopkg.in/yaml.v3`

### 6. `go.sum` — обновление хешей зависимостей
- Обновлены все хеши под новые версии + новые записи

### 7. `internal/metrics/metrics.go` — Prometheus-метрики (новый файл, 46 строк)
- `BufIOWriterFlushErrors` — ошибки flush bufio.Writer
- `ReadErrors` — ошибки чтения upstream
- `WriteErrors` — ошибки записи upstream
- `CloseErrors` — ошибки закрытия соединений
- `Upstream5xx` — upstream 5xx → 502
- `RequestBodyWriteDuration` — гистограмма записи тела запроса (DefBuckets)
- `ResponseBodyReadDuration` — гистограмма чтения тела ответа (DefBuckets)
- Все через `promauto.NewCounter`/`NewHistogram`

### 8. `internal/pool/pool.go` — пул соединений (новый файл, 95 строк)
- LIFO-пул на 100 соединений на upstream-адрес (`sync.Map` → `connPool`)
- `SetDial(dial func)` — установка кастомной диал-функции (fasthttpproxy)
- `dial(addr)` — вызов `customDial` или `fasthttp.Dial`
- `Get(addr)` — получение/создание соединения с лимитом `maxUpstreamConnsPerHost`
- `Put(addr, conn)` — возврат в пул, закрытие при переполнении
- Счётчик `count` + лимит через `atomic.LoadInt32`

### 9. `internal/pool/pool_test.go` — тесты пула (новый файл, 147 строк)
- `TestGetAndPut` — Get после Put возвращает то же соединение
- `TestGetLimit` — превышение лимита → `ErrDialTimeout`
- `TestPutClosesWhenFull` — лишние соединения закрываются
- `TestGetConcurrent` — 50 горутин, пул не превышает лимит
- `testDialer` — локальный TCP-сервер для тестов

### 10. `internal/proxy/proxy.go` — стриминг-прокси handler (новый файл, 237 строк)
- `Handler() fasthttp.RequestHandler` — замыкание на `handler.handle()`
- 7 методов по SRP:
  - `resolveUpstream` — Host → `upstreamAddr` (400 если пусто)
  - `acquireUpstreamConn` — `pool.Get(upstreamAddr)` (502 если ошибка)
  - `writeRequestHeaders` — `req.Header.Write(bw)` + `bw.Flush()`
  - `writeRequestBody` — стрим (PipeCopy) или фиксированное тело с метрикой `RequestBodyWriteDuration`
  - `readResponseHeaders` — `respHeader.Read(br)`
  - `copyResponseStatus` — 5xx → 502, иначе копирование
  - `streamResponseBody` — `SetBodyStream` с `ImmediateHeaderFlush` + `timedReader`
- `timedReader` — обёртка io.Reader с гистограммой `ResponseBodyReadDuration`
- `PipeCopy` — копирование 64KB буфером

### 11. `internal/proxy/proxy_test.go` — тесты прокси (новый файл, 906 строк)
- Модульные тесты каждого метода: resolveUpstream, acquireUpstreamConn,
  writeRequestHeaders (success, header error, flush error),
  writeRequestBody (stream success, stream error, empty, fixed success,
  fixed write error, fixed short write),
  readResponseHeaders (success, error),
  copyResponseStatus (<500, 500+, 503, nil header),
  streamResponseBody (content-length, chunked)
- Интеграционные: `TestFullProxyHandler`, `TestFullProxyHandlerUpstreamError`,
  `TestFullProxyHandlerNoHost`
- Handler-тесты: `TestHandlerFullCycle`, `TestHandlerFullCycleWithBody`,
  `TestHandlerFullCycleUpstream5xx`, `TestHandlerFullCycleNoHost`,
  `TestHandlerFullCycleDialError`
- Вспомогательные: `mockConn`, `errWriter`, `shortWriter`, `writerConn`, `errReader`

## Сводка

| Файл                    | Статус    | Строк |
|-------------------------|-----------|------:|
| `cmd/http/main.go`      | + новый   |   4   |
| `cmd/proxy/main.go`     | изменён   |  25   |
| `cmd/proxy_metrics/main.go` | + новый |  23   |
| `cmd/proxy_metrics/main_test.go` | + новый | 14 |
| `go.mod`                | изменён   |  27   |
| `go.sum`                | изменён   |  ~70  |
| `internal/metrics/metrics.go` | + новый | 46 |
| `internal/pool/pool.go` | + новый   |  95   |
| `internal/pool/pool_test.go` | + новый | 147 |
| `internal/proxy/proxy.go` | + новый | 237 |
| `internal/proxy/proxy_test.go` | + новый | 906 |
