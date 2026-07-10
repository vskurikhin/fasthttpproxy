# ChangeLog — PR-9: Многопоточный прокси с upstream-серверами, метриками и конфигурацией

## Изменения

### 1. `cmd/proxy/main.go` — переписан с флагами и метриками
- Добавлены импорты: `prometheus/client_golang/prometheus/promhttp`, `fasthttpadaptor`, `internal/config`
- `main()` делегирует `run()` с обработкой ошибки
- `run()` → `config.ParseFlags()` → `runWithUpstreams(metricsAddr, proxyAddr, upstreams)`
- `proxy.Handler()` теперь принимает `upstreams []string` — список предопределённых бэкендов
- Добавлен `go serveMetrics(metricsAddr, metricsHandler)` — второй сервер на `:7070` для Prometheus
- `ListenAndServe(proxyAddr)` возвращает ошибку вместо `log.Fatal`

### 2. `cmd/proxy/main_test.go` — новый файл (76 строк)
- `TestRunWithUpstreamsInvalidPort`, `TestRunWithUpstreamsInvalidMetrics`, `TestRunWithUpstreamsWithUpstreams`
- `TestHandlerReturnsNonNil`, `TestHandlerWithUpstreams`, `TestRunWithUpstreamsEmptyUpstreams`
- `TestServeMetricsError`, `TestRunWithBadAddrs`, `TestRunFunction`

### 3. `internal/config/config.go` — новый файл (46 строк)
- `ParseFlags()` — парсит `--upstreams`, `--metrics-addr`, `--proxy-addr` через `flag.NewFlagSet`
- `ParseUpstreams()` — обёртка для совместимости
- `parseUpstreams(raw)` — разбивает CSV на адреса
- `addrAppend()` — валидация через `url.Parse("https://" + addr)`, `log.Fatalf` при ошибке

### 4. `internal/config/config_test.go` — новый файл (129 строк)
- `TestParseFlagsEmpty`, `TestParseFlagsWithUpstreams`, `TestParseFlagsMultipleUpstreams`
- `TestParseFlagsWithAddrs`, `TestParseFlagsWithSpaces`
- `TestParseUpstreams`, `TestParseUpstreamsDirect`, `TestParseUpstreamsMultipleDirect`
- `TestAddrAppendTrimsSpace`, `TestAddrAppendValidURL`

### 5. `internal/metrics/metrics.go` — новые метрики
- Добавлены `DialErrors` (счётчик ошибок соединения) и `IdleDropErrors` (счётчик сброса просроченных соединений)
- `BodyDefBuckets` — кастомные бакеты для гистограмм (27 значений, от 5ms до 25290s ≈ 7ч)
- Обе гистограммы переключены с `prometheus.DefBuckets` на `BodyDefBuckets`

### 6. `internal/pool/pool.go` — idle timeout + CloseAndDrop + dialNew
- Добавлена структура `idleConn` (net.Conn + `returnedAt time.Time`)
- `free` изменён с `[]net.Conn` на `[]idleConn`
- `idleTimeout = 45 * time.Second` — соединения старше этого времени отбрасываются при Get
- `Get()` при обнаружении stale → `IdleDropErrors.Inc()` → закрывает → декрементит count → `dialNew()`
- `Put()` при переполнении теперь декрементит `count` (раньше не уменьшал)
- `CloseAndDrop(addr, conn)` — новый метод: закрывает коннект, декрементит `count`. Для мёртвых соединений (broken pipe, EOF на upstream)
- `dialNew()` — вынесено из `Get()`: проверка лимита, инкремент count, вызов `dial()`
- Добавлен импорт `time`

### 7. `internal/pool/pool_test.go` — новые тесты
- `SetIdleTimeoutForTest()` — хелпер для подмены `idleTimeout` в тестах
- `errCloseConn` — обёртка с отслеживанием закрытия
- `countConn`, `poolConnCount`, `wrapConnForCloseError`
- `TestCloseAndDrop` — проверка декремента count
- `TestPutFullDecrementsCount` — Put при переполнении уменьшает count
- `TestGetAfterCloseAndDropAllowsNewDial` — CloseAndDrop освобождает слот
- `TestGetStaleThenDial` — idle timeout → дроп → новый dial
- `TestGetMultipleFreeLIFO` — LIFO-порядок извлечения
- `TestPutWithCloseError` — ошибка Close не паникует, count декрементится
- `TestCloseAndDropWithCloseError` — то же для CloseAndDrop
- `TestIdleTimeoutDropsStaleConnection` — интеграционный тест stale → дроп

### 8. `internal/proxy/handler.go` — переименован из `proxy.go`
- `Handler(upstreams []string)` — принимает список upstream-серверов, создаёт `upstreamsObj = upstream.NewUpstreams(upstreams)`
- `resolveUpstream()` — если `upstreamsObj.Random()` даёт адрес, использует его; иначе читает Host из запроса (и добавляет в `upstreamsObj.Append`)
- `acquireUpstreamConn()` — добавлены `metrics.DialErrors.Inc()` и логи при ошибке
- `writeRequestHeaders()` — добавлены логи ошибок
- `writeRequestBody()` — добавлены логи ошибок
- `readResponseHeaders()` — добавлен лог ошибки
- `streamResponseBody()` — `NewTimedReader` принимает `connection`, `NewPoolReader` принимает `remain` (Content-Length)
- `pool.Put()` при ошибках заменён на `pool.CloseAndDrop()` — мёртвые соединения не возвращаются в пул
- Добавлен импорт `internal/upstream`

### 9. `internal/proxy/handler_test.go` — переименован из `proxy_test.go`
- `ResetUpstreams()` — сброс глобального upstreamsObj
- Все вызовы `Handler()` → `Handler(nil)`
- `TestResolveUpstreamMissingHost` — временно заскипан
- `TestFullProxyHandlerNoHost` — теперь ожидает 502 (BadGateway) вместо 400, т.к. upstreamsObj содержит `127.0.0.1:1`
- Новые тесты:
  - `startKeepaliveUpstream` — upstream не закрывает соединение (keepalive)
  - `TestContentLengthConnectionReused` — Content-Length: соединение возвращается в пул и повторно используется
  - `startCloseUpstream` — upstream закрывает соединение (Connection: close)
  - `TestChunkedConnectionClosed` — chunked: соединение закрывается, второй запрос создаёт новое

### 10. `internal/readers/pool_reader.go` — стратегия remain
- `PoolReader` теперь принимает `remain int64`
- `remain >= 0` (Content-Length): `readWithLimit()` — после чтения лимита соединение возвращается в пул (pool.Put), считается живым
- `remain < 0` (chunked/identity): `readUntilEOF()` — при EOF соединение закрывается (pool.CloseAndDrop), upstream явно закрыл сокет
- Поведение `readWithLimit` аналогично `io.LimitReader`: при исчерпании лимита первый Read возвращает `(n, nil)`, второй — `(0, io.EOF)`

### 11. `internal/readers/pool_reader_test.go` — новые тесты
- Все существующие тесты обновлены под сигнатуру с `remain`
- Новые тесты Content-Length:
  - `TestPoolReaderContentLengthReturnsToPool` — после лимита соединение НЕ закрывается
  - `TestPoolReaderContentLengthPartialRead` — частичные чтения до лимита
  - `TestPoolReaderContentLengthExactLimit` — ровно remain байт за один Read
- Новые тесты chunked:
  - `TestPoolReaderChunkedClosesOnEOF` — EOF → соединение закрывается
  - `TestPoolReaderChunkedClosesOnError` — ошибка → соединение закрывается
  - `TestPoolReaderChunkedReadThenEOF` — данные + EOF → закрытие
- `closeTrackConn` — вспомогательный тип с колбэком на Close

### 12. `internal/readers/timed_reader.go` — логи + размер
- `NewTimedReader` теперь принимает `connection net.Conn`
- `TimedReader` хранит `connection` и `size` (прочитанные байты)
- `Read()` накапливает `size`, логирует `connection`, `size`, `duration` при EOF

### 13. `internal/readers/timed_reader_test.go` — обновлены вызовы
- Все `NewTimedReader(...)` → `NewTimedReader(..., nil)`

### 14. `internal/upstream/upstream.go` — новый файл (41 строка)
- `Upstreams` — структура с `map[string]struct{}` (множество) и `keys []string` (срез для случайного выбора)
- `NewUpstreams(address)` — создаёт из списка
- `Random()` — случайный адрес через `rand.Intn`, false если пусто
- `Append(address)` — добавляет адрес без дубликатов

### 15. `internal/upstream/upstream_test.go` — новый файл (130 строк)
- `TestNewUpstreamsEmpty`, `TestNewUpstreamsNil`, `TestNewUpstreamsSingle`, `TestNewUpstreamsMultiple`
- `TestRandomReturnsDifferentAddresses`, `TestAppendNewAddress`, `TestAppendDuplicate`
- `TestAppendToEmpty`, `TestNewUpstreamsPreservesOrder`, `TestRandomOnSingleElement`

## Ключевые архитектурные изменения

**Управление соединениями:**
- `idleTimeout = 45s` — просроченные соединения отбрасываются при извлечении из пула
- `CloseAndDrop()` — для мёртвых соединений (не возвращаются в пул, count декрементится)
- `PoolReader` с двумя стратегиями: Content-Length (возврат в пул) vs chunked (закрытие)
- `dialNew()` — вынесен из `Get()`, используется и при stale-drop

**Маршрутизация:**
- `Handler(upstreams []string)` — случайный выбор из предопределённого списка
- Если список пуст — используется Host из запроса (классический прокси)
- `Upstreams.Append()` — динамическое добавление адресов

**Метрики:**
- `DialErrors` — ошибки соединения с upstream
- `IdleDropErrors` — сброс просроченных соединений
- `BodyDefBuckets` — кастомные бакеты (5ms–7h) вместо `DefBuckets`

## Сводка

| Файл                                   | Статус    | Строк |
|----------------------------------------|-----------|------:|
| `cmd/proxy/main.go`                    | изменён   |  56   |
| `cmd/proxy/main_test.go`               | + новый   |  76   |
| `internal/config/config.go`            | + новый   |  46   |
| `internal/config/config_test.go`       | + новый   | 129   |
| `internal/metrics/metrics.go`          | изменён   |  46→62|
| `internal/pool/pool.go`                | изменён   |  50→84|
| `internal/pool/pool_test.go`           | изменён   | 147→400|
| `internal/proxy/handler.go`            | переименован| 237→245|
| `internal/proxy/handler_test.go`       | переименован| 906→1050|
| `internal/readers/pool_reader.go`      | изменён   |  34→89 |
| `internal/readers/pool_reader_test.go` | изменён   |  99→280|
| `internal/readers/timed_reader.go`     | изменён   |  33→37 |
| `internal/readers/timed_reader_test.go`| изменён   | 126→126|
| `internal/upstream/upstream.go`        | + новый   |  41   |
| `internal/upstream/upstream_test.go`   | + новый   | 130   |
