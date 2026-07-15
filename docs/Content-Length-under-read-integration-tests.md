# Интеграционные тесты: Content-Length under-read

## 5.4. Интеграционные тесты

Интеграционные тесты используют полный цикл Handler с реальным faulty upstream.
Расположены в `internal/proxy/handler_integration_test.go`.

### TestIntegrationContentLengthUnderreadZeroBody

```go
func TestIntegrationContentLengthUnderreadZeroBody(t *testing.T) {
```

| Параметр             | Значение                                   |
|----------------------|--------------------------------------------|
| FaultType            | `FaultContentLengthUnderreadZeroBody`      |
| Content-Length       | 100                                        |
| Тело                 | 0 байт                                     |
| Статус               | 200 (заголовки скопированы до чтения тела) |
| BodyStream           | установлен                                 |
| ImmediateHeaderFlush | включён                                    |

Проверяет, что при under-read без единого байта тела:
- `handle()` не падает
- `ctx.Response.IsBodyStream()` == true
- `ctx.Response.ImmediateHeaderFlush` == true

### TestIntegrationContentLengthUnderreadSevere

```go
func TestIntegrationContentLengthUnderreadSevere(t *testing.T) {
```

| Параметр             | Значение                        |
|----------------------|---------------------------------|
| FaultType            | `FaultContentLengthUnderread99` |
| Content-Length       | 100                             |
| Тело                 | 99 байт (`'x' * 99`)            |
| Статус               | 200                             |
| BodyStream           | установлен                      |
| ImmediateHeaderFlush | включён                         |

Проверяет, что при under-read почти в конце:
- `handle()` не падает
- `ctx.Response.IsBodyStream()` == true
- `ctx.Response.ImmediateHeaderFlush` == true
- `len(ctx.Response.Body()) < 100`
- `len(ctx.Response.Body()) > 0`

### Новые fault-типы

Добавлены в `FaultType` в `handler_faulty_upstream_test.go`:

| FaultType                             | Content-Length | Отправлено байт | Поведение                                     |
|---------------------------------------|----------------|-----------------|-----------------------------------------------|
| `FaultContentLengthUnderreadZeroBody` | 100            | 0               | отправляет заголовки, закрывает без тела      |
| `FaultContentLengthUnderread99`       | 100            | 99              | отправляет заголовки, 99 байт тела, закрывает |

Оба используют `startFaultyUpstream` — TCP-сервер, который отправляет предопределённый ответ и закрывает соединение.

### Критерии приёмки

| # | Критерий                                                | Подтип | Проверка                              |
|---|---------------------------------------------------------|--------|---------------------------------------|
| 1 | Content-Length: 100, тело 50 → EOF + частичное чтение   | A1     | `n == 50, err == io.EOF`              |
| 2 | Content-Length: 100, тело 0 → EOF без данных            | A2     | `n == 0, err == io.EOF`               |
| 3 | Content-Length: 100, RST на 50 → ErrUnexpectedEOF       | B      | `n == 50, err == io.ErrUnexpectedEOF` |
| 4 | handle() не падает при under-read                       | A/B    | `handle()` возвращается без паники    |
| 5 | `ctx.Response.Body()` < Content-Length                  | A/B    | `len(Body()) < 100`                   |
| 6 | Соединение возвращается в пул (текущее поведение)       | A/B    | `ReleaseUpstreamConnection` вызван    |
| 7 | Все тесты проходят с `-race`                            | все    | `go test -race` без data race         |
