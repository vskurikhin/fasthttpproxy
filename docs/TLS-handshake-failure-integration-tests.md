# 5.4. Интеграционные тесты — TLS handshake failure

## Обзор

Реализованы интеграционные тесты для сценария **TLS handshake failure** (пункт 5.4 плана). Каждый тест использует реальные TCP/TLS-серверы и проверяет поведение полного цикла `Handler()`.

Файлы реализации:

- `internal/proxy/handler_integration_test.go` — интеграционные тесты
- `internal/proxy/handler_faulty_upstream_test.go` — утилиты (`startTLSServer`, `startPlainTCPServer`)

---

## Подтип A: TLS handshake failure (TCP без TLS)

### `TestIntegrationTLSHandshakeFailure`

**Файл:** `internal/proxy/handler_integration_test.go:346`

**Назначение:** Проверяет, что при подключении прокси по HTTPS к обычному TCP-серверу (без TLS) возникает ошибка handshake → 502.

**Сценарий:**
1. Запускается plain TCP-сервер через `startPlainTCPServer`
2. Настраивается `pool.TLSConfig` с `InsecureSkipVerify: true`
3. Вызывается `Handler()` с `https://` адресом upstream
4. `dial()` → `fasthttp.Dial()` (TCP успешен) → `tls.Client()` (обёртка) → соединение возвращено
5. `writeRequestHeaders()` → `bw.Flush()` → `tls.Conn.Write()` → TLS handshake с plain TCP
6. Handshake падает: `"tls: first record does not look like a TLS handshake"`
7. `writeRequestHeaders()` возвращает `false` → **502**

**Проверки:**
- `handler()` не падает
- `ctx.Response.StatusCode() == 502`

```go
func TestIntegrationTLSHandshakeFailure(t *testing.T) {
    ln, cleanup := startPlainTCPServer(t, "HTTP/1.1 200 OK\r\nContent-Length: 6\r\n\r\nhello!")
    defer cleanup()

    pool.TLSConfig(&tls.Config{InsecureSkipVerify: true, ServerName: "127.0.0.1"})
    defer pool.TLSConfig(nil)

    var ctx fasthttp.RequestCtx
    var req fasthttp.Request
    req.Header.SetMethod("GET")
    req.SetRequestURI("/")
    req.Header.SetHost("https://" + ln.Addr().String())
    ctx.Init(&req, nil, nil)

    handler := Handler(nil)
    handler(&ctx)

    if ctx.Response.StatusCode() != fasthttp.StatusBadGateway {
        t.Fatalf("expected 502, got %d", ctx.Response.StatusCode())
    }
}
```

---

## Подтип B1: Невалидный сертификат (InsecureSkipVerify=false)

### `TestIntegrationTLSInvalidCertificate`

**Файл:** `internal/proxy/handler_integration_test.go:375`

**Назначение:** Проверяет, что при подключении к TLS-серверу с самоподписанным сертификатом и `InsecureSkipVerify=false` прокси возвращает 502.

**Сценарий:**
1. Запускается TLS-сервер через `startTLSServer` с самоподписанным сертификатом
2. Настраивается `pool.TLSConfig` с `ServerName: "127.0.0.1"` (без `InsecureSkipVerify`)
3. Вызывается `Handler()` с `https://` адресом
4. TCP dial успешен, TLS handshake устанавливается
5. Верификация сертификата: самоподписанный → `"x509: certificate signed by unknown authority"`
6. `writeRequestHeaders()` возвращает `false` → **502**

**Проверки:**
- `handler()` не падает
- `ctx.Response.StatusCode() == 502`

```go
func TestIntegrationTLSInvalidCertificate(t *testing.T) {
    ln := startTLSServer(t, "HTTP/1.1 200 OK\r\nContent-Length: 6\r\n\r\nhello!")
    defer ln.Close()

    pool.TLSConfig(&tls.Config{ServerName: "127.0.0.1"})
    defer pool.TLSConfig(nil)

    var ctx fasthttp.RequestCtx
    var req fasthttp.Request
    req.Header.SetMethod("GET")
    req.SetRequestURI("/")
    req.Header.SetHost("https://" + ln.Addr().String())
    ctx.Init(&req, nil, nil)

    handler := Handler(nil)
    handler(&ctx)

    if ctx.Response.StatusCode() != fasthttp.StatusBadGateway {
        t.Fatalf("expected 502, got %d", ctx.Response.StatusCode())
    }
}
```

---

## Success case: InsecureSkipVerify=true с самоподписанным

### `TestIntegrationTLSInsecureSkipVerifySuccess`

**Файл:** `internal/proxy/handler_integration_test.go:403`

**Назначение:** Проверяет, что с `InsecureSkipVerify=true` прокси успешно проксирует запрос к TLS-серверу с самоподписанным сертификатом (200 OK).

**Сценарий:**
1. Запускается TLS-сервер через `startTLSServer`
2. Настраивается `pool.TLSConfig` с `InsecureSkipVerify: true`
3. Вызывается `Handler()` — handshake проходит, запрос проксируется
4. Статус **200 OK**

**Проверки:**
- `handler()` не падает
- `ctx.Response.StatusCode() == 200`

```go
func TestIntegrationTLSInsecureSkipVerifySuccess(t *testing.T) {
    ln := startTLSServer(t, "HTTP/1.1 200 OK\r\nContent-Length: 6\r\n\r\nhello!")
    defer ln.Close()

    pool.TLSConfig(&tls.Config{InsecureSkipVerify: true, ServerName: "127.0.0.1"})
    defer pool.TLSConfig(nil)

    var ctx fasthttp.RequestCtx
    var req fasthttp.Request
    req.Header.SetMethod("GET")
    req.SetRequestURI("/")
    req.Header.SetHost("https://" + ln.Addr().String())
    ctx.Init(&req, nil, nil)

    handler := Handler(nil)
    handler(&ctx)

    if ctx.Response.StatusCode() != fasthttp.StatusOK {
        t.Fatalf("expected 200 with InsecureSkipVerify, got %d", ctx.Response.StatusCode())
    }
}
```

---

## Success case: валидный сертификат

### `TestIntegrationTLSValidCertificate`

**Файл:** `internal/proxy/handler_integration_test.go:430`

**Назначение:** Проверяет, что при подключении к TLS-серверу с валидным сертификатом (самоподписанный + `InsecureSkipVerify=true`) прокси успешно проксирует запрос (200 OK).

**Сценарий:** Аналогично `TestIntegrationTLSInsecureSkipVerifySuccess`.

**Проверки:**
- `handler()` не падает
- `ctx.Response.StatusCode() == 200`

```go
func TestIntegrationTLSValidCertificate(t *testing.T) {
    ln := startTLSServer(t, "HTTP/1.1 200 OK\r\nContent-Length: 6\r\n\r\nhello!")
    defer ln.Close()

    pool.TLSConfig(&tls.Config{InsecureSkipVerify: true, ServerName: "127.0.0.1"})
    defer pool.TLSConfig(nil)

    var ctx fasthttp.RequestCtx
    var req fasthttp.Request
    req.Header.SetMethod("GET")
    req.SetRequestURI("/")
    req.Header.SetHost("https://" + ln.Addr().String())
    ctx.Init(&req, nil, nil)

    handler := Handler(nil)
    handler(&ctx)

    if ctx.Response.StatusCode() != fasthttp.StatusOK {
        t.Fatalf("expected 200, got %d", ctx.Response.StatusCode())
    }
}
```

---

## Сводная таблица

| Тест                                          | Подтип  | Upstream        | TLS Config                  | Статус |
|-----------------------------------------------|---------|-----------------|-----------------------------|--------|
| `TestIntegrationTLSHandshakeFailure`          | A       | Plain TCP       | `InsecureSkipVerify=true`   | 502    |
| `TestIntegrationTLSInvalidCertificate`        | B1      | TLS self-signed | `InsecureSkipVerify=false`  | 502    |
| `TestIntegrationTLSInsecureSkipVerifySuccess` | success | TLS self-signed | `InsecureSkipVerify=true`   | 200    |
| `TestIntegrationTLSValidCertificate`          | success | TLS self-signed | `InsecureSkipVerify=true`   | 200    |

## Критерии приёмки (из п.6 плана)

| # | Критерий                                        | Подтип  | Статус                                           |
|---|-------------------------------------------------|---------|--------------------------------------------------|
| 1 | TLS handshake failure → 502                     | A       | ✅ `TestIntegrationTLSHandshakeFailure`          |
| 2 | Невалидный сертификат → 502                     | B       | ✅ `TestIntegrationTLSInvalidCertificate`        |
| 3 | InsecureSkipVerify=true с самоподписанным → 200 | success | ✅ `TestIntegrationTLSInsecureSkipVerifySuccess` |
| 4 | При handshake failure соединение закрыто        | A/B     | ✅ (CloseAndDrop)                                |
| 5 | Все тесты проходят с `-race`                    | все     | ✅                                               |
