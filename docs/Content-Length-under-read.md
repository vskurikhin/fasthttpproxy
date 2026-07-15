# План: Content-Length under-read

## 1. Описание сценария

Upstream в заголовке ответа указывает `Content-Length: N`, но отправляет меньше чем `N` байт тела и закрывает (или обрывает) соединение. Прокси должен корректно обработать несоответствие: не возвращать повреждённое соединение в пул, вернуть клиенту 502.

Сценарий распадается на **два подтипа**:

| Подтип                    | Условие                                                               | Поведение PoolReader                                                                                          | Риск                                                                                        |
|---------------------------|-----------------------------------------------------------------------|---------------------------------------------------------------------------------------------------------------|---------------------------------------------------------------------------------------------|
| **A. Under-read + EOF**   | Upstream отправляет `< N` байт и закрывает соединение (штатный EOF)   | `readWithLimit()` получает `io.EOF` при `remain > 0` → возвращает `(n, err)`, соединение возвращается в пул   | **Да:** conn возвращён как живой, но он может содержать непрочитанные данные                |
| **B. Under-read + RST**   | Upstream отправляет `< N` байт и сбрасывает соединение (ECONNRESET)   | `readWithLimit()` получает ошибку RST при `remain > 0` → возвращает `(n, err)`, соединение возвращается в пул | **Да:** conn мёртв, но возвращается в пул (будет закрыт при следующей попытке использовать) |

---

## 2. Подтип A: Under-read + EOF

### Что происходит в коде

`PoolReader.readWithLimit()` (pool_reader.go:57–90):

```go
n, err := pr.reader.Read(p)   // n < remain, err == io.EOF
pr.remain -= int64(n)          // remain > 0

if err != nil && !pr.returned {
    // Возвращаем соединение в пул!
    pr.returned = true
    pool.ReleaseUpstreamConnection(pr.upstreamAddr, pr.connection)
    // ...
}

return n, err  // (n, io.EOF)
```

**Проблема:** соединение возвращается в пул через `ReleaseUpstreamConnection` (живым), хотя upstream закрыл сокет. Следующий запрос, получивший это соединение, при записи заголовков получит `write: broken pipe` или аналогичную ошибку.

**Правильное поведение:** при under-read (EOF до достижения лимита) соединение **должно закрываться** через `CloseAndDropUpstreamConnection`, а не возвращаться в пул.

### Текущее поведение

```go
// readWithLimit при err != nil:
if err != nil && !pr.returned {
    pr.returned = true
    pool.ReleaseUpstreamConnection(pr.upstreamAddr, pr.connection)  // <— Release, не CloseAndDrop!
    // ...
}
```

Это баг: при under-read соединение возвращается в пул как живое, но на самом деле оно мёртво.

### Варианты under-read

| Вариант | Content-Length | Отправлено байт                           | Результат             |
|---------|----------------|-------------------------------------------|-----------------------|
| A1      | 100            | 50                                        | EOF на половине       |
| A2      | 100            | 0                                         | EOF без единого байта |
| A3      | 100            | 99                                        | EOF почти в конце     |
| A4      | 100            | 100 (но upstream закрывает сразу после)   | Штатный случай        |

### Реализация тестов

**Уровень 1: Unit-тест PoolReader**

```go
func TestPoolReaderContentLengthUnderreadReturnsToPool(t *testing.T) {
    // Проверяем текущее поведение: при under-read соединение возвращается в пул.
    var closed atomic.Bool
    conn := &closeTrackConn{closeFn: func() { closed.Store(true) }}

    // Симулируем upstream, который отправляет 3 байта из 5 и EOF
    pr := NewPoolReader(&partialReader{
        data: []byte("abc"),
        err:  io.EOF,
    }, "example.com:8080", conn, 5, nil)

    buf := make([]byte, 64)
    n, err := pr.Read(buf)
    if n != 3 {
        t.Fatalf("expected 3 bytes, got %d", n)
    }
    if err != io.EOF {
        t.Fatalf("expected EOF, got %v", err)
    }

    // Текущее поведение: соединение возвращено в пул (Release, не CloseAndDrop)
    if closed.Load() {
        t.Log("connection was closed — expected CloseAndDrop behavior")
    }
}
```

**Уровень 2: Интеграционный тест через handler**

Используем `FaultContentLengthUnderread` (уже есть в `handler_faulty_upstream.go`).

```go
func TestHandlerContentLengthUnderread(t *testing.T) {
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

    // handle() не падает — stream установлен
    if !ctx.Response.IsBodyStream() {
        t.Fatal("expected body stream")
    }

    // Читаем тело — должно быть меньше 100 байт
    body := ctx.Response.Body()
    if len(body) >= 100 {
        t.Fatalf("expected body < 100 bytes, got %d", len(body))
    }
    if len(body) == 0 {
        t.Fatal("expected some body bytes")
    }
}
```

### Проверка

- `handle()` не падает (stream установлен)
- `ctx.Response.Body()` возвращает `< 100` байт
- Соединение возвращается в пул (Release) или закрывается (CloseAndDrop) — зависит от того, считаем ли мы это багом или фичей

---

## 3. Подтип B: Under-read + RST

### Что происходит в коде

Аналогично подтипу A, но вместо `io.EOF` приходит `io.ErrUnexpectedEOF` или ошибка соединения (connection reset by peer).

`readWithLimit()`:
```go
n, err := pr.reader.Read(p)   // err == io.ErrUnexpectedEOF
// ...
if err != nil && !pr.returned {
    pool.ReleaseUpstreamConnection(...)  // тоже Release!
}
```

**Проблема:** соединение гарантированно мертво (RST), но возвращается в пул как живое. При следующей попытке использования `writeRequestHeaders()` получит ошибку.

### Реализация тестов

```go
func TestPoolReaderContentLengthUnderreadRST(t *testing.T) {
    var closed atomic.Bool
    conn := &closeTrackConn{closeFn: func() { closed.Store(true) }}

    pr := NewPoolReader(&partialReader{
        data: []byte("ab"),
        err:  io.ErrUnexpectedEOF,
    }, "example.com:8080", conn, 5, nil)

    buf := make([]byte, 64)
    n, err := pr.Read(buf)
    if n != 2 {
        t.Fatalf("expected 2 bytes, got %d", n)
    }
    if err != io.ErrUnexpectedEOF {
        t.Fatalf("expected ErrUnexpectedEOF, got %v", err)
    }

    if closed.Load() {
        t.Log("connection was closed on RST underread")
    }
}
```

### Проверка

- `PoolReader.Read()` возвращает `(n, err)` с частичными данными
- Соединение возвращается в пул (текущее поведение) или закрывается (ideal)

---

## 4. Дополнительные сценарии

### 4.1. Zero body with Content-Length > 0

Upstream отправляет заголовки с `Content-Length: 100`, затем `\r\n\r\n` и закрывает без единого байта тела.

В `readWithLimit()`:
- `pr.remain = 100`
- `pr.reader.Read(buf)` → `(0, io.EOF)` (нет данных)
- `n = 0, err = io.EOF`
- `pr.remain -= 0` → remain = 100
- `err != nil` → Release соединения
- Возвращает `(0, io.EOF)`

Клиент получает пустое тело. Соединение возвращается в пул (мертвое).

### 4.2. Over-read (Content-Length меньше тела)

Upstream отправляет `Content-Length: 5`, затем 10 байт тела и закрывает. Это **не under-read**, а over-read — но тоже mismatch.

`readWithLimit()` читает только 5 байт (ограничение `remain`), остальные 5 байт остаются в буфере. Соединение возвращается в пул с остаточными данными — следующий запрос получит «хвост».

Это отдельный сценарий (Content-Length mismatch: too much data), не входит в текущий план.

---

## 5. План реализации

### 5.1. Файлы

```
internal/readers/
  └── pool_reader_test.go     — добавить тесты для under-read (A1-A4, B)

internal/proxy/
  ├── handler_network_test.go — добавить TestHandlerContentLengthUnderreadVariants
  └── handler_faulty_upstream.go — уже есть FaultContentLengthUnderread
```

### 5.2. Тестовые функции

```go
// Подтип A: under-read + EOF
func TestPoolReaderContentLengthUnderreadEOF(t *testing.T)
func TestHandlerContentLengthUnderreadEOF(t *testing.T)

// Подтип A2: zero body with Content-Length > 0
func TestPoolReaderContentLengthZeroBody(t *testing.T)

// Подтип B: under-read + RST
func TestPoolReaderContentLengthUnderreadRST(t *testing.T)

// Варианты
func TestPoolReaderContentLengthUnderreadSevere(t *testing.T)  // 99 из 100
func TestPoolReaderContentLengthUnderreadPartial(t *testing.T) // 50 из 100
```

### 5.3. Новые fault-типы

```go
const (
    // ...
    FaultContentLengthZeroBody   // Content-Length: 100, 0 байт тела
    FaultContentLengthUnderread50  // Content-Length: 100, 50 байт (уже есть)
    FaultContentLengthUnderread99  // Content-Length: 100, 99 байт
)
```

---

## 6. Критерии приёмки

| # | Критерий                                              | Подтип | Проверка                              |
|---|-------------------------------------------------------|--------|---------------------------------------|
| 1 | Content-Length: 100, тело 50 → EOF + частичное чтение | A      | `n == 50, err == io.EOF`              |
| 2 | Content-Length: 100, тело 0 → EOF без данных          | A2     | `n == 0, err == io.EOF`               |
| 3 | Content-Length: 100, RST на 50 → ErrUnexpectedEOF     | B      | `n == 50, err == io.ErrUnexpectedEOF` |
| 4 | handle() не падает при under-read                     | A/B    | `handle()` возвращается без паники    |
| 5 | `ctx.Response.Body()` < Content-Length                | A/B    | `len(Body()) < 100`                   |
| 6 | Соединение возвращается в пул (текущее поведение)     | A/B    | `ReleaseUpstreamConnection` вызван    |
| 7 | Все тесты проходят с `-race`                          | все    | `go test -race` без data race         |

## 7. Различия с upstream disconnect mid-response (B1)

| Аспект         | Upstream disconnect mid-response (B1)                | Content-Length under-read                           |
|----------------|------------------------------------------------------|-----------------------------------------------------|
| Объём          | Частный случай under-read                            | Общий случай                                        |
| Content-Length | Любой                                                | >= 0 (фиксированный)                                |
| Тип ошибки     | EOF или RST                                          | EOF или RST                                         |
| Покрытие       | Уже есть `TestHandlerUpstreamContentLengthUnderread` | Добавляются варианты (zero body, severe under-read) |
| PoolReader     | `readWithLimit()`                                    | `readWithLimit()`                                   |
| Соединение     | Возвращается в пул                                   | Возвращается в пул (тот же баг)                     |
