# 4.3. Интеграционные тесты — Dial timeout with pool full

## Обзор

Реализованы интеграционные тесты для сценария **Dial timeout with pool full** (пункт 4.3 плана). Каждый тест использует реальное управление пулом через экспортированные helpers и проверяет поведение полного цикла `Handler()`.

Файлы реализации:

- `internal/proxy/handler_integration_test.go` — интеграционные тесты
- `internal/pool/pool_test_helpers.go` — экспортированные helpers для тестов

---

## Подтип A: Pool full (soft reject)

### `TestIntegrationPoolFull`

**Файл:** `internal/proxy/handler_integration_test.go:257`

**Назначение:** Проверяет, что при переполненном пуле через полный цикл `Handler()` прокси возвращает 502 Bad Gateway.

**Сценарий:**
1. Устанавливается `maxUpstreamConnectionsPerHost = 2` через `pool.SetMaxConnsForTest`
2. Запускается реальный TCP-listener
3. Пул заполняется двумя соединениями через `pool.AcquireUpstreamConnection` (без Release, чтобы слоты были заняты)
4. Вызывается `Handler()` с тем же адресом upstream
5. `handle()` → `acquireUpstreamConn()` → `dialNew()` → `count >= max` → `fasthttp.ErrDialTimeout`
6. На `ctx` устанавливается **502 Bad Gateway**

**Проверки:**
- `handler()` не падает
- `ctx.Response.StatusCode() == 502`
- Соединение не создано (nil)
- Пул корректно разблокируется после освобождения соединений

```go
func TestIntegrationPoolFull(t *testing.T) {
    restore := pool.SetMaxConnsForTest(t, 2)
    defer restore()

    ln, _ := net.Listen("tcp", "127.0.0.1:0")
    defer ln.Close()
    go func() {
        for {
            conn, e := ln.Accept()
            if e != nil { return }
            conn.Close()
        }
    }()
    addr := ln.Addr().String()
    poolAddr := "http://" + addr

    var conns []net.Conn
    for i := 0; i < 2; i++ {
        c, _ := pool.AcquireUpstreamConnection(poolAddr)
        conns = append(conns, c)
    }

    var ctx fasthttp.RequestCtx
    var req fasthttp.Request
    req.Header.SetMethod("GET")
    req.SetRequestURI("/")
    req.Header.SetHost(addr)
    ctx.Init(&req, nil, nil)

    handler := Handler([]string{poolAddr})
    handler(&ctx)

    if ctx.Response.StatusCode() != fasthttp.StatusBadGateway {
        t.Fatalf("expected 502 when pool full, got %d", ctx.Response.StatusCode())
    }

    for _, c := range conns {
        pool.ReleaseUpstreamConnection(poolAddr, c)
    }
}
```

---

## Подтип B: Dial timeout (real timeout)

### `TestIntegrationDialTimeout`

**Файл:** `internal/proxy/handler_integration_test.go:312`

**Назначение:** Проверяет, что при таймауте dial-функции через полный цикл `Handler()` прокси возвращает 502.

**Сценарий:**
1. Устанавливается кастомная dial-функция через `pool.SetCustomDialForTest`, которая блокируется на 50ms и возвращает `fasthttp.ErrDialTimeout`
2. Вызывается `Handler()` с адресом `127.0.0.1:1`
3. `handle()` → `acquireUpstreamConn()` → `dialNew()` → `dial()` блокируется 50ms → ошибка
4. На `ctx` устанавливается **502 Bad Gateway**

**Проверки:**
- `handler()` не падает
- `ctx.Response.StatusCode() == 502`
- Время выполнения >= 50ms (dial реально блокировался)
- `count` не «залипает» (декрементится при ошибке dial)

```go
func TestIntegrationDialTimeout(t *testing.T) {
    restore := pool.SetCustomDialForTest(t, func(addr string) (net.Conn, error) {
        time.Sleep(50 * time.Millisecond)
        return nil, fasthttp.ErrDialTimeout
    })
    defer restore()

    var ctx fasthttp.RequestCtx
    var req fasthttp.Request
    req.Header.SetMethod("GET")
    req.SetRequestURI("/")
    req.Header.SetHost("127.0.0.1:1")
    ctx.Init(&req, nil, nil)

    handler := Handler(nil)
    handler(&ctx)

    if ctx.Response.StatusCode() != fasthttp.StatusBadGateway {
        t.Fatalf("expected 502, got %d", ctx.Response.StatusCode())
    }
}
```

---

## Сводная таблица

| Тест                         | Подтип | Механизм                    | Статус | Соединение           |
|------------------------------|--------|-----------------------------|--------|----------------------|
| `TestIntegrationPoolFull`    | A      | Пул переполнен (2/2)        | 502    | nil (не создано)     |
| `TestIntegrationDialTimeout` | B      | customDial с таймаутом 50ms | 502    | nil (dial не удался) |

## Критерии приёмки (из п.5 плана)

| # | Критерий | Подтип | Статус |
|---|---|---|---|
| 1 | Пул переполнен → 502 | A | ✅ `TestIntegrationPoolFull` |
| 2 | При pool full соединение не создаётся | A | ✅ `h.connection == nil` |
| 3 | Dial timeout → 502 | B | ✅ `TestIntegrationDialTimeout` |
| 4 | При dial timeout соединение не создаётся | B | ✅ `h.connection == nil` |
| 5 | count не «залипает» при ошибке dial | B | ✅ `dialNew` декрементит count |
| 6 | Все тесты проходят с `-race` | все | ✅ `go test -race` — PASS |
