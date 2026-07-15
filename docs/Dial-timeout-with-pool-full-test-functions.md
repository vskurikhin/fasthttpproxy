# 4.2. Тестовые функции — Dial timeout with pool full

## Обзор

Реализованы тестовые функции для сценария **Dial timeout with pool full** (пункт 4.2 плана). Каждая функция покрывает один подтип сбоя, используя mock-соединения и утилиты пула.

Файлы реализации:

- `internal/proxy/handler_network_test.go` — unit-тесты
- `internal/pool/pool_test_helpers.go` — экспортированные тестовые helpers
- `internal/pool/upstream_pool_test.go` — существующие тесты пула

---

## Подтип A: Pool full (soft reject)

### `TestAcquireUpstreamConnPoolFull`

**Файл:** `internal/proxy/handler_network_test.go:338`

**Назначение:** Проверяет, что при переполненном пуле `acquireUpstreamConn()` возвращает `false` и устанавливает 502 без создания соединения.

**Сценарий:**
1. Устанавливается `maxUpstreamConnectionsPerHost = 1` через `pool.SetMaxConnsForTest`
2. Занимается единственный слот через `pool.AcquireUpstreamConnection` (реальный listener)
3. Вызывается `handler.acquireUpstreamConn()` — второй раз для того же адреса
4. `dialNew()` видит `count >= max` → возвращает `fasthttp.ErrDialTimeout` без вызова `dial()`
5. `acquireUpstreamConn()` возвращает `false`, на `ctx` устанавливается **502**

**Проверки:**
- `acquireUpstreamConn()` возвращает `false`
- `ctx.Response.StatusCode() == 502`
- `h.connection == nil` (соединение не создано)

```go
func TestAcquireUpstreamConnPoolFull(t *testing.T) {
    restore := pool.SetMaxConnsForTest(t, 1)
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

    conn1, _ := pool.AcquireUpstreamConnection(ln.Addr().String())

    var ctx fasthttp.RequestCtx
    ctx.Init(&fasthttp.Request{}, nil, nil)

    h := &handler{
        ctx:             &ctx,
        upstreamAddress: ln.Addr().String(),
    }
    ok := h.acquireUpstreamConn()
    if ok { t.Fatal("expected false") }
    if ctx.Response.StatusCode() != fasthttp.StatusBadGateway { ... }
    if h.connection != nil { t.Fatal("expected nil connection") }

    pool.ReleaseUpstreamConnection(ln.Addr().String(), conn1)
}
```

---

## Подтип B: Dial timeout (real timeout)

### `TestAcquireUpstreamConnDialTimeout`

**Файл:** `internal/proxy/handler_network_test.go:397`

**Назначение:** Проверяет, что при таймауте dial-функции `acquireUpstreamConn()` возвращает `false` и устанавливает 502.

**Сценарий:**
1. Устанавливается кастомная dial-функция через `pool.SetCustomDialForTest`, которая блокируется на 100ms и возвращает `fasthttp.ErrDialTimeout`
2. Вызывается `handler.acquireUpstreamConn()`
3. `dialNew()` инкрементирует `count`, вызывает `dial()` → блокируется 100ms → ошибка
4. `dialNew()` декрементирует `count`, возвращает ошибку
5. `acquireUpstreamConn()` возвращает `false`, **502**

**Проверки:**
- `acquireUpstreamConn()` возвращает `false`
- Время выполнения >= 100ms (dial реально блокировался)
- `ctx.Response.StatusCode() == 502`
- `h.connection == nil`

```go
func TestAcquireUpstreamConnDialTimeout(t *testing.T) {
    restore := pool.SetCustomDialForTest(t, func(addr string) (net.Conn, error) {
        time.Sleep(100 * time.Millisecond)
        return nil, fasthttp.ErrDialTimeout
    })
    defer restore()

    var ctx fasthttp.RequestCtx
    ctx.Init(&fasthttp.Request{}, nil, nil)

    h := &handler{
        ctx:             &ctx,
        upstreamAddress: "127.0.0.1:1",
    }

    start := time.Now()
    ok := h.acquireUpstreamConn()
    elapsed := time.Since(start)

    if ok { t.Fatal("expected false") }
    if elapsed < 100*time.Millisecond { ... }
    if ctx.Response.StatusCode() != fasthttp.StatusBadGateway { ... }
    if h.connection != nil { t.Fatal("expected nil connection") }
}
```

---

## Комбинация: Handler + Pool full

### `TestHandlerPoolFull`

**Файл:** `internal/proxy/handler_network_test.go:432`

**Назначение:** Проверяет, что при переполненном пуле через полный цикл `Handler()` прокси возвращает 502.

**Сценарий:**
1. Устанавливается `maxUpstreamConnectionsPerHost = 2`
2. Заполняется пул через `pool.AcquireUpstreamConnection` (2 соединения для одного адреса)
3. Вызывается `Handler()` — третий запрос к тому же адресу
4. `acquireUpstreamConn()` → `dialNew()` → `count >= max` → `ErrDialTimeout` → **502**

**Проверки:**
- `handler()` не падает
- `ctx.Response.StatusCode() == 502`

### `TestHandlerDialTimeout`

**Файл:** `internal/proxy/handler_network_test.go:480`

**Назначение:** Проверяет, что при таймауте dial-функции через полный цикл `Handler()` прокси возвращает 502.

**Сценарий:**
1. Устанавливается кастомная dial-функция с таймаутом 50ms
2. Вызывается `Handler()` — `acquireUpstreamConn` получает `ErrDialTimeout` → **502**

**Проверки:**
- `handler()` не падает
- `ctx.Response.StatusCode() == 502`

---

## Экспортированные тестовые helpers (pool/pool_test_helpers.go)

Добавлены функции для доступа к внутреннему состоянию пула из тестов proxy:

| Функция                            | Назначение                                                        |
|------------------------------------|-------------------------------------------------------------------|
| `pool.SetMaxConnsForTest(t, n)`    | Устанавливает `maxUpstreamConnectionsPerHost`, возвращает restore |
| `pool.SetIdleTimeoutForTest(t, d)` | Устанавливает `idleTimeout`, возвращает restore                   |
| `pool.PoolConnCount(addr)`         | Возвращает текущее значение `count` для адреса                    |
| `pool.SetCustomDialForTest(t, fn)` | Устанавливает `customDial`, возвращает restore                    |

---

## Критерии приёмки (из п.5 плана)

| # | Критерий                                 | Подтип | Статус                                  |
|---|------------------------------------------|--------|-----------------------------------------|
| 1 | Пул переполнен → 502                     | A      | ✅ `TestAcquireUpstreamConnPoolFull`    |
| 2 | При pool full соединение не создаётся    | A      | ✅ `h.connection == nil`                |
| 3 | Dial timeout → 502                       | B      | ✅ `TestAcquireUpstreamConnDialTimeout` |
| 4 | При dial timeout соединение не создаётся | B      | ✅ `h.connection == nil`                |
| 5 | count не «залипает» при ошибке dial      | B      | ✅ `dialNew` декрементит count          |
| 6 | Все тесты проходят с `-race`             | все    | ✅ `go test -race` — PASS               |
