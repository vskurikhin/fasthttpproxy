# 4.4. Тестовые функции — Upstream disconnect mid-response

## Обзор

Реализованы тестовые функции для сценария **Upstream disconnect mid-response** (пункт 4.4 плана). Каждая функция покрывает один подтип сбоя и использует реальный upstream через `startFaultyUpstream`.

Файлы реализации:

- `internal/proxy/handler_network_test.go` — unit-тесты
- `internal/proxy/handler_faulty_upstream_test.go` — upstream-утилиты (`FaultType`, `startFaultyUpstream`)
- `internal/proxy/mock_conn_ext_test.go` — mock-типы (`partialReader`, `resetWriter`, `closeTrackConn`)

---

## Подтип A: Разрыв до завершения заголовков

### `TestHandlerUpstreamCloseImmediate`

**Файл:** `internal/proxy/handler_network_test.go:16`

**Назначение:** Проверяет, что при закрытии upstream без отправки данных прокси возвращает 502 Bad Gateway.

**Сценарий:**
1. Запускается upstream с `FaultCloseImmediate` — принимает соединение и сразу закрывает
2. Вызывается `handle()` через `Handler()`
3. `readResponseHeaders()` вызывает `h.responseHeader.Read(h.bufIOReader)` → получает `io.EOF`
4. `readResponseHeaders()` возвращает `false`
5. `handle()` вызывает `pool.CloseAndDropUpstreamConnection()` и устанавливает **502**

**Проверки:**
- `handle()` не падает
- `ctx.Response.StatusCode() == 502`

### `TestHandlerUpstreamPartialHeaders`

**Файл:** `internal/proxy/handler_network_test.go:41`

**Назначение:** Проверяет, что при частичном заголовке ответа upstream прокси возвращает 502.

**Сценарий:**
1. Запускается upstream с `FaultPartialHeaders` — отправляет `"HTTP/1.1 200 OK\r\n"` и закрывает
2. `readResponseHeaders()` получает неполный заголовок → ошибка парсинга
3. Возвращается `false`, `CloseAndDropUpstreamConnection`, **502**

**Проверки:**
- `ctx.Response.StatusCode() == 502`

---

## Подтип B1: Content-Length under-read

### `TestHandlerUpstreamContentLengthUnderread`

**Файл:** `internal/proxy/handler_network_test.go:69`

**Назначение:** Проверяет поведение прокси при неполном теле ответа upstream: заголовки с `Content-Length: 100`, отправлено 50 байт, затем закрытие.

**Сценарий:**
1. Запускается upstream с `FaultContentLengthUnderread` — отправляет полный заголовок с `Content-Length: 100`, 50 байт тела, закрывает
2. `readResponseHeaders()` успешно читает заголовки
3. `copyResponseStatus()` копирует статус (200)
4. `streamResponseBody()` устанавливает `PoolReader(remain=100)`
5. `ctx.Response.Body()` читает тело — PoolReader.readWithLimit получает EOF при `remain=50`, возвращает соединение в пул

**Проверки:**
- `handle()` не падает
- `ctx.Response.IsBodyStream() == true`
- `ctx.Response.ImmediateHeaderFlush == true`
- `len(body) < 100` (неполное тело)
- `len(body) > 0` (первые 50 байт прочитаны)

---

## Подтип B2: Chunked disconnect

### `TestHandlerUpstreamChunkedDisconnect`

**Файл:** `internal/proxy/handler_network_test.go:113`

**Назначение:** Проверяет, что при chunked-ответе без терминатора прокси корректно устанавливает body stream и соединение закрывается при EOF.

**Сценарий:**
1. Запускается upstream с `FaultChunkedDisconnect` — отправляет chunked-заголовок, один чанк `"hello"`, закрывает без `0\r\n\r\n`
2. `readResponseHeaders()` успешно читает заголовки (chunked)
3. `streamResponseBody()` устанавливает `PoolReader(remain=-1)`
4. `ctx.Response.Body()` читает тело — PoolReader.readUntilEOF получает EOF → `CloseAndDropUpstreamConnection`

**Проверки:**
- `handle()` не падает
- `ctx.Response.IsBodyStream() == true`
- `ctx.Response.ImmediateHeaderFlush == true`
- Тело может быть пустым (fasthttp отбрасывает неполный chunked-чанк)

---

## Табличный тест

### `TestNetworkFailures`

**Файл:** `internal/proxy/handler_network_test.go:145`

**Назначение:** Табличный (table-driven) тест, запускающий все 4 сценария как субтесты с `t.Run`.

| Субтест                             | FaultType                     | Проверка                        |
|-------------------------------------|-------------------------------|---------------------------------|
| `upstream_close_immediate`          | `FaultCloseImmediate`         | `status == 502`                 |
| `upstream_partial_headers`          | `FaultPartialHeaders`         | `status == 502`                 |
| `upstream_content_length_underread` | `FaultContentLengthUnderread` | body stream + `len(body) < 100` |
| `upstream_chunked_disconnect`       | `FaultChunkedDisconnect`      | body stream                     |

---

## Типы сбоев (handler_faulty_upstream_test.go)

Четыре типа сбоя в `FaultType`:

| Константа                     | Поведение upstream                                            | Симулирует                                                      |
|-------------------------------|---------------------------------------------------------------|-----------------------------------------------------------------|
| `FaultCloseImmediate`         | Принять соединение, закрыть без данных                        | Пустой ответ (io.EOF при чтении заголовков)                     |
| `FaultPartialHeaders`         | Отправить `"HTTP/1.1 200 OK\r\n"`, закрыть                    | Частичный заголовок (ошибка парсинга)                           |
| `FaultContentLengthUnderread` | Отправить `200 OK + Content-Length: 100 + 50 байт`, закрыть   | Upstream оборвал соединение до завершения тела (Content-Length) |
| `FaultChunkedDisconnect`      | Отправить chunked-заголовок + 1 чанк, закрыть без `0\r\n\r\n` | Upstream оборвал chunked-ответ без терминатора                  |

Функция `startFaultyUpstream` запускает TCP-сервер, реализующий выбранный сбой.

---

## Покрытие точек сбоя

| Точка сбоя                               | Тест                                        | Подтип |
|------------------------------------------|---------------------------------------------|--------|
| 6c. Connection closed before headers     | `TestHandlerUpstreamCloseImmediate`         | A1     |
| 6d. Partial header read                  | `TestHandlerUpstreamPartialHeaders`         | A2     |
| 8d. Content-Length mismatch (under-read) | `TestHandlerUpstreamContentLengthUnderread` | B1     |
| 8e. Chunked encoding error               | `TestHandlerUpstreamChunkedDisconnect`      | B2     |

## Вспомогательные файлы

| Файл                         | Назначение                                                                |
|------------------------------|---------------------------------------------------------------------------|
| `handler_faulty_upstream.go` | Утилиты: `startFaultyUpstream`, `startPartialUpstream`, типы `FaultType`  |
| `mock_conn_ext.go`           | Расширенные mock-типы: `partialReader`, `resetWriter`, `closeTrackConn`   |

