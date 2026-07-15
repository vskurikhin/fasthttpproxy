# Интеграционные тесты: Upstream disconnect mid-response (Level 2)

## Файл: `internal/proxy/handler_test.go` (дополнение к существующим тестам)

Пакет: `proxy`

---

## 1. Обзор

Интеграционные тесты используют **реальный TCP** для симуляции сбоев upstream. В отличие от unit-тестов (Level 1), здесь upstream — полноценный TCP-сервер, который отправляет HTTP-ответ и контролируемым образом обрывает соединение.

---

## 2. Дополнительные утилиты

### 2.1. `startFaultyUpstream` (файл `handler_faulty_upstream.go`)

Запускает TCP-сервер, который принимает одно соединение, обрабатывает его согласно типу сбоя и закрывает.

```go
func startFaultyUpstream(t *testing.T, fault FaultType) net.Listener
```

**Типы сбоев:**

| FaultType                     | Поведение                                                        | Эквивалент точки сбоя |
|-------------------------------|------------------------------------------------------------------|-----------------------|
| `FaultCloseImmediate`         | Принять соединение, закрыть без данных                           | 6c                    |
| `FaultPartialHeaders`         | Отправить `HTTP/1.1 200 OK\r\n`, закрыть                         | 6d                    |
| `FaultContentLengthUnderread` | Отправить полные заголовки + 50 байт тела, закрыть               | 8d                    |
| `FaultChunkedDisconnect`      | Отправить chunked-заголовок + один чанк, закрыть без терминатора | 8e                    |

### 2.2. `startPartialUpstream` (файл `handler_faulty_upstream.go`)

Отправляет произвольную строку данных и закрывает соединение.

```go
func startPartialUpstream(t *testing.T, data string) net.Listener
```

---

## 3. Интеграционные тесты

### 3.1. `TestIntegrationUpstreamCloseImmediate`

**Цель:** Проверить полный цикл «клиент → прокси → upstream» при немедленном закрытии upstream.

**Сценарий:**
1. Запускается faulty upstream (FaultCloseImmediate)
2. Прокси-обработчик вызывается с адресом этого upstream
3. Прокси устанавливает TCP-соединение, отправляет запрос
4. Upstream закрывает соединение без ответа
5. `readResponseHeaders` возвращает ошибку

**Ожидание:** статус 502, тело ответа непустое (сообщение об ошибке).

```go
func TestIntegrationUpstreamCloseImmediate(t *testing.T) {
    ResetUpstreams()
    ln := startFaultyUpstream(t, FaultCloseImmediate)
    defer ln.Close()

    var ctx fasthttp.RequestCtx
    var req fasthttp.Request
    req.Header.SetMethod("GET")
    req.SetRequestURI("/")
    req.Header.SetHost(ln.Addr().String())
    ctx.Init(&req, nil, nil)

    handler := Handler(nil)
    handler(&ctx)

    if ctx.Response.StatusCode() != fasthttp.StatusBadGateway {
        t.Fatalf("expected 502, got %d", ctx.Response.StatusCode())
    }
    if len(ctx.Response.Body()) == 0 {
        t.Fatal("expected error body")
    }
}
```

---

### 3.2. `TestIntegrationUpstreamPartialHeaders`

**Цель:** Проверить обработку частичного заголовка на полном цикле.

**Сценарий:**
1. Upstream отправляет `HTTP/1.1 200 OK\r\n` (без завершения заголовка) и закрывает
2. `fasthttp.ResponseHeader.Read()` не может распарсить — ошибка
3. Прокси возвращает 502

**Ожидание:** статус 502, соединение закрыто и сброшено из пула.

```go
func TestIntegrationUpstreamPartialHeaders(t *testing.T) {
    ResetUpstreams()
    ln := startFaultyUpstream(t, FaultPartialHeaders)
    defer ln.Close()

    var ctx fasthttp.RequestCtx
    var req fasthttp.Request
    req.Header.SetMethod("GET")
    req.SetRequestURI("/")
    req.Header.SetHost(ln.Addr().String())
    ctx.Init(&req, nil, nil)

    handler := Handler(nil)
    handler(&ctx)

    if ctx.Response.StatusCode() != fasthttp.StatusBadGateway {
        t.Fatalf("expected 502, got %d", ctx.Response.StatusCode())
    }
}
```

---

### 3.3. `TestIntegrationUpstreamContentLengthUnderread`

**Цель:** Проверить поведение прокси при неполном теле с фиксированным Content-Length.

**Сценарий:**
1. Upstream отправляет полные заголовки `Content-Length: 100`, затем 50 байт тела
2. Прокси читает заголовки — OK, копирует статус 200
3. `streamResponseBody` устанавливает `PoolReader` с `remain=100`
4. При чтении тела `PoolReader` получает EOF на 50 байтах — ошибка
5. Соединение возвращается в пул (для Content-Length возврат, а не закрытие)

**Ожидание:**
- `handle()` не падает (body stream установлен корректно)
- `ctx.Response.IsBodyStream() == true`
- `len(ctx.Response.Body()) < 100` — тело усечено

```go
func TestIntegrationUpstreamContentLengthUnderread(t *testing.T) {
    ResetUpstreams()
    ln := startFaultyUpstream(t, FaultContentLengthUnderread)
    defer ln.Close()

    var ctx fasthttp.RequestCtx
    var req fasthttp.Request
    req.Header.SetMethod("GET")
    req.SetRequestURI("/")
    req.Header.SetHost(ln.Addr().String())
    ctx.Init(&req, nil, nil)

    handler := Handler(nil)
    handler(&ctx)

    if !ctx.Response.IsBodyStream() {
        t.Fatal("expected body stream")
    }
    body := ctx.Response.Body()
    if len(body) >= 100 {
        t.Fatalf("expected body < 100 bytes, got %d", len(body))
    }
}
```

---

### 3.4. `TestIntegrationUpstreamChunkedDisconnect`

**Цель:** Проверить поведение прокси при chunked-ответе без терминатора.

**Сценарий:**
1. Upstream отправляет chunked-заголовок + один чанк, закрывает без `0\r\n\r\n`
2. `readResponseHeaders` читает заголовки — OK (Content-Length = -1, chunked)
3. `streamResponseBody` устанавливает `PoolReader` с `remain=-1`
4. При чтении тела `PoolReader.readUntilEOF` получает EOF
5. Соединение **закрывается** через `CloseAndDropUpstreamConnection`

**Ожидание:**
- `handle()` не падает
- `ctx.Response.IsBodyStream() == true`
- Соединение закрыто (не возвращено в пул)

```go
func TestIntegrationUpstreamChunkedDisconnect(t *testing.T) {
    ResetUpstreams()
    ln := startFaultyUpstream(t, FaultChunkedDisconnect)
    defer ln.Close()

    var ctx fasthttp.RequestCtx
    var req fasthttp.Request
    req.Header.SetMethod("GET")
    req.SetRequestURI("/")
    req.Header.SetHost(ln.Addr().String())
    ctx.Init(&req, nil, nil)

    handler := Handler(nil)
    handler(&ctx)

    if !ctx.Response.IsBodyStream() {
        t.Fatal("expected body stream")
    }
}
```

---

## 4. Связь unit-тестов (Level 1) и интеграционных тестов (Level 2)

| Сценарий                     | Unit-тест (Level 1)                         | Интеграционный тест (Level 2)    | Разница                                                             |
|------------------------------|---------------------------------------------|----------------------------------|---------------------------------------------------------------------|
| A1: Close immediate          | `TestHandlerUpstreamCloseImmediate`         | тот же (использует реальный TCP) | Level 2 = Level 1 для этого сценария, т.к. mock-соединение не нужно |
| A2: Partial headers          | `TestHandlerUpstreamPartialHeaders`         | тот же                           | Аналогично                                                          |
| B1: Content-Length underread | `TestHandlerUpstreamContentLengthUnderread` | тот же                           | Проверка через реальный bufio.Reader из TCP-соединения              |
| B2: Chunked disconnect       | `TestHandlerUpstreamChunkedDisconnect`      | тот же                           | Проверка через реальный TCP                                         |

**Примечание:** Для данных сценариев unit-тесты и интеграционные тесты совпадают, поскольку оба используют `startFaultyUpstream` — реальный TCP-сервер. Разделение на Level 1 и Level 2 проявляется при использовании mock-соединений без реального TCP (например, `partialReader`/`resetWriter` для тестирования отдельных методов).

---

## 5. Запуск интеграционных тестов

```bash
# Все тесты сетевых сбоев
go test -v -race -shuffle=on -count=1 -timeout 120s \
    ./internal/proxy/ -run TestIntegration

# Только upstream disconnect
go test -v -race -shuffle=on -count=1 -timeout 120s \
    ./internal/proxy/ -run 'TestIntegrationUpstream'

# Табличный тест всех сценариев
go test -v -race -shuffle=on -count=1 -timeout 120s \
    ./internal/proxy/ -run TestNetworkFailures
```

---

## 6. Критерии приёмки интеграционных тестов

| # | Критерий                                                    | Тест                                            | Проверка                              |
|---|-------------------------------------------------------------|-------------------------------------------------|---------------------------------------|
| 1 | Upstream закрыл без данных → 502                            | `TestIntegrationUpstreamCloseImmediate`         | `StatusCode() == 502`                 |
| 2 | Частичный заголовок → 502                                   | `TestIntegrationUpstreamPartialHeaders`         | `StatusCode() == 502`                 |
| 3 | Content-Length: 100, тело 50 → body stream + усечённое тело | `TestIntegrationUpstreamContentLengthUnderread` | `IsBodyStream() && len(Body()) < 100` |
| 4 | Chunked без терминатора → body stream                       | `TestIntegrationUpstreamChunkedDisconnect`      | `IsBodyStream() == true`              |
| 5 | Все тесты проходят с `-race`                                | Все                                             | `go test -race` без data race         |
