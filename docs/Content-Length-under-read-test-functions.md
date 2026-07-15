# Тестовые функции: Content-Length under-read

## 5.2. Тестовые функции

### 5.2.1. PoolReader unit-тесты (internal/readers/pool_reader_test.go)

Все тесты используют `partialReader` (отдаёт N байт, затем err) и `closeTrackConn` (отслеживает вызов Close).

#### TestPoolReaderContentLengthUnderreadEOF (Подтип A1)

```go
func TestPoolReaderContentLengthUnderreadEOF(t *testing.T) {
```

| Параметр         | Значение                                                                   |
|------------------|----------------------------------------------------------------------------|
| Content-Length   | 100                                                                        |
| Отправлено       | 50 байт (`abcdefghijklmnopqrstuvwxyz0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ`) |
| Ошибка           | `io.EOF`                                                                   |
| Ожидание         | `n == 50, err == io.EOF`                                                   |

Проверяет, что при under-read + EOF на половине тела PoolReader возвращает частичные данные. Соединение возвращается в пул (Release, не CloseAndDrop).

#### TestPoolReaderContentLengthUnderreadZeroBody (Подтип A2)

```go
func TestPoolReaderContentLengthUnderreadZeroBody(t *testing.T) {
```

| Параметр         | Значение                |
|------------------|-------------------------|
| Content-Length   | 100                     |
| Отправлено       | 0 байт                  |
| Ошибка           | `io.EOF`                |
| Ожидание         | `n == 0, err == io.EOF` |

Проверяет under-read без единого байта тела.

#### TestPoolReaderContentLengthUnderreadSevere (Подтип A3)

```go
func TestPoolReaderContentLengthUnderreadSevere(t *testing.T) {
```

| Параметр       | Значение                                                            |
|----------------|---------------------------------------------------------------------|
| Content-Length | 100                                                                 |
| Отправлено     | 99 байт (`'x' * 99`)                                                |
| Ошибка         | `io.EOF`                                                            |
| Ожидание       | 1-й Read: `n == 64, err == nil`; 2-й Read: `n == 35, err == io.EOF` |

Проверяет under-read почти в конце — первый Read читает 64 байта без ошибки, второй читает остальные 35 с EOF.

#### TestPoolReaderContentLengthUnderreadRST (Подтип B)

```go
func TestPoolReaderContentLengthUnderreadRST(t *testing.T) {
```

| Параметр       | Значение                              |
|----------------|---------------------------------------|
| Content-Length | 100                                   |
| Отправлено     | 50 байт                               |
| Ошибка         | `io.ErrUnexpectedEOF`                 |
| Ожидание       | `n == 50, err == io.ErrUnexpectedEOF` |

Проверяет under-read + RST (connection reset by peer).

---

### 5.2.2. Handler network-тесты (internal/proxy/handler_network_test.go)

#### TestHandlerContentLengthUnderreadVariants

Табличный тест, запускает три подтипа через реальный faulty upstream:

| Вариант                 | FaultType                             | Проверка                                          |
|-------------------------|---------------------------------------|---------------------------------------------------|
| `underread_50_of_100`   | `FaultContentLengthUnderread`         | body stream установлен, body < 100, body != empty |
| `underread_zero_body`   | `FaultContentLengthUnderreadZeroBody` | body stream установлен, body < 100                |
| `underread_99_of_100`   | `FaultContentLengthUnderread99`       | body stream установлен, body < 100, body != empty |

Для каждого варианта:
1. `ResetUpstreams()` — очистка пула
2. `startFaultyUpstream(t, fault)` — запуск faulty upstream
3. `Handler` — прокси-обработчик
4. Проверка: `ctx.Response.IsBodyStream()`, `ImmediateHeaderFlush`, `len(Body())`
