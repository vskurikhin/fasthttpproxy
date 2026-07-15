# Тестовые функции: Chunked encoding error

## 6.3. Тестовые функции

Все тесты расположены в `internal/proxy/handler_network_test.go`.

### TestHandlerChunkedInvalidSize (Подтип B)

```go
func TestHandlerChunkedInvalidSize(t *testing.T) {
```

| Параметр  | Значение                                               |
|-----------|--------------------------------------------------------|
| FaultType | `FaultChunkedInvalidSize`                              |
| Заголовок | `Transfer-Encoding: chunked`                           |
| Тело      | `GG\r\nhello\r\n0\r\n\r\n`                             |
| Ошибка    | fasthttp не может распарсить `GG` как hex-размер чанка |

Проверяет:
- `handle()` не падает
- `ctx.Response.IsBodyStream() == true`
- `ctx.Response.ImmediateHeaderFlush == true`

Статус может быть 200 (заголовки отправлены до чтения тела) или 502 (fasthttp сфаталил ошибку).

### TestHandlerChunkedBrokenTrailer (Подтип C)

```go
func TestHandlerChunkedBrokenTrailer(t *testing.T) {
```

| Параметр  | Значение                                          |
|-----------|---------------------------------------------------|
| FaultType | `FaultChunkedBrokenTrailer`                       |
| Заголовок | `Transfer-Encoding: chunked`                      |
| Тело      | `5\r\nhello\r\n0\r\nGARBAGE\r\n`                  |
| Ошибка    | fasthttp получает мусор после терминатора `0\r\n` |

Проверяет:
- `handle()` не падает
- `ctx.Response.IsBodyStream() == true`
- `ctx.Response.ImmediateHeaderFlush == true`

### TestChunkedEncodingErrors

Табличный тест, объединяющий все три подтипа:

| Вариант              | FaultType                   | Проверка                             |
|----------------------|-----------------------------|--------------------------------------|
| `missing_terminator` | `FaultChunkedDisconnect`    | body stream + ImmediateHeaderFlush   |
| `invalid_chunk_size` | `FaultChunkedInvalidSize`   | body stream + ImmediateHeaderFlush   |
| `broken_trailer`     | `FaultChunkedBrokenTrailer` | body stream + ImmediateHeaderFlush   |

Для каждого варианта:
1. `ResetUpstreams()` — очистка пула
2. `startFaultyUpstream(t, fault)` — запуск faulty upstream
3. `Handler` — прокси-обработчик
4. Проверка: `IsBodyStream()`, `ImmediateHeaderFlush`

### TestNetworkFailures (дополнен)

В существующий табличный тест добавлены два новых варианта:
- `chunked_invalid_size` → `FaultChunkedInvalidSize`
- `chunked_broken_trailer` → `FaultChunkedBrokenTrailer`
