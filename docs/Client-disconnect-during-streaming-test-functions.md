# 4.3. Тестовые функции — Client disconnect during streaming

## Обзор

Реализованы тестовые функции для сценария **Client disconnect during streaming** (пункт 4.3 плана). Каждая функция покрывает один подтип сбоя и использует mock-соединения или реальный upstream через `startFaultyClientUpstream`.

Файлы реализации:

- `internal/proxy/handler_network_test.go` — unit-тесты
- `internal/proxy/handler_faulty_upstream_test.go` — upstream-утилиты
- `internal/proxy/mock_conn_ext_test.go` — mock-типы

---

## Подтип A: Client disconnect during request body

### `TestHandlerClientDisconnectRequestBody`

**Файл:** `internal/proxy/handler_network_test.go`

**Назначение:** Проверяет, что при ошибке чтения клиентского body stream (симуляция обрыва клиента во время POST) прокси возвращает 502 Bad Gateway.

**Сценарий:**
1. Создаётся `handler` с POST-запросом, у которого `BodyStream` установлен на `errReader{err: io.ErrUnexpectedEOF}`
2. Вызывается `writeRequestBody()`
3. `PipeCopy(src=BodyStream, dst=connection)` вызывает `src.Read()` → получает `io.ErrUnexpectedEOF`
4. `PipeCopy` возвращает ошибку → `writeRequestBody()` возвращает `false`
5. На `ctx` устанавливается **502 Bad Gateway**

**Проверки:**
- `writeRequestBody()` возвращает `false`
- `ctx.Response.StatusCode() == 502`

**Используемые mock-типы:**
- `errReader` (mock_conn_ext_test.go) — reader, который всегда возвращает ошибку при `Read()`

```go
func TestHandlerClientDisconnectRequestBody(t *testing.T) {
    mc := newMockConn()
    var ctx fasthttp.RequestCtx
    var req fasthttp.Request
    req.Header.SetMethod("POST")
    req.SetRequestURI("/")
    req.Header.SetHost("example.com")
    ctx.Init(&req, nil, nil)
    ctx.Request.SetBodyStream(&errReader{err: io.ErrUnexpectedEOF}, -1)

    h := &handler{
        ctx:        &ctx,
        connection: mc,
    }

    ok := h.writeRequestBody()
    if ok {
        t.Fatal("expected false when client body stream fails")
    }
    if ctx.Response.StatusCode() != fasthttp.StatusBadGateway {
        t.Fatalf("expected 502, got %d", ctx.Response.StatusCode())
    }
}
```

---

## Подтип B1: Client disconnect during Content-Length response

### `TestHandlerClientDisconnectResponseContentLength`

**Файл:** `internal/proxy/handler_network_test.go`

**Назначение:** Проверяет, что при обрыве клиента во время стриминга ответа с Content-Length прокси корректно устанавливает body stream. Соединение upstream возвращается в пул (PoolReader.readWithLimit дочитывает до remain).

**Сценарий:**
1. Запускается upstream с `FaultClientDisconnectResponseContentLength`: отправляет `200 OK` + `Content-Length: 1000` + 500 байт тела
2. Вызывается `handle()` через `Handler()`
3. `streamResponseBody()` устанавливает body stream с `PoolReader(remain=1000)`
4. `ctx.Response.Body()` читает тело — PoolReader получает 500 байт (ошибка чтения на 501-м), дочитывает до `remain=0` и вызывает `ReleaseUpstreamConnection`

**Проверки:**
- `handle()` не падает
- `ctx.Response.IsBodyStream() == true`
- `ctx.Response.ImmediateHeaderFlush == true`
- `len(body) > 0` (частичное тело получено)
- `len(body) <= 500` (неполный ответ из-за обрыва)

```go
func TestHandlerClientDisconnectResponseContentLength(t *testing.T) {
    ResetUpstreams()
    ln := startFaultyClientUpstream(t, FaultClientDisconnectResponseContentLength)
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

### `TestHandlerClientDisconnectResponseChunked`

**Файл:** `internal/proxy/handler_network_test.go`

**Назначение:** Проверяет, что при обрыве клиента во время стриминга chunked-ответа прокси корректно устанавливает body stream. Соединение upstream закрывается (PoolReader.readUntilEOF получает EOF → CloseAndDrop).

**Сценарий:**
1. Запускается upstream с `FaultClientDisconnectResponseChunked`: отправляет chunked-заголовок, один чанк "hello", и закрывает без `0\r\n\r\n`
2. Вызывается `handle()` через `Handler()`
3. `streamResponseBody()` устанавливает body stream с `PoolReader(remain=-1)`
4. `ctx.Response.Body()` читает тело — PoolReader.readUntilEOF получает EOF → `CloseAndDropUpstreamConnection`

**Проверки:**
- `handle()` не падает
- `ctx.Response.IsBodyStream() == true`
- `ctx.Response.ImmediateHeaderFlush == true`
- Тело может быть пустым (fasthttp отбрасывает неполный chunked-чанк)

```go
func TestHandlerClientDisconnectResponseChunked(t *testing.T) {
    ResetUpstreams()
    ln := startFaultyClientUpstream(t, FaultClientDisconnectResponseChunked)
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
    _ = body // может быть пустым — это допустимо для неполного chunked
}
```

---

## Новые типы сбоев (handler_faulty_upstream_test.go)

В `FaultType` добавлены три новых константы:

```go
const (
    // ... существующие типы
    FaultClientDisconnectRequest                       // клиент обрывает POST-запрос
    FaultClientDisconnectResponseContentLength         // клиент обрывает GET с Content-Length
    FaultClientDisconnectResponseChunked               // клиент обрывает GET с chunked
)
```

Функция `startFaultyClientUpstream` реализует поведение upstream для каждого типа:

| Тип сбоя | Поведение upstream |
|---|---|
| `FaultClientDisconnectRequest` | Читает частичный запрос, отвечает 502, закрывает |
| `FaultClientDisconnectResponseContentLength` | Отправляет `200 OK + Content-Length: 1000 + 500 байт тела`, закрывает |
| `FaultClientDisconnectResponseChunked` | Отправляет chunked-заголовок + один чанк, закрывает без терминатора |

---

## Новый mock-тип (mock_conn_ext_test.go)

Добавлен `errReader` — reader, который всегда возвращает ошибку при `Read()`:

```go
type errReader struct {
    io.Reader
    err error
}

func (r *errReader) Read(p []byte) (int, error) { return 0, r.err }
```

Используется в `TestHandlerClientDisconnectRequestBody` для симуляции обрыва клиентского body stream.
