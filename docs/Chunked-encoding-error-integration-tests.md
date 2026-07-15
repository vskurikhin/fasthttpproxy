# Интеграционные тесты: Chunked encoding error

## 6.4. Интеграционные тесты

Интеграционные тесты используют полный цикл Handler с реальным faulty upstream.
Расположены в `internal/proxy/handler_integration_test.go`.

### TestIntegrationChunkedInvalidSize

```go
func TestIntegrationChunkedInvalidSize(t *testing.T) {
```

| Параметр             | Значение                                   |
|----------------------|--------------------------------------------|
| FaultType            | `FaultChunkedInvalidSize`                  |
| Заголовок            | `Transfer-Encoding: chunked`               |
| Тело                 | `GG\r\nhello\r\n0\r\n\r\n`                 |
| Статус               | 200 (заголовки скопированы до чтения тела) |
| BodyStream           | установлен                                 |
| ImmediateHeaderFlush | включён                                    |

Проверяет, что при неверном hex-размере чанка через полный цикл Handler:
- `handle()` не падает
- `ctx.Response.IsBodyStream() == true`
- `ctx.Response.ImmediateHeaderFlush == true`

### TestIntegrationChunkedBrokenTrailer

```go
func TestIntegrationChunkedBrokenTrailer(t *testing.T) {
```

| Параметр             | Значение                         |
|----------------------|----------------------------------|
| FaultType            | `FaultChunkedBrokenTrailer`      |
| Заголовок            | `Transfer-Encoding: chunked`     |
| Тело                 | `5\r\nhello\r\n0\r\nGARBAGE\r\n` |
| Статус               | 200                              |
| BodyStream           | установлен                       |
| ImmediateHeaderFlush | включён                          |

Проверяет, что при мусоре после терминатора через полный цикл Handler:
- `handle()` не падает
- `ctx.Response.IsBodyStream() == true`
- `ctx.Response.ImmediateHeaderFlush == true`

### Новые fault-типы

Добавлены в `FaultType` в `handler_faulty_upstream_test.go`:

| FaultType                   | Заголовок                      | Данные                           | Поведение                 |
|-----------------------------|--------------------------------|----------------------------------|---------------------------|
| `FaultChunkedInvalidSize`   | `Transfer-Encoding: chunked`   | `GG\r\nhello\r\n0\r\n\r\n`       | неверный hex размер чанка |
| `FaultChunkedBrokenTrailer` | `Transfer-Encoding: chunked`   | `5\r\nhello\r\n0\r\nGARBAGE\r\n` | мусор после терминатора   |

Оба используют `startFaultyUpstream` — TCP-сервер, который отправляет предопределённый ответ и закрывает соединение.

### Критерии приёмки

| # | Критерий                                              | Подтип | Проверка                             |
|---|-------------------------------------------------------|--------|--------------------------------------|
| 1 | Missing terminator → body stream + EOF → CloseAndDrop | A      | `IsBodyStream()`, соединение закрыто |
| 2 | Invalid chunk size → fasthttp не падает               | B      | `handle()` без паники                |
| 3 | Broken trailer → fasthttp не падает                   | C      | `handle()` без паники                |
| 4 | Все тесты проходят с `-race`                          | все    | `go test -race` без data race        |
