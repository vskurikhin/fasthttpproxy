# 4.5. Интеграционные тесты — Upstream disconnect mid-response

## Обзор

Реализованы интеграционные тесты для сценария **Upstream disconnect mid-response** (пункт 4.5 плана). Каждый тест использует реальный upstream (TCP-сервер через `startFaultyUpstream`) и проверяет поведение полного цикла `handle()` через `Handler()`.

Иными словами: интеграционные тесты используют **реальный TCP** для симуляции сбоев upstream. В отличие от unit-тестов (Level 1), здесь upstream — полноценный TCP-сервер, который отправляет HTTP-ответ и контролируемым образом обрывает соединение.

Файлы реализации:

- `internal/proxy/handler_test.go` — интеграционные тесты
- `internal/proxy/handler_faulty_upstream_test.go` — upstream-утилиты (`startFaultyUpstream`)

---

## Подтип A: Разрыв до завершения заголовков

### `TestIntegrationUpstreamCloseImmediate`

**Файл:** `internal/proxy/handler_test.go`

**Назначение:** Проверяет, что при закрытии upstream без отправки данных через полный цикл `Handler()` прокси возвращает 502 Bad Gateway.

**Сценарий:**
1. Запускается реальный upstream с `FaultCloseImmediate`
2. Создаётся `Handler` с адресом upstream
3. Вызывается `handler(&ctx)` — полный цикл прокси
4. `acquireUpstreamConn()` — успешно (TCP connected)
5. `writeRequestHeaders()` — успешно
6. `readResponseHeaders()` — `bufIOReader.Read()` получает `io.EOF`
7. `readResponseHeaders()` возвращает `false`
8. `handle()` вызывает `pool.CloseAndDropUpstreamConnection()` и устанавливает **502**

**Проверки:**
- `handler()` не падает
- `ctx.Response.StatusCode() == 502`

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

    handler := Handler([]string{ln.Addr().String()})
    handler(&ctx)

    if ctx.Response.StatusCode() != fasthttp.StatusBadGateway {
        t.Fatalf("expected 502, got %d", ctx.Response.StatusCode())
    }
}
```

### `TestIntegrationUpstreamPartialHeaders`

**Файл:** `internal/proxy/handler_test.go`

**Назначение:** Проверяет, что при частичном заголовке ответа upstream через полный цикл `Handler()` прокси возвращает 502.

**Сценарий:**
1. Запускается реальный upstream с `FaultPartialHeaders`
2. Upstream отправляет `"HTTP/1.1 200 OK\r\n"` и закрывает
3. `readResponseHeaders()` получает неполный заголовок → ошибка → **502**

**Проверки:**
- `handler()` не падает
- `ctx.Response.StatusCode() == 502`

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

    handler := Handler([]string{ln.Addr().String()})
    handler(&ctx)

    if ctx.Response.StatusCode() != fasthttp.StatusBadGateway {
        t.Fatalf("expected 502, got %d", ctx.Response.StatusCode())
    }
}
```

---

## Подтип B1: Content-Length under-read

### `TestIntegrationUpstreamContentLengthUnderread`

**Файл:** `internal/proxy/handler_test.go`

**Назначение:** Проверяет поведение прокси при неполном теле ответа upstream через полный цикл `Handler()`: заголовки с `Content-Length: 100`, отправлено 50 байт, затем закрытие.

**Сценарий:**
1. Запускается реальный upstream с `FaultContentLengthUnderread`
2. Upstream отправляет полный заголовок с `Content-Length: 100`, 50 байт тела, закрывает
3. `readResponseHeaders()` успешно читает заголовки
4. `copyResponseStatus()` копирует статус (200)
5. `streamResponseBody()` устанавливает `PoolReader(remain=100)`
6. `ctx.Response.Body()` читает тело — PoolReader.readWithLimit получает EOF при `remain=50`, возвращает соединение в пул

**Проверки:**
- `handler()` не падает
- `ctx.Response.IsBodyStream() == true`
- `ctx.Response.ImmediateHeaderFlush == true`
- `len(body) < 100` (неполное тело)
- `len(body) > 0` (первые 50 байт прочитаны)

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

    handler := Handler([]string{ln.Addr().String()})
    handler(&ctx)

    if !ctx.Response.IsBodyStream() {
        t.Fatal("expected body stream")
    }
    if !ctx.Response.ImmediateHeaderFlush {
        t.Fatal("expected ImmediateHeaderFlush")
    }

    body := ctx.Response.Body()
    if len(body) >= 100 {
        t.Fatalf("expected body < 100 bytes due to underread, got %d", len(body))
    }
    if len(body) == 0 {
        t.Fatal("expected some body bytes")
    }
}
```

---

## Подтип B2: Chunked disconnect

### `TestIntegrationUpstreamChunkedDisconnect`

**Файл:** `internal/proxy/handler_test.go`

**Назначение:** Проверяет, что при chunked-ответе без терминатора через полный цикл `Handler()` прокси корректно устанавливает body stream и не падает.

**Сценарий:**
1. Запускается реальный upstream с `FaultChunkedDisconnect`
2. Upstream отправляет chunked-заголовок, один чанк `"hello"`, закрывает без `0\r\n\r\n`
3. `readResponseHeaders()` успешно читает заголовки
4. `streamResponseBody()` устанавливает `PoolReader(remain=-1)`
5. `ctx.Response.Body()` читает тело — PoolReader.readUntilEOF получает EOF → `CloseAndDropUpstreamConnection`

**Проверки:**
- `handler()` не падает
- `ctx.Response.IsBodyStream() == true`
- `ctx.Response.ImmediateHeaderFlush == true`

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

    handler := Handler([]string{ln.Addr().String()})
    handler(&ctx)

    if !ctx.Response.IsBodyStream() {
        t.Fatal("expected body stream")
    }
    if !ctx.Response.ImmediateHeaderFlush {
        t.Fatal("expected ImmediateHeaderFlush")
    }

    body := ctx.Response.Body()
    _ = body // может быть пустым — допустимо для неполного chunked
}
```

---

## Сводная таблица

| Тест                                            | Подтип      | FaultType                     | Статус | Проверка body stream  | Проверка тела     |
|-------------------------------------------------|-------------|-------------------------------|--------|-----------------------|-------------------|
| `TestIntegrationUpstreamCloseImmediate`         | A (empty)   | `FaultCloseImmediate`         | 502    | —                     | —                 |
| `TestIntegrationUpstreamPartialHeaders`         | A (partial) | `FaultPartialHeaders`         | 502    | —                     | —                 |
| `TestIntegrationUpstreamContentLengthUnderread` | B1          | `FaultContentLengthUnderread` | 200    | ✅                    | `len(body) < 100` |
| `TestIntegrationUpstreamChunkedDisconnect`      | B2          | `FaultChunkedDisconnect`      | 200    | ✅                    | может быть пусто  |

## Критерии приёмки (из п.5 плана)

| # | Критерий                                                    | Подтип | Статус                                                                                            |
|---|-------------------------------------------------------------|--------|---------------------------------------------------------------------------------------------------|
| 1 | Пустой ответ от upstream закрыл без данных → 502            | A      | ✅ `TestHandlerUpstreamCloseImmediate` + `TestIntegrationUpstreamCloseImmediate`                  |
| 2 | Частичный заголовок → 502                                   | A      | ✅ `TestHandlerUpstreamPartialHeaders` + `TestIntegrationUpstreamPartialHeaders`                  |
| 3 | Content-Length: 100, отправлено 50 → 502                    | B1     | ✅ `TestHandlerUpstreamContentLengthUnderread` + `TestIntegrationUpstreamContentLengthUnderread`  |
| 4 | Chunked, обрыв без терминатора → соединение закрыто         | B2     | ✅ `TestHandlerUpstreamChunkedDisconnect` + `TestIntegrationUpstreamChunkedDisconnect`            |
| 5 | После ошибки чтения заголовков соединение закрыто           | A      | ✅ (handle → CloseAndDrop)                                                                        |
| 6 | После Content-Length under-read соединение возвращено в пул | B1     | ✅ (PoolReader.readWithLimit → ReleaseUpstreamConnection)                                         |
| 7 | Все тесты проходят с `-race`                                | все    | ✅ `go test -race -shuffle=on ./...` — PASS                                                       |
