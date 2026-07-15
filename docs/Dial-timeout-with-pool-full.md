# План: Dial timeout with pool full

## 1. Описание сценария

Комбинация двух условий: пул соединений к upstream переполнен (`maxUpstreamConnectionsPerHost`), и при попытке создать новое соединение `dialNew()` возвращает `fasthttp.ErrDialTimeout`. Это самый частый сбой на высоконагруженных прокси, когда все соединения к upstream заняты.

Сценарий распадается на **два подтипа**:

| Подтип                           | Условие                                                                  | Фаза                                    | Ожидаемый статус |
|----------------------------------|--------------------------------------------------------------------------|-----------------------------------------|------------------|
| **A. Pool full (soft reject)**   | `count >= maxUpstreamConnsPerHost`, dial не вызывается                   | `acquireUpstreamConn()` → `dialNew()`   | 502 Bad Gateway |
| **B. Dial timeout + pool full**  | `count < max`, dial вызывается, но таймаут + параллельно пул заполняется | `acquireUpstreamConn()` → `dial()`      | 502 Bad Gateway |

---

## 2. Подтип A: Pool full (soft reject)

### Что происходит в коде

`connPool.dialNew()` (pool.go:84–97):

```go
func (cp *connPool) dialNew(addr string) (net.Conn, error) {
    if atomic.LoadInt32(&cp.count) >= int32(maxUpstreamConnectionsPerHost) {
        return nil, fasthttp.ErrDialTimeout  // <— здесь
    }
    // ...
}
```

Когда `count == maxUpstreamConnectionsPerHost`, `dialNew()` сразу возвращает `fasthttp.ErrDialTimeout` **без вызова `dial()`**. Это не настоящий таймаут, а сигнал «пул переполнен».

В `handler.acquireUpstreamConn()` (handler.go:115–126):

```go
h.connection, err = pool.AcquireUpstreamConnection(h.upstreamAddress)
if err != nil {
    metrics.DialErrors.Inc()
    h.ctx.Error("cannot connect to upstream", fasthttp.StatusBadGateway)
    return false
}
```

Ошибка `fasthttp.ErrDialTimeout` попадает в `ctx.Error()` → клиент получает **502**.

### Существующее покрытие

В `upstream_pool_test.go` уже есть тесты для лимита пула:

| Тест                             | Проверка                                                                                                |
|----------------------------------|---------------------------------------------------------------------------------------------------------|
| `TestGetLimit`                   | После `maxUpstreamConnectionsPerHost` вызовов `AcquireUpstreamConnection()` возвращает `ErrDialTimeout` |
| `TestSetMaxConnsPerHostValid`    | Аналогично с кастомным лимитом 50                                                                       |
| `TestSetMaxConnsPerHostMinimum`  | С лимитом 1                                                                                             |
| `TestSetMaxConnsPerHostIsolated` | Изоляция пулов для разных адресов                                                                       |

Но нет теста, который проверяет **полный цикл handle()** при переполненном пуле.

### Проверка

- `acquireUpstreamConn()` возвращает `false`
- `ctx.Response.StatusCode() == 502`
- `metrics.DialErrors` инкрементирован
- Соединение не создано (net.Conn == nil)

---

## 3. Подтип B: Dial timeout + pool full (real timeout)

### Что происходит в коде

Реальный сценарий: пул почти полон (`count < max`), вызывается `dial()`, но:
1. `fasthttp.Dial` имеет таймаут (по умолчанию ~30 секунд)
2. Upstream не отвечает в течение таймаута
3. `dial()` возвращает ошибку таймаута
4. За время ожидания диала другие запросы заполнили пул

**Разница с подтипом A:** здесь dial **реально вызывается**, тратится время на ожидание, и только потом возвращается ошибка.

### Текущий код

`fasthttp.Dial(cleanAddr)` (dial.go:77) вызывает `net.DialTimeout` с дефолтным таймаутом. Если таймаут истекает, возвращается ошибка.

После ошибки `dialNew()` декрементит `count` (pool.go:93):

```go
conn, err := dial(addr)
if err != nil {
    atomic.AddInt32(&cp.count, -1)
    return nil, err
}
```

Так что count не "залипает" — он корректно уменьшается. Но проблема в том, что во время ожидания dial-таймаута другие запросы могут заполнить пул и тоже получить ошибку.

### Реализация

**Mock:** `customDial` с задержкой, превышающей таймаут.

```go
// slowDial — dial-функция, которая блокируется на заданное время и возвращает ошибку.
func slowDial(addr string, delay time.Duration) (net.Conn, error) {
    time.Sleep(delay)
    return nil, fasthttp.ErrDialTimeout
}
```

---

## 5. Критерии приёмки

| # | Критерий                                 | Подтип | Проверка                      |
|---|------------------------------------------|--------|-------------------------------|
| 1 | Пул переполнен → 502                     | A      | `StatusCode() == 502`         |
| 2 | При pool full соединение не создаётся    | A      | `h.connection == nil`         |
| 3 | Dial timeout → 502                       | B      | `StatusCode() == 502`         |
| 4 | При dial timeout соединение не создаётся | B      | `h.connection == nil`         |
| 5 | count не «залипает» при ошибке dial      | B      | `poolConnCount` не изменился  |
| 6 | Все тесты проходят с `-race`             | все    | `go test -race` без data race |

## 6. Различия с upstream disconnect

| Аспект            | Upstream disconnect                            | Dial timeout / pool full                     |
|-------------------|------------------------------------------------|----------------------------------------------|
| Фаза в handle()   | `readResponseHeaders` или `streamResponseBody` | `acquireUpstreamConn()`                      |
| Статус клиенту    | 502                                            | 502                                          |
| Upstream conn     | Есть, затем закрывается                        | Нет (не создано)                             |
| Причина ошибки    | Upstream закрыл сокет                          | Пул переполнен / upstream не отвечает        |
| Время обработки   | Быстро (чтение данных)                         | Быстро (A) / Медленно (B, ожидание таймаута) |
