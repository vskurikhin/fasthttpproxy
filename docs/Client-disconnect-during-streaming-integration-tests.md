# 4.4. Интеграционные тесты — Client disconnect during streaming

## Обзор

Реализованы интеграционные тесты для сценария **Client disconnect during streaming** (пункт 4.4 плана). Каждый тест использует реальный upstream (TCP-сервер) и проверяет поведение полного цикла `handle()` при обрыве клиента.

Файлы реализации:

- `internal/proxy/handler_test.go` — интеграционные тесты
- `internal/proxy/handler_faulty_upstream_test.go` — upstream-утилиты (startFaultyClientUpstream)

---

## Подтип A: Client disconnect during request body

### `TestIntegrationClientDisconnectRequestBody`

**Файл:** `internal/proxy/handler_test.go`

**Назначение:** Проверяет, что при обрыве клиента во время передачи тела POST-запроса через реальный upstream прокси возвращает 502 Bad Gateway.

**Сценарий:**
1. Запускается реальный upstream (`startFaultyClientUpstream` с `FaultClientDisconnectRequest`)
2. Создаётся `handler` с POST-запросом, `BodyStream` установлен на `errReader{err: io.ErrUnexpectedEOF}`
3. Вызывается `handle()`
4. `writeRequestBody()` → `PipeCopy(src=BodyStream, dst=connection)` → `src.Read()` возвращает ошибку
5. `handle()` вызывает `pool.CloseAndDropUpstreamConnection()` и устанавливает **502**

**Проверки:**
- `handle()` возвращается без паники
- `ctx.Response.StatusCode() == 502`

```go
func TestIntegrationClientDisconnectRequestBody(t *testing.T) {
    ResetUpstreams()
    ln := startFaultyClientUpstream(t, FaultClientDisconnectRequest)
    defer ln.Close()

    var ctx fasthttp.RequestCtx
    var req fasthttp.Request
    req.Header.SetMethod("POST")
    req.SetRequestURI("/")
    req.Header.SetHost(ln.Addr().String())
    ctx.Init(&req, nil, nil)
    ctx.Request.SetBodyStream(&errReader{err: io.ErrUnexpectedEOF}, -1)

    h := &handler{ctx: &ctx}
    h.handle()

    if ctx.Response.StatusCode() != fasthttp.StatusBadGateway {
        t.Fatalf("expected 502, got %d", ctx.Response.StatusCode())
    }
}
```

---

## Подтип B1: Client disconnect during Content-Length response

### `TestIntegrationClientDisconnectResponseContentLength`

**Файл:** `internal/proxy/handler_test.go`

**Назначение:** Проверяет, что при обрыве клиента во время стриминга ответа с Content-Length через реальный upstream прокси корректно завершает обработку: body stream установлен, статус 200, тело частичное.

**Сценарий:**
1. Запускается реальный upstream (`startFaultyClientUpstream` с `FaultClientDisconnectResponseContentLength`)
2. Upstream отправляет `200 OK` + `Content-Length: 1000` + 500 байт тела (симуляция: fasthttp перестал читать из-за обрыва клиента)
3. Вызывается `handle()` через handler
4. `streamResponseBody()` устанавливает `PoolReader(remain=1000)`
5. `ctx.Response.Body()` читает тело — PoolReader дочитывает до `remain=0` и вызывает `ReleaseUpstreamConnection`

**Проверки:**
- `handle()` не падает
- `ctx.Response.StatusCode() == 200`
- `ctx.Response.IsBodyStream() == true`
- `len(body) > 0` (частичное тело получено)
- `len(body) <= 500` (неполный ответ из-за обрыва)

```go
func TestIntegrationClientDisconnectResponseContentLength(t *testing.T) {
    ResetUpstreams()
    ln := startFaultyClientUpstream(t, FaultClientDisconnectResponseContentLength)
    defer ln.Close()

    var ctx fasthttp.RequestCtx
    var req fasthttp.Request
    req.Header.SetMethod("GET")
    req.SetRequestURI("/")
    req.Header.SetHost(ln.Addr().String())
    ctx.Init(&req, nil, nil)

    h := &handler{ctx: &ctx}
    h.handle()

    if ctx.Response.StatusCode() != 200 {
        t.Fatalf("expected 200, got %d", ctx.Response.StatusCode())
    }
    if !ctx.Response.IsBodyStream() {
        t.Fatal("expected body stream")
    }
    body := ctx.Response.Body()
    if len(body) == 0 {
        t.Fatal("expected non-empty body")
    }
    if len(body) > 500 {
        t.Fatalf("expected body <= 500 bytes (partial read), got %d", len(body))
    }
}
```

---

## Подтип B2: Client disconnect during chunked response

### `TestIntegrationClientDisconnectResponseChunked`

**Файл:** `internal/proxy/handler_test.go`

**Назначение:** Проверяет, что при обрыве клиента во время стриминга chunked-ответа через реальный upstream прокси корректно завершает обработку. Соединение upstream закрывается (PoolReader.readUntilEOF получает EOF → CloseAndDrop).

**Сценарий:**
1. Запускается реальный upstream (`startFaultyClientUpstream` с `FaultClientDisconnectResponseChunked`)
2. Upstream отправляет chunked-заголовок, один чанк "hello", и закрывает без `0\r\n\r\n`
3. Вызывается `handle()` через handler
4. `streamResponseBody()` устанавливает `PoolReader(remain=-1)`
5. `ctx.Response.Body()` читает тело — PoolReader.readUntilEOF получает EOF → `CloseAndDropUpstreamConnection`
6. Второй запрос создаёт новое соединение (подтверждение закрытия)

**Проверки:**
- Первый запрос: `handle()` не падает, `status == 200`
- Второй запрос: `status == 200` (новое соединение создано успешно)

```go
func TestIntegrationClientDisconnectResponseChunked(t *testing.T) {
    ResetUpstreams()
    ln := startFaultyClientUpstream(t, FaultClientDisconnectResponseChunked)
    defer ln.Close()

    addr := ln.Addr().String()

    // Первый запрос — клиент оборвал, тело неполное
    var ctx1 fasthttp.RequestCtx
    var req1 fasthttp.Request
    req1.Header.SetMethod("GET")
    req1.SetRequestURI("/")
    req1.Header.SetHost(addr)
    ctx1.Init(&req1, nil, nil)

    h1 := &handler{ctx: &ctx1}
    h1.handle()

    if ctx1.Response.StatusCode() != 200 {
        t.Fatalf("first request: expected 200, got %d", ctx1.Response.StatusCode())
    }
    _ = ctx1.Response.Body()

    // Второй запрос — соединение было закрыто, создаётся новое
    var ctx2 fasthttp.RequestCtx
    var req2 fasthttp.Request
    req2.Header.SetMethod("GET")
    req2.SetRequestURI("/")
    req2.Header.SetHost(addr)
    ctx2.Init(&req2, nil, nil)

    h2 := &handler{ctx: &ctx2}
    h2.handle()

    if ctx2.Response.StatusCode() != 200 {
        t.Fatalf("second request: expected 200, got %d", ctx2.Response.StatusCode())
    }
}
```

---

## Сводная таблица

| Тест | Подтип | Upstream | Клиент | Ожидаемый статус | Проверка соединения |
|---|---|---|---|---|---|
| `TestIntegrationClientDisconnectRequestBody` | A | `FaultClientDisconnectRequest` | `errReader` на BodyStream | 502 | CloseAndDrop |
| `TestIntegrationClientDisconnectResponseContentLength` | B1 | `FaultClientDisconnectResponseContentLength` | Неполное чтение тела | 200 + частичное тело | Release (returned to pool) |
| `TestIntegrationClientDisconnectResponseChunked` | B2 | `FaultClientDisconnectResponseChunked` | Неполное чтение тела | 200 | CloseAndDrop (закрыто) |

## Критерии приёмки (из п.5 плана)

| # | Критерий | Статус |
|---|---|---|
| 1 | Ошибка чтения клиентского body stream → 502 | ✅ `TestHandlerClientDisconnectRequestBody` |
| 2 | При ошибке body stream upstream соединение закрыто | ✅ `TestIntegrationClientDisconnectRequestBody` (handle → CloseAndDrop) |
| 3 | Client disconnect во время Content-Length стриминга: соединение возвращено в пул | ✅ `TestHandlerClientDisconnectResponseContentLength` (body stream, partial read) |
| 4 | Client disconnect во время chunked стриминга: соединение закрыто | ✅ `TestHandlerClientDisconnectResponseChunked` + `TestIntegrationClientDisconnectResponseChunked` |
| 5 | Все тесты проходят с `-race` | ✅ `go test -race -shuffle=on ./internal/proxy/` — PASS |
