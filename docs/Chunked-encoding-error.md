# План: Chunked encoding error

## 1. Описание сценария

Upstream присылает ответ с `Transfer-Encoding: chunked`, но данные содержат ошибки: неверный размер чанка, отсутствие терминатора, битый trailer. Прокси не парсит chunked-кодировку — он просто стримит сырые байты через `PoolReader.readUntilEOF()`. Ошибка проявляется на стороне fasthttp, когда он пытается декодировать chunked-стрим для записи клиенту.

Сценарий распадается на **три подтипа**:

| Подтип                    | Ошибка                                      | Где проявляется                                      | Ожидаемый статус          |
|---------------------------|---------------------------------------------|------------------------------------------------------|---------------------------|
| **A. Missing terminator** | Upstream закрывает без `0\r\n\r\n`          | fasthttp получает EOF до завершения chunked-трайлера | 200 + неполное тело / 502 |
| **B. Invalid chunk size** | Неверный hex-размер чанка                   | fasthttp не может распарсить размер                  | 502                       |
| **C. Broken trailer**     | Некорректный trailer после последнего чанка | fasthttp получает мусор после `0\r\n`                | 502                       |

---

## 2. Как работает chunked-стриминг в прокси

Прокси не парсит chunked-кодировку. Весь стриминг выглядит так:

```
handler.streamResponseBody()
  → contentLen = h.responseHeader.ContentLength()  // -1 для chunked
  → pr = NewPoolReader(bufIOReader, addr, conn, -1, ...)  // remain = -1
  → ctx.SetBodyStream(pr, -1)
```

Прокси читает сырые байты из upstream и передаёт их fasthttp. fasthttp, получив `Content-Length < 0`, ожидает chunked-кодировку и декодирует её при записи ответа клиенту.

Ошибки chunked-кодировки возникают **в fasthttp**, а не в прокси. Прокси просто доставляет байты.

---

## 3. Подтип A: Missing terminator

### Что происходит

Upstream отправляет:
```
HTTP/1.1 200 OK\r\n
Transfer-Encoding: chunked\r\n
\r\n
5\r\n
hello\r\n
```
И закрывает соединение **без** `0\r\n\r\n`.

**Поток данных:**
1. `readResponseHeaders()` читает заголовки — OK (Content-Length = -1)
2. `streamResponseBody()` устанавливает `PoolReader` с `remain = -1`
3. fasthttp начинает читать тело: вызывает `PoolReader.Read()`
4. `readUntilEOF()` читает `5\r\nhello\r\n` из bufIOReader → OK
5. fasthttp декодирует чанк, пишет клиенту `hello`
6. fasthttp вызывает следующий `PoolReader.Read()`
7. `readUntilEOF()` получает EOF (upstream закрыл)
8. `err != nil` → `CloseAndDropUpstreamConnection()`
9. fasthttp получает EOF без терминатора → **неполный chunked-ответ**

**Результат для клиента:** заголовки уже отправлены (`ImmediateHeaderFlush = true`), клиент получает часть тела. Статус 200, тело усечено. fasthttp может установить 502, если сочтёт ошибку фатальной.

---

## 4. Подтип B: Invalid chunk size

### Что происходит

Upstream отправляет неверный размер чанка (не hex или отрицательное число):
```
HTTP/1.1 200 OK\r\n
Transfer-Encoding: chunked\r\n
\r\n
GG\r\n    ← невалидный hex
hello\r\n
0\r\n\r\n
```

**Поток данных:**
1. Заголовки прочитаны OK
2. Body stream установлен
3. fasthttp читает `GG\r\n` из PoolReader
4. fasthttp пытается распарсить `GG` как hex → ошибка
5. fasthttp... что делает? Скорее всего, закрывает соединение с клиентом или возвращает 502

**Важно:** fasthttp может обработать эту ситуацию по-разному. Тест должен проверить фактическое поведение.

---

## 5. Подтип C: Broken trailer

### Что происходит

Upstream отправляет корректный последний чанк `0\r\n`, затем мусор вместо trailer:
```
HTTP/1.1 200 OK\r\n
Transfer-Encoding: chunked\r\n
\r\n
5\r\n
hello\r\n
0\r\n
GARBAGE\r\n    ← мусор после терминатора
```

**Поток данных:**
1. fasthttp читает `0\r\n` — OK, это конец
2. fasthttp ожидает trailer (или `\r\n`)
3. Получает `GARBAGE\r\n` — ошибка парсинга trailer
4. fasthttp закрывает соединение

---

## 6. План реализации

### 6.1. Файлы

```
internal/proxy/
  ├── handler_network_test.go         — добавить TestHandlerChunked*
  ├── handler_faulty_upstream.go      — добавить FaultChunkedInvalidSize, FaultChunkedBrokenTrailer
  └── mock_conn_ext.go                — уже есть partialReader, resetWriter
```

---

## 7. Критерии приёмки

| # | Критерий                                              | Подтип | Проверка                             |
|---|-------------------------------------------------------|--------|--------------------------------------|
| 1 | Missing terminator → body stream + EOF → CloseAndDrop | A      | `IsBodyStream()`, соединение закрыто |
| 2 | Invalid chunk size → fasthttp не падает               | B      | `handle()` без паники                |
| 3 | Broken trailer → fasthttp не падает                   | C      | `handle()` без паники                |
| 4 | Все тесты проходят с `-race`                          | все    | `go test -race` без data race        |

## 8. Различия с Content-Length under-read

| Аспект         | Content-Length under-read                  | Chunked encoding error         |
|----------------|--------------------------------------------|--------------------------------|
| Content-Length | `>= 0` (фиксированный)                     | `< 0` (chunked)                |
| PoolReader     | `readWithLimit()`                          | `readUntilEOF()`               |
| Соединение     | Возвращается в пул (Release)               | Закрывается (CloseAndDrop)     |
| Ошибка         | EOF до лимита → Release + частичные данные | EOF или ошибка → CloseAndDrop  |
| Риск для пула  | Мёртвое соединение возвращается как живое  | Нет риска (соединение закрыто) |
