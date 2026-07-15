# План: TLS handshake failure

## 1. Описание сценария

При использовании HTTPS upstream (`https://...`) прокси устанавливает TCP-соединение, затем поверх него выполняет TLS handshake. Если handshake завершается ошибкой (неверный сертификат, неподдерживаемые протоколы, несовпадение SNI, самоподписанный сертификат без `InsecureSkipVerify`), прокси должен вернуть 502 и закрыть соединение.

Сценарий распадается на **два подтипа**:

| Подтип                                          | Условие                                                       | Фаза                                                                   | Ожидаемый статус  |
|-------------------------------------------------|---------------------------------------------------------------|------------------------------------------------------------------------|-------------------|
| **A. TCP dial success + TLS handshake failure** | TCP соединяется, но TLS handshake падает                      | `acquireUpstreamConn()` → `dial()` → `tls.Client()`                    | 502 Bad Gateway   |
| **B. TLS handshake with invalid certificate**   | Сертификат невалиден/самоподписан, `InsecureSkipVerify=false` | `acquireUpstreamConn()` → `dial()` → `tls.Client()` + первый `Write()` | 502 Bad Gateway   |

---

## 2. Как работает TLS в текущем коде

### `dial()` (pool/dial.go:49–78)

```go
func dial(addr string) (net.Conn, error) {
    cleanAddr := parseUpstreamAddressForDial(addr)

    if customDial != nil {
        return customDial(cleanAddr)
    }

    if strings.HasPrefix(addr, config.PrefixHTTPS) && tlsConfig != nil {
        conn, err := fasthttp.Dial(cleanAddr)
        if err != nil {
            return nil, err
        }
        tlsConn := tls.Client(conn, tlsConfig)
        return tlsConn, nil
    }

    return fasthttp.Dial(cleanAddr)
}
```

**Ключевые особенности:**
1. `tls.Client()` не выполняет handshake синхронно — он просто оборачивает `net.Conn` в `*tls.Conn`. Handshake происходит при первой операции чтения/записи.
2. Если `tlsConfig == nil`, HTTPS-адреса обрабатываются как обычный TCP (без TLS) — это потенциальная проблема.
3. `tls.Client()` не возвращает ошибку — handshake failure возникнет позже при `writeRequestHeaders()`.

### Где происходит handshake failure

Handshake возникает при первой записи в `*tls.Conn` — в `writeRequestHeaders()`:

```
handler.writeRequestHeaders()
  → bw := pool.AcquireBufIOWriter(h.connection)  // connection — *tls.Conn
  → h.request.Header.Write(bw)                     // пишет в bufio.Writer
  → bw.Flush()                                     // flush → conn.Write → tls.Conn.Write → TLS handshake
```

Если handshake падает, `conn.Write()` возвращает ошибку TLS, которая всплывает через `bw.Flush()` → `writeRequestHeaders()` возвращает `false` → 502.

### Альтернативный путь: `customDial`

Если установлен `customDial` (через `HTTPDialerTimeout`), он полностью заменяет логику — может выполнять handshake внутри себя (например, `fasthttpproxy.FasthttpHTTPDialerDualStackTimeout`). В этом случае ошибка handshake возвращается сразу из `dial()`.

---

## 3. Подтип A: TCP dial success + TLS handshake failure

### Что происходит в коде

1. `dial()` вызывает `fasthttp.Dial(cleanAddr)` — TCP-соединение устанавливается успешно
2. `tls.Client(conn, tlsConfig)` — создаёт `*tls.Conn` (без ошибки)
3. Соединение возвращается в `handler.connection`
4. `writeRequestHeaders()` вызывает `bw.Flush()` → `conn.Write()` → `tls.Conn.Write()` → TLS handshake
5. Handshake падает (например, upstream не отвечает на TLS handshake, протоколы не согласованы)
6. `conn.Write()` возвращает ошибку TLS
7. `writeRequestHeaders()` возвращает `false`
8. `handle()` вызывает `pool.CloseAndDropUpstreamConnection()` → 502

### Причины failure

- Upstream не поддерживает TLS (обычный HTTP на 443 порту)
- Upstream закрывает соединение после TCP-диала (без handshake)
- Неподдерживаемая версия TLS
- Timeout во время handshake (медленный upstream)

### Реализация тестов

**Уровень 1: Mock-тест**

Используем `mockConn`, который успешно читает/пишет, но при первой записи возвращает TLS-подобную ошибку.

```go
func TestWriteRequestHeadersTLSHandshakeFailure(t *testing.T) {
    mc := newMockConn()
    // Симулируем ошибку TLS handshake при первой записи
    mc.writer = &errWriter{err: io.ErrClosedPipe}  // или tls.Error-like

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

**Уровень 2: Интеграционный тест с реальным TLS-сервером**

Запускаем реальный TLS-сервер с неверным сертификатом (или без него), прокси пытается подключиться с `tlsConfig` с `InsecureSkipVerify=false`.

```go
func TestIntegrationTLSHandshakeFailure(t *testing.T) {
    // Создаём TLS-сервер с самоподписанным сертификатом
    cert, err := tls.X509KeyPair(...)
    ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{cert}})
    defer ln.Close()

    // Настраиваем прокси с tlsConfig без InsecureSkipVerify
    pool.TLSConfig(&tls.Config{ServerName: "example.com"}) // не совпадает с сертификатом
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

### Проверка

- `writeRequestHeaders()` возвращает `false`
- `ctx.Response.StatusCode() == 502`
- `metrics.WriteErrors` или `metrics.BufIOWriterFlushErrors` инкрементирован
- `pool.CloseAndDropUpstreamConnection()` вызван

---

## 4. Подтип B: TLS handshake with invalid certificate

### Что происходит в коде

Аналогично подтипу A, но handshake успешно устанавливает TLS-соединение, а затем:
- `tls.Conn` проверяет сертификат сервера
- Если сертификат невалиден (истёк, не тот CN, самоподписанный без `InsecureSkipVerify`), handshake возвращает ошибку

### Варианты

| Вариант | Условие                                                    | Ожидаемая ошибка                                          |
|---------|------------------------------------------------------------|-----------------------------------------------------------|
| B1      | Самоподписанный сертификат, `InsecureSkipVerify=false`     | `tls.AlertException` или `x509.UnknownAuthorityError`     |
| B2      | Несовпадение CN/SNI                                        | `x509.HostnameError`                                      |
| B3      | Истёкший сертификат                                        | `x509.CertificateInvalidError`                            |

### Реализация тестов

**Уровень 2: Интеграционный тест**

Запускаем TLS-сервер с самоподписанным сертификатом. Прокси подключается с `tls.Config{InsecureSkipVerify: false}`.

```go
func TestIntegrationTLSInvalidCertificate(t *testing.T) {
    // Создаём самоподписанный сертификат
    cert, err := tls.X509KeyPair(...) // self-signed
    ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{cert}})
    defer ln.Close()

    // Прокси с проверкой сертификата (без skip verify)
    pool.TLSConfig(&tls.Config{InsecureSkipVerify: false, ServerName: "127.0.0.1"})
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

**Уровень 3: Тест с `InsecureSkipVerify=true` (success case)**

Убеждаемся, что с `InsecureSkipVerify=true` handshake проходит успешно.

```go
func TestIntegrationTLSInsecureSkipVerifySuccess(t *testing.T) {
    cert, err := tls.X509KeyPair(...) // self-signed
    ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{cert}})
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

### Проверка

- `ctx.Response.StatusCode() == 502` (для failure)
- `ctx.Response.StatusCode() == 200` (для success с InsecureSkipVerify)
- Соединение закрыто при failure

---

## 5. План реализации

### 5.1. Файлы

```
internal/proxy/
  ├── handler_network_test.go    — добавить TestHandlerTLSHandshakeFailure
  └── handler_faulty_upstream.go — добавить startTLSServer

internal/pool/
  └── dial_test.go               — тесты для dial() с TLS
```

### 5.2. Утилиты

```go
// startTLSServer запускает TLS-сервер с заданной конфигурацией.
func startTLSServer(t *testing.T, cfg *tls.Config, response string) net.Listener {
    t.Helper()
    ln, err := tls.Listen("tcp", "127.0.0.1:0", cfg)
    if err != nil {
        t.Fatalf("tls listen: %v", err)
    }
    go func() {
        for {
            conn, err := ln.Accept()
            if err != nil {
                return
            }
            go func(c net.Conn) {
                br := bufio.NewReader(c)
                req := fasthttp.AcquireRequest()
                req.Read(br)
                fasthttp.ReleaseRequest(req)
                bw := bufio.NewWriter(c)
                bw.WriteString(response)
                bw.Flush()
                c.Close()
            }(conn)
        }
    }()
    return ln
}
```

### 5.3. Тестовые функции

```go
// Подтип A: TLS handshake failure (обрыв после TCP)
func TestHandlerTLSHandshakeFailure(t *testing.T)

// Подтип B1: Неверный сертификат
func TestHandlerTLSInvalidCertificate(t *testing.T)

// Success case: InsecureSkipVerify=true
func TestHandlerTLSInsecureSkipVerifySuccess(t *testing.T)

// Success case: валидный сертификат
func TestHandlerTLSValidCertificate(t *testing.T)
```

### 5.4. Интеграционные тесты

```go
func TestIntegrationTLSHandshakeFailure(t *testing.T) {
    // TCP-сервер без TLS, прокси с https:// — handshake failure
}

func TestIntegrationTLSInvalidCertificate(t *testing.T) {
    // TLS-сервер с самоподписанным сертификатом, InsecureSkipVerify=false
}

func TestIntegrationTLSValidCertificate(t *testing.T) {
    // TLS-сервер с валидным сертификатом
}
```

---

## 6. Критерии приёмки

| # | Критерий                                        | Подтип  | Проверка                                |
|---|-------------------------------------------------|---------|-----------------------------------------|
| 1 | TLS handshake failure → 502                     | A       | `StatusCode() == 502`                   |
| 2 | Невалидный сертификат → 502                     | B       | `StatusCode() == 502`                   |
| 3 | InsecureSkipVerify=true с самоподписанным → 200 | success | `StatusCode() == 200`                   |
| 4 | При handshake failure соединение закрыто        | A/B     | `CloseAndDropUpstreamConnection` вызван |
| 5 | Все тесты проходят с `-race`                    | все     | `go test -race` без data race           |

## 7. Различия с предыдущими сценариями

| Аспект          | Upstream disconnect                          | Dial timeout                   | TLS handshake failure           |
|-----------------|----------------------------------------------|--------------------------------|---------------------------------|
| Фаза в handle() | `readResponseHeaders` / `streamResponseBody` | `acquireUpstreamConn()`        | `writeRequestHeaders()` (flush) |
| Причина ошибки  | Upstream закрыл сокет                        | Пул переполнен / таймаут диала | TLS handshake не прошёл         |
| Статус клиенту  | 502                                          | 502                            | 502                             |
| Upstream conn   | Есть, закрывается                            | Нет (не создан)                | Есть, закрывается               |
| Требуется TLS   | Нет                                          | Нет                            | Да                              |
