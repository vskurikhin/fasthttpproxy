# План: Upstream disconnect mid-response

## 1. Описание сценария

Upstream-сервер начинает отправлять ответ, но разрывает соединение до завершения передачи. Это самый частый сбой в production-прокси.

Сценарий распадается на **два подтипа** в зависимости от момента разрыва:

| Подтип                                                      | Точка сбоя                                         | Фаза в `handle()`                                                     | Ожидаемый статус клиенту |
|-------------------------------------------------------------|----------------------------------------------------|-----------------------------------------------------------------------|--------------------------|
| **A. Разрыв до завершения заголовков**                      | 6 — ReadResponseHeaders                            | `readResponseHeaders()`                                               | 502 Bad Gateway          |
| **B. Разрыв во время тела ответа**   8 — StreamResponseBody | `streamResponseBody()` → чтение через PoolReader   | 502 Bad Gateway (для Content-Length) / обрыв + закрытие (для chunked) | 502 Bad Gateway          |

---

## 2. Подтип A: Разрыв до завершения заголовков

### Что происходит в коде

`handler.readResponseHeaders()` (handler.go:190–201) вызывает `h.responseHeader.Read(h.bufIOReader)`. `fasthttp.ResponseHeader.Read()` читает из `bufio.Reader`, который буферизирует данные из `h.connection` (net.Conn). Если upstream закрывает соединение до того, как заголовки полностью прочитаны, `Read()` возвращает ошибку (например, `io.ErrUnexpectedEOF` или `io.EOF`).

Метод возвращает `false`, хендлер вызывает `pool.CloseAndDropUpstreamConnection()`, на `ctx` устанавливается 502.

### Тест

**Цель:** Проверить, что при частичном/пустом заголовке ответа upstream возвращается 502.

**Два варианта:**

1. **Upstream закрывает соединение сразу после Accept** (без отправки данных) — `readResponseHeaders` получает `io.EOF`.
2. **Upstream отправляет частичный заголовок и закрывает** — `readResponseHeaders` получает ошибку парсинга или `io.ErrUnexpectedEOF`.

### Проверка

- `ctx.Response.StatusCode() == 502`
- `metrics.ReadErrors` инкрементирован

---

## 3. Подтип B: Разрыв во время тела ответа

### Что происходит в коде

После `readResponseHeaders()` и `copyResponseStatus()` хендлер вызывает `streamResponseBody()`. Она устанавливает `PoolReader` как body stream на `ctx`. fasthttp будет читать из `PoolReader`, когда будет записывать ответ клиенту.

`PoolReader.Read()` делегирует `bufIOReader.Read()`. Если upstream закрывает соединение, `bufIOReader` получает `io.EOF` или ошибку.

**Два варианта внутри подтипа B:**

| Вариант  | Content-Length           | Поведение PoolReader                                                                                          | Ожидаемый результат                                                                                            |
|----------|--------------------------|---------------------------------------------------------------------------------------------------------------|----------------------------------------------------------------------------------------------------------------|
| **B1**   | `>= 0` (фиксированный)   | `readWithLimit()` — читает до `remain` байт. Если `remain > 0` и приходит EOF → ошибка + возврат соединения   | 502 (недогрузка)                                                                                               |
| **B2**   | `< 0` (chunked/identity) | `readUntilEOF()` — читает до EOF. EOF — штатное завершение, соединение закрывается                            | Клиент видит нормальный конец (для fasthttp EOF — конец стрима). Если данные не chunked, клиент получит обрыв. |

Для B1 (Content-Length, upstream не дослал): `readWithLimit()` получает `(n, io.EOF)` при `remain > 0`. Метод возвращает `(n, err)`, `pr.returned` становится true, соединение возвращается. fasthttp видит ошибку → 502.

Для B2 (chunked, upstream закрыл): `readUntilEOF()` получает `(n, io.EOF)`. Это штатная ситуация — `err != nil`, соединение закрывается через `CloseAndDropUpstreamConnection`. fasthttp завершает стрим. Клиент получает нормальный конец.

**Важно:** Если при chunked upstream закрывает соединение, не завершив chunked trailer, fasthttp может записать клиенту некорректный ответ (без завершающего `0\r\n\r\n`). Это отдельный сценарий — "Chunked encoding error".

### Тест B1: Content-Length under-read

Upstream в заголовках указывает `Content-Length: 100`, отправляет 50 байт и закрывает соединение.

**Mock-реализация:** `partialReader` с лимитом байт, затем `io.EOF`. Подставляется как `h.bufIOReader` через `h.connection`.

**Интеграционная реализация:** реальный TCP-сервер, который пишет заголовки с большим Content-Length, пишет часть тела и закрывает.

### Тест B2: Chunked disconnect

Upstream отправляет chunked-ответ, начинает тело, закрывает без завершающего `0\r\n\r\n`.

**Mock-реализация:** `partialReader` с chunked-данными без терминатора.

**Интеграционная реализация:** реальный TCP-сервер с chunked-ответом и обрывом.

### Проверка для B1

- `ctx.Response.StatusCode() == 502`
- `pool.ReleaseUpstreamConnection` или `CloseAndDropUpstreamConnection` вызваны

### Проверка для B2

- Соединение закрыто (CloseAndDropUpstreamConnection)
- Клиент получил усечённые данные — это допустимо для chunked (но не идеально)

---

### Проверка метрик

После каждого теста проверять:

- `metrics.ReadErrors` — инкрементирован для подтипа A
- `metrics.WriteErrors` — не инкрементирован (ошибка на чтении, не на записи)

---

## Критерии приёмки (Acceptance Criteria)

| # | Критерий                                                    | Подтип | Проверка                                |
|---|-------------------------------------------------------------|--------|-----------------------------------------|
| 1 | Пустой ответ от upstream → 502                              | A      | `StatusCode() == 502`                   |
| 2 | Частичный заголовок → 502                                   | A      | `StatusCode() == 502`                   |
| 3 | Content-Length: 100, отправлено 50 → 502                    | B1     | `StatusCode() == 502`                   |
| 4 | Chunked, обрыв без терминатора → соединение закрыто         | B2     | `CloseAndDropUpstreamConnection` вызван |
| 5 | После ошибки чтения заголовков соединение закрыто           | A      | `CloseAndDropUpstreamConnection` вызван |
| 6 | После Content-Length under-read соединение возвращено в пул | B1     | `ReleaseUpstreamConnection` вызван      |
| 7 | Все тесты проходят с `-race`                                | все    | `go test -race` без data race           |
