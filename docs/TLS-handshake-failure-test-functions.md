# 5.3. Тестовые функции — TLS handshake failure

## Обзор

Реализованы тестовые функции для сценария **TLS handshake failure** (пункт 5.3 плана). Каждая функция покрывает один подтип сбоя.

Файлы реализации:

- `internal/proxy/handler_network_test.go` — unit-тест
- `internal/proxy/handler_faulty_upstream_test.go` — утилиты (`generateSelfSignedCert`, `startTLSServer`, `startPlainTCPServer`)

---

## Подтип A: TLS handshake failure (обрыв после TCP)

### `TestHandlerTLSHandshakeFailure`

**Файл:** `internal/proxy/handler_network_test.go:519`

**Назначение:** Проверяет, что при ошибке, похожей на TLS handshake failure во время `writeRequestHeaders`, прокси возвращает 502.

**Сценарий:**
1. Создаётся `handler` с mock-соединением, `writer` которого возвращает `io.ErrClosedPipe` при первой записи
2. Вызывается `writeRequestHeaders()`
3. `bw.Flush()` → `conn.Write()` → ошибка (симуляция TLS handshake failure)
4. `writeRequestHeaders()` возвращает `false`, на `ctx` устанавливается **502**

**Проверки:**
- `writeRequestHeaders()` возвращает `false`
- `ctx.Response.StatusCode() == 502`

```go
func TestHandlerTLSHandshakeFailure(t *testing.T) {
    mc := newMockConn()
    mc.writer = &errWriter{err: io.ErrClosedPipe}
    var ctx fasthttp.RequestCtx
    var req fasthttp.Request
    req.Header.SetMethod("GET")
    req.SetRequestURI("/")
    req.Header.SetHost("example.com")
    ctx.Init(&req, nil, nil)

    h := &handler{
        ctx:        &ctx,
        connection: mc,
        request:    &req,
    }

    ok := h.writeRequestHeaders()
    if ok {
        t.Fatal("expected false on TLS handshake failure")
    }
    if ctx.Response.StatusCode() != fasthttp.StatusBadGateway {
        t.Fatalf("expected 502, got %d", ctx.Response.StatusCode())
    }
}
```

---

## Утилиты (handler_faulty_upstream_test.go)

### `generateSelfSignedCert`

Генерирует самоподписанный сертификат RSA-2048 для заданного Common Name. Используется для создания TLS-серверов в тестах.

```go
func generateSelfSignedCert(t *testing.T, cn string) (certPEM, keyPEM []byte)
```

### `startTLSServer`

Запускает TLS-сервер с самоподписанным сертификатом для CN=127.0.0.1. Читает HTTP-запрос и отправляет предопределённый ответ.

```go
func startTLSServer(t *testing.T, response string) net.Listener
```

### `startPlainTCPServer`

Запускает обычный TCP-сервер (без TLS). Используется для тестов, где прокси пытается подключиться по HTTPS к HTTP-серверу.

```go
func startPlainTCPServer(t *testing.T, response string) (net.Listener, func())
```

---

## Критерии приёмки (из п.6 плана)

| # | Критерий                                        | Подтип  | Статус                                           |
|---|-------------------------------------------------|---------|--------------------------------------------------|
| 1 | TLS handshake failure → 502                     | A       | ✅ `TestHandlerTLSHandshakeFailure`              |
| 2 | Невалидный сертификат → 502                     | B       | ✅ `TestIntegrationTLSInvalidCertificate`        |
| 3 | InsecureSkipVerify=true с самоподписанным → 200 | success | ✅ `TestIntegrationTLSInsecureSkipVerifySuccess` |
| 4 | При handshake failure соединение закрыто        | A/B     | ✅ (CloseAndDrop)                                |
| 5 | Все тесты проходят с `-race`                    | все     | ✅                                               |
