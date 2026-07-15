# Документация тестовых функций: Upstream disconnect mid-response

## Файл: `internal/proxy/handler_network_test.go`

Пакет: `proxy`

---

## 1. Обзор

Файл содержит тесты для сценария «Upstream disconnect mid-response» — самого частого сбоя в production. Тесты покрывают все подтипы, описанные в плане `Analysis-Step-20-2-Priority-scenarios-Upstream-disconnect-mid-response.md`.

---

## 2. Тестовые функции

### 2.1. `TestHandlerUpstreamCloseImmediate`

**Подтип:** A1 — разрыв до заголовков (пустой ответ).

**Сценарий:** upstream принимает TCP-соединение и **немедленно закрывает**, не отправляя ни одного байта.

**Цепочка вызовов в `handle()`:**
1. `resolveUpstream()` — OK (адрес определён)
2. `acquireUpstreamConn()` — OK (TCP-соединение установлено)
3. `writeRequestHeaders()` — OK (заголовки отправлены)
4. `readResponseHeaders()` — **FAIL**: `bufIOReader.Read()` → `io.EOF`
   - Вызывается `ctx.Error("...", 502)`
   - Возвращает `false`
5. `handle()` закрывает соединение через `CloseAndDropUpstreamConnection`
6. Клиенту возвращается **502 Bad Gateway**

**Проверка:** `ctx.Response.StatusCode() == 502`

**Используемый fault:** `FaultCloseImmediate`

```go
func TestHandlerUpstreamCloseImmediate(t *testing.T) {
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
}
```

---

### 2.2. `TestHandlerUpstreamPartialHeaders`

**Подтип:** A2 — разрыв во время заголовков (частичный заголовок).

**Сценарий:** upstream отправляет только первую строку заголовка `HTTP/1.1 200 OK\r\n` и закрывает соединение. Заголовок неполный — нет `\r\n` завершения, нет Content-Length и т.д.

**Цепочка вызовов в `handle()`:**
1. `resolveUpstream()` — OK
2. `acquireUpstreamConn()` — OK
3. `writeRequestHeaders()` — OK
4. `readResponseHeaders()` — **FAIL**: `fasthttp.ResponseHeader.Read()` не может распарсить неполный заголовок → ошибка
5. `handle()` закрывает соединение, возвращает 502

**Проверка:** `ctx.Response.StatusCode() == 502`

**Используемый fault:** `FaultPartialHeaders`

```go
func TestHandlerUpstreamPartialHeaders(t *testing.T) {
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

### 2.3. `TestHandlerUpstreamContentLengthUnderread`

**Подтип:** B1 — Content-Length under-read.

**Сценарий:** upstream отправляет **полные заголовки** с `Content-Length: 100`, затем **50 байт тела** и закрывает соединение. Заголовки прочитаны успешно, но тело неполное.

**Цепочка вызовов в `handle()`:**
1. `resolveUpstream()` — OK
2. `acquireUpstreamConn()` — OK
3. `writeRequestHeaders()` — OK
4. `readResponseHeaders()` — **OK** (заголовки полные)
5. `copyResponseStatus()` — OK (статус 200 копируется клиенту)
6. `streamResponseBody()` — OK (устанавливает `PoolReader` с `remain=100`)
7. `handle()` завершается успешно

**Ошибка чтения тела** возникает **после** `handle()`, при вызове `ctx.Response.Body()`:
- `PoolReader.readWithLimit()` получает `(50, io.EOF)` при `remain=100`
- `err != nil` → соединение возвращается в пул через `ReleaseUpstreamConnection`
- fasthttp получает EOF до достижения Content-Length → тело усечено

**Проверки:**
- `ctx.Response.IsBodyStream() == true` — body stream установлен
- `ctx.Response.ImmediateHeaderFlush == true` — заголовки отправлены до тела
- `len(ctx.Response.Body()) < 100` — тело неполное

```go
func TestHandlerUpstreamContentLengthUnderread(t *testing.T) {
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

### 2.4. `TestHandlerUpstreamChunkedDisconnect`

**Подтип:** B2 — chunked disconnect.

**Сценарий:** upstream отправляет chunked-заголовок `Transfer-Encoding: chunked`, один чанк `5\r\nhello\r\n`, и закрывает соединение **без** терминатора `0\r\n\r\n`.

**Цепочка вызовов в `handle()`:**
1. Все шаги до `streamResponseBody()` — OK
2. `streamResponseBody()` устанавливает `PoolReader` с `remain=-1` (chunked)
3. `handle()` завершается успешно

**Чтение тела:**
- `PoolReader.readUntilEOF()` получает EOF → `CloseAndDropUpstreamConnection` (соединение закрывается)
- fasthttp получает неполный chunked-ответ

**Проверки:**
- `ctx.Response.IsBodyStream() == true`
- `ctx.Response.ImmediateHeaderFlush == true`

```go
func TestHandlerUpstreamChunkedDisconnect(t *testing.T) {
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
    if !ctx.Response.ImmediateHeaderFlush {
        t.Fatal("expected ImmediateHeaderFlush")
    }

    body := ctx.Response.Body()
    if len(body) == 0 {
        t.Log("body is empty after chunked disconnect (fasthttp may discard incomplete chunk)")
    }
}
```

---

## 3. Табличный тест: `TestNetworkFailures`

Объединяет все сценарии в один табличный тест с субтестами через `t.Run`:

```go
func TestNetworkFailures(t *testing.T) {
    tests := []struct {
        name   string
        fault  FaultType
        verify func(*testing.T, *fasthttp.RequestCtx)
    }{
        {name: "upstream_close_immediate",         fault: FaultCloseImmediate,         verify: verify502},
        {name: "upstream_partial_headers",         fault: FaultPartialHeaders,         verify: verify502},
        {name: "upstream_content_length_underread", fault: FaultContentLengthUnderread, verify: verifyBodyStream},
        {name: "upstream_chunked_disconnect",      fault: FaultChunkedDisconnect,      verify: verifyBodyStream},
    }
    // ...
}
```

---

## 4. Вспомогательные файлы

| Файл                         | Назначение                                                                |
|------------------------------|---------------------------------------------------------------------------|
| `handler_faulty_upstream.go` | Утилиты: `startFaultyUpstream`, `startPartialUpstream`, типы `FaultType`  |
| `mock_conn_ext.go`           | Расширенные mock-типы: `partialReader`, `resetWriter`, `closeTrackConn`   |

---

## 5. Покрытие точек сбоя

| Точка сбоя                               | Тест                                        | Подтип |
|------------------------------------------|---------------------------------------------|--------|
| 6c. Connection closed before headers     | `TestHandlerUpstreamCloseImmediate`         | A1     |
| 6d. Partial header read                  | `TestHandlerUpstreamPartialHeaders`         | A2     |
| 8d. Content-Length mismatch (under-read) | `TestHandlerUpstreamContentLengthUnderread` | B1     |
| 8e. Chunked encoding error               | `TestHandlerUpstreamChunkedDisconnect`      | B2     |
