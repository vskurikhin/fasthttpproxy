# Анализ кода и покрытия тестами

## 1. Проблемы в коде

### 1.1. Дублирование тестов в handler_test.go

Файл `internal/proxy/handler_test.go` содержит копии интеграционных тестов, которые уже есть в `handler_integration_test.go`:

| Функция                                         | handler_test.go | handler_integration_test.go |
|-------------------------------------------------|-----------------|-----------------------------|
| `TestIntegrationUpstreamCloseImmediate`         | строка 970      | строка 21                   |
| `TestIntegrationUpstreamPartialHeaders`         | строка 995      | строка 46                   |
| `TestIntegrationUpstreamContentLengthUnderread` | строка 1022     | строка 73                   |
| `TestIntegrationUpstreamChunkedDisconnect`      | строка 1061     | строка 244                  |
| `TestCopyResponseStatusNilHeader`               | строка 1091     | строка 583                  |

**Риск:** дубли создают неоднозначность — изменения в одном файле не синхронизируются с другим. LSP показывает ошибки «redeclared in this block».

### 1.2. Устаревшие имена в pool-тестах

`pool_test.go` и `upstream_pool_test.go` используют `SetMaxConnsForTest`, который был переименован в `SetMaxUpstreamConnectionsForTest`. Это вызывает ошибки компиляции (undefined).

### 1.3. Баг в PoolReader.readWithLimit: under-read возвращает соединение в пул

`internal/readers/pool_reader.go:78-84`:
```go
if err != nil && !pr.returned {
    pr.returned = true
    pool.ReleaseUpstreamConnection(pr.upstreamAddr, pr.connection)  // Release, не CloseAndDrop!
}
```

При under-read (EOF или RST до достижения `remain`) соединение возвращается в пул как живое, хотя upstream закрыл сокет. Следующий запрос получит ошибку `write: broken pipe`.

**Затронутые подтипы:**
- Content-Length under-read (все варианты A1-A4, B)
- Client disconnect during Content-Length response (B1)

### 1.4. Отсутствие проверки IsClientDisconnect

После `streamResponseBody()` контроль над чтением тела полностью переходит к fasthttp. Прокси не проверяет `ctx.IsClientDisconnect()` и не может остановить чтение из upstream при обрыве клиента. Это приводит к:
- Wasted upstream bandwidth при обрыве клиента во время стриминга ответа
- Возврат соединения в пул с непрочитанными данными для Content-Length

---

## 2. Failure Points не покрытые интеграционными тестами

Из 22 Failure Points, перечисленных в `docs/Analysis-Step-20-1-streaming-fasthttpproxy-testing-My-DeepSeek-V4-Flash.md` (раздел 2), часть не имеет интеграционных тестов.

### 2.1. Failure Points, покрытые интеграционными тестами

| ID | Failure Point                                | Интеграционный тест                                                                                                                                    |
|----|----------------------------------------------|--------------------------------------------------------------------------------------------------------------------------------------------------------|
| 3a | Dial timeout                                 | `TestIntegrationDialTimeout`                                                                                                                           |
| 3e | Pool limit exceeded                          | `TestIntegrationPoolFull`                                                                                                                              |
| 3d | TLS handshake failure                        | `TestIntegrationTLSHandshakeFailure`, `TestIntegrationTLSInvalidCertificate`                                                                           |
| 6c | Connection closed before headers             | `TestIntegrationUpstreamCloseImmediate`                                                                                                                |
| 6d | Partial header read                          | `TestIntegrationUpstreamPartialHeaders`                                                                                                                |
| 8a | Upstream disconnects mid-body                | `TestIntegrationUpstreamContentLengthUnderread`, `TestIntegrationContentLengthUnderreadZeroBody`, `TestIntegrationContentLengthUnderreadSevere`        |
| 8b | Client disconnects (proxy continues reading) | `TestIntegrationClientDisconnectRequestBody`, `TestIntegrationClientDisconnectResponseContentLength`, `TestIntegrationClientDisconnectResponseChunked` |
| 8d | Content-Length mismatch (too little data)    | `TestIntegrationUpstreamContentLengthUnderread`, `TestIntegrationContentLengthUnderreadZeroBody`, `TestIntegrationContentLengthUnderreadSevere`        |
| 8e | Chunked encoding error                       | `TestIntegrationUpstreamChunkedDisconnect`, `TestIntegrationChunkedInvalidSize`, `TestIntegrationChunkedBrokenTrailer`                                 |

### 2.2. Failure Points НЕ покрытые интеграционными тестами

| ID       | Failure Point                             | Описание                                          | Есть ли unit-тест                                                                                        |
|----------|-------------------------------------------|---------------------------------------------------|----------------------------------------------------------------------------------------------------------|
| **3b**   | Connection refused                        | Dial к неактивному порту                          | Да: `TestHandlerFullCycleDialError` (handler_test.go)                                                    |
| **3c**   | DNS resolution failure                    | Невалидный hostname                               | Нет                                                                                                      |
| **3f**   | Idle connection expired mid-handshake     | Соединение в пуле умерло до использования         | Нет                                                                                                      |
| **4a**   | Partial header write                      | Частичная запись заголовков upstream              | Нет                                                                                                      |
| **4b**   | Connection reset during write             | RST во время записи заголовков                    | Нет                                                                                                      |
| **4c**   | Flush failure                             | Ошибка при bufio.Flush()                          | Да: `TestWriteRequestHeadersFlushError`, `TestWriteRequestHeadersFlushErrorControlled` (handler_test.go) |
| **5a**   | Connection reset mid-body                 | RST во время записи тела запроса                  | Нет                                                                                                      |
| **5b**   | Upstream closes before body complete      | Upstream закрывает соединение до полного тела     | Нет                                                                                                      |
| **5c**   | PipeCopy read error (client stream fails) | Ошибка чтения клиентского body stream             | Да: `TestHandlerClientDisconnectRequestBody` (handler_network_test.go)                                   |
| **5d**   | PipeCopy write error (upstream fails)     | Ошибка записи в upstream при PipeCopy             | Да: `TestWriteRequestBodyStreamPipeCopyError` (handler_test.go)                                          |
| **6a**   | Timeout waiting for headers               | Таймаут при ожидании заголовков ответа            | Нет                                                                                                      |
| **6b**   | Malformed HTTP response                   | Битый HTTP-ответ от upstream                      | Да: `TestReadResponseHeadersError` (handler_test.go)                                                     |
| **7a**   | 5xx upstream → 502 to client              | Upstream вернул 502/503                           | Да: `TestHandlerFullCycleUpstream5xx` (handler_test.go)                                                  |
| **7b**   | Header copy failure                       | Ошибка копирования заголовков ответа              | Нет                                                                                                      |
| **8c**   | Slow upstream (client timeout)            | Медленный upstream, клиент уходит по таймауту     | Нет                                                                                                      |
| **8d**   | Content-Length mismatch: too much data    | Upstream отправил больше байт, чем Content-Length | Нет                                                                                                      |

### 2.3. Итого

- **Покрыто интеграционными тестами:** 9 Failure Points из 22 (41%)
- **Не покрыто интеграционными тестами:** 13 Failure Points из 22 (59%)
- **Из 13 непокрытых:**
  - 7 имеют unit-тесты (3b, 4c, 5c, 5d, 6b, 7a)
  - 6 не имеют вообще никаких тестов (3c, 3f, 4a, 4b, 5a, 5b, 6a, 7b, 8c, 8d-overread)

### 2.4. Приоритетные для добавления

| Приоритет | Failure Point                                  | Обоснование                            |
|-----------|------------------------------------------------|----------------------------------------|
| Высокий   | 8c — Slow upstream (client timeout)            | Часто встречается в production         |
| Высокий   | 8d — Content-Length mismatch: too much data    | Обратная сторона under-read            |
| Средний   | 4c — Flush failure                             | Критично для TLS (handshake при flush) |
| Средний   | 5c — PipeCopy read error (client stream fails) | Уже есть unit, нужна интеграция        |
| Средний   | 7a — 5xx upstream → 502                        | Уже есть unit, нужна интеграция        |
| Низкий    | 3b — Connection refused                        | Простой сценарий                       |
| Низкий    | 6a — Timeout waiting for headers               | Требует slowReader                     |
| Низкий    | 4a, 4b, 5a, 5b                                 | Редкие сценарии                        |
