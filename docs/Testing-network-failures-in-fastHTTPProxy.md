# Тестирование сетевых сбоев в fastHTTPProxy

## 1. Какой инструмент выбрать для тестирования?

### Рекомендация: комбинация стандартного `testing` + рукописные mock-соединения + утилиты для инъекции сбоев

Проект уже использует стандартный пакет `testing` и не имеет сторонних тестовых зависимостей (нет testify, gomock и т.д.). Это осознанный выбор автора, обеспечивающий минимальные требования к сборке. Добавление внешнего фреймворка увеличит время `go build` и усложнит CI.

**Предлагаемый стек:**

| Инструмент                   | Назначение                                              | Обоснование                                                                              |
|------------------------------|---------------------------------------------------------|------------------------------------------------------------------------------------------|
| `testing` (стандартный)      | Каркас тестов, запуск, субтесты (`t.Run`)               | Уже используется, zero-зависимость                                                       |
| `net/http/httptest`          | Запуск реального HTTP-сервера для интеграционных тестов | Уже используется в тестах (через `net.Listen`), но httptest даёт больше контроля         |
| Mock-соединения (рукописные) | Инъекция ошибок на уровне TCP/IO                        | Паттерн уже реализован: `mockConn`, `errWriter`, `shortWriter`, `errReader`              |
| `context` + `time.After`     | Симуляция таймаутов                                     | Встроенный Go-механизм                                                                   |
| `golang.org/x/sync/errgroup` | Контролируемый запуск параллельных сбоев                | Лёгкая зависимость, уже может быть в go.sum                                              |
| Chaos-прокси (опционально)   | Сетевые сбои на уровне ядра (потери пакетов, задержки)  | Для end-to-end тестов под Linux — `tc` (traffic control); под macOS — `pfctl` + dummynet |

**Почему не testify/gomock/etc.:**
- Проект следует принципу «минимум зависимостей»
- Все mock-объекты просты (5-15 строк), testify не даёт выигрыша
- Добавление testify в `go.mod` — изменение публичного API модуля

**Почему не специализированные фреймворки вроде `go-fault`:**
- Они не поддерживаются в Go 1.26 (на момент анализа)
- Вносят накладные расходы в рантайм
- Не дают контроля над конкретными точками сбоя на уровне потока данных

### Альтернатива для углублённого тестирования: `go test -exec` с обёрткой для network chaos

Можно написать скрипт-обёртку, который перехватывает `net.Dial` через `LD_PRELOAD` (Linux) или `DYLD_INSERT_LIBRARIES` (macOS). Но это избыточно для текущего проекта — проще контролировать сбои на уровне кода.

---

## 2. Как написать сценарии тестирования?

### Классификация сетевых сбоев для прокси

Сетевой сбой может произойти в любой точке жизненного цикла прокси-соединения. Для каждой точки нужно определить сценарий.

#### Точки сбоя (Failure Points)

```
Client → [PROXY] → Upstream
            │
            ├── 1. Приём соединения от клиента (fasthttp.Server)
            ├── 2. Выбор upstream (resolveUpstream)
            ├── 3. AcquireUpstreamConnection (dial / pool.Get)
            │       ├── 3a. Dial timeout
            │       ├── 3b. Connection refused
            │       ├── 3c. DNS resolution failure
            │       ├── 3d. TLS handshake failure
            │       ├── 3e. Pool limit exceeded
            │       └── 3f. Idle connection expired mid-handshake
            ├── 4. WriteRequestHeaders
            │       ├── 4a. Partial header write
            │       ├── 4b. Connection reset during write
            │       └── 4c. Flush failure
            ├── 5. WriteRequestBody
            │       ├── 5a. Connection reset mid-body
            │       ├── 5b. Upstream closes before body complete
            │       ├── 5c. PipeCopy read error (client stream fails)
            │       └── 5d. PipeCopy write error (upstream fails)
            ├── 6. ReadResponseHeaders
            │       ├── 6a. Timeout waiting for headers
            │       ├── 6b. Malformed HTTP response
            │       ├── 6c. Connection closed before headers
            │       └── 6d. Partial header read
            ├── 7. CopyResponseStatus
            │       ├── 7a. 5xx upstream → 502 to client
            │       └── 7b. Header copy failure
            └── 8. StreamResponseBody
                    ├── 8a. Upstream disconnects mid-body
                    ├── 8b. Client disconnects (proxy continues reading)
                    ├── 8c. Slow upstream (client timeout)
                    ├── 8d. Content-Length mismatch (too much/too little data)
                    └── 8e. Chunked encoding error
```

#### Сценарии тестирования (Test Scenarios)

Каждый сценарий должен быть оформлен как отдельная функция с `t.Run`:

```go
func TestNetworkFailures(t *testing.T) {
    tests := []struct {
        name    string
        inject  func(*handler, net.Conn)  // функция инъекции сбоя
        verify  func(*testing.T, *fasthttp.Response)  // проверка результата
    }{
        {name: "dial_timeout",       inject: injectDialTimeout,       verify: verify502},
        {name: "conn_refused",       inject: injectConnRefused,      verify: verify502},
        {name: "header_write_rst",   inject: injectHeaderWriteRST,   verify: verify502},
        {name: "body_write_reset",   inject: injectBodyWriteReset,   verify: verify502},
        {name: "upstream_5xx",       inject: injectUpstream5xx,      verify: verify502},
        {name: "upstream_disconnect",inject: injectUpstreamDisconnect, verify: verify502},
        // ...
    }
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            // ...
        })
    }
}
```

**Принципы именования:**
- `Test<Component><FailureScenario>` — например, `TestHandlerDialTimeout`, `TestPoolGetConnRefused`
- Для каждого компонента свой файл: `handler_network_test.go`, `pool_network_test.go`

**Покрытие потоков данных (Data Flow Coverage):**
- Каждый путь через `handle()` должен быть протестирован со сбоем
- Учтены как streaming paths (PipeCopy), так и non-streaming paths (фиксированный body)
- Учтены keepalive vs close paths (Content-Length vs chunked)

---

## 3. Как реализовать сценарии тестирования?

### Уровни реализации

#### Уровень 1: Unit-тесты с mock-соединениями (рекомендуется)

Используем существующие mock-типы, расширяем их для новых сценариев.

```go
// Расширение mockConn для сценариев сброса соединения
type mockConn struct {
    net.Conn
    reader    *bytes.Buffer
    writer    io.Writer  // может быть заменён на errWriter/shortWriter/resetWriter
    closeFn   func()
    closed    atomic.Bool
    // Новые поля:
    writeHook func([]byte) (int, error)  // для динамического контроля записи
    readHook  func([]byte) (int, error)  // для динамического контроля чтения
}

// resetWriter — имитация TCP RST: после N байт соединение «рвётся»
type resetWriter struct {
    max     int64
    written int64
    err     error  // симулируем ECONNRESET
}

func (w *resetWriter) Write(p []byte) (int, error) {
    avail := w.max - w.written
    if avail <= 0 {
        return 0, w.err
    }
    if int64(len(p)) > avail {
        n, _ := w.writeWithLimit(p[:avail])
        return n, w.err  // частичная запись + RST
    }
    n, _ := w.writeWithLimit(p)
    return n, nil
}
```

**Пример реализации сценария «Upstream disconnect mid-body»:**

```go
func TestStreamResponseBodyUpstreamDisconnect(t *testing.T) {
    h, ctx := newHandlerTest(t)
    defer resetUpstreams()

    // upstream отвечает частично, затем рвёт соединение
    body := []byte("partial body, then boom")
    partial := body[:10]  // только первые 10 байт

    upstream := startPartialUpstream(t, string(partial))  // отправляет 10 байт, закрывает
    h.upstreams.Set("http://" + upstream.Addr())

    req := newGETRequest(upstream.Addr())
    reqCtx := newRequestCtx(t, req)

    err := h.handle(reqCtx)
    // Ожидаем: ошибка чтения ответа, 502 Bad Gateway клиенту
    if err == nil {
        t.Fatal("expected error from upstream disconnect")
    }
    // Проверяем статус ответа
    resp := reqCtx.Response()
    if resp.StatusCode() != 502 {
        t.Fatalf("expected 502, got %d", resp.StatusCode())
    }
}
```

#### Уровень 2: Интеграционные тесты с реальным TCP-сервером

Для сценариев, где нужен настоящий TCP (RST, таймауты, медленные соединения):

```go
// startFaultyUpstream — upstream, который симулирует конкретный сбой
func startFaultyUpstream(t *testing.T, fault FaultType) net.Listener {
    ln, err := net.Listen("tcp", "127.0.0.1:0")
    if err != nil { t.Fatal(err) }

    go func() {
        conn, err := ln.Accept()
        if err != nil { return }
        simulateFault(conn, fault)  // блокирует/сбрасывает/задерживает
    }()
    return ln
}
```

#### Уровень 3: End-to-end тесты с сетевым chaos (опционально)

Для полной проверки поведения прокси под реальными сетевыми аномалиями:

```bash
# Пример использования tc (Linux) для симуляции:
# tc qdisc add dev lo root netem loss 10% delay 100ms
# go test -run TestEndToEnd ./...
# tc qdisc del dev lo root
```

### Шаблон реализации для каждого типа сбоя

| Тип сбоя                | Реализация                                        | Уровень          |
|-------------------------|---------------------------------------------------|------------------|
| Dial timeout            | `customDial` возвращает `fasthttp.ErrDialTimeout` | Unit             |
| Connection refused      | Закрыть listener до dial                          | Unit/Integration |
| DNS failure             | Передать невалидный хост                          | Unit             |
| TLS failure             | Настроить `tls.Config` с неверным сертификатом    | Integration      |
| Pool limit              | Установить MaxConns=0, вызвать Acquire            | Unit             |
| Header write RST        | `resetWriter` на upstream conn                    | Unit             |
| Flush error             | `errWriter` на bufio.Writer.Flush                 | Unit             |
| Body write RST          | `resetWriter` после N байт                        | Unit             |
| PipeCopy error          | `errReader` на client body stream                 | Unit             |
| Header read timeout     | `slowReader` с задержкой > таймаута               | Integration      |
| Malformed response      | `invalidHTTPWriter`                               | Unit             |
| Upstream 5xx            | Вернуть 502/503                                   | Unit/Integration |
| Upstream disconnect     | Закрыть conn после частичного ответа              | Integration      |
| Client disconnect       | Закрыть client conn после частичного чтения       | Integration      |
| Slow upstream           | Задержка между пакетами                           | Integration      |
| Content-Length mismatch | Вернуть больше/меньше байт, чем Content-Length    | Unit/Integration |

### Организация кода

Новые файлы:

```
internal/proxy/
  ├── handler_network_test.go    # Сценарии сбоев для handler.handle()
  ├── handler_faulty_upstream.go # Утилиты: startFaultyUpstream и т.д.
  └── mock_conn_ext.go          # Расширенные mockConn (resetWriter, slowReader и т.д.)

internal/pool/
  └── pool_network_test.go       # Сценарии сбоев для connection pool

internal/readers/
  └── pool_reader_network_test.go # Сценарии сбоев для PoolReader
```

### Работа с контекстом и таймаутами

```go
// Для сценариев с таймаутом используем:
ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
defer cancel()

// В mock-соединении проверяем ctx.Done():
func (s *slowReader) Read(p []byte) (int, error) {
    select {
    case <-s.ctx.Done():
        return 0, s.ctx.Err()
    case <-time.After(s.delay):
        // симулируем медленное чтение
    }
}
```

---

## 4. Как построить отчёты для этих сценариев тестирования?

### Уровень 1: Встроенный `go test` output

**Минимальный, но достаточный.** Все сценарии запускаются через:

```bash
go test -v -race -shuffle=on -count=1 ./internal/proxy/ -run TestNetwork
```

Вывод: verbose-лог каждого субтеста + итоговая статистика (`ok`/`FAIL`).

**Рекомендуемые флаги:**
- `-v` — verbose: виден каждый сценарий
- `-race` — data race detection (критично для streaming-кода)
- `-shuffle=on` — порядок не детерминирован, выявляет скрытые гонки
- `-count=1` — без кэширования результатов
- `-timeout 120s` — защита от зависших тестов

### Уровень 2: JSON-вывод (`go test -json`)

```bash
go test -json -race -shuffle=on -count=1 ./internal/proxy/ -run TestNetwork > results.json
```

Можно обработать через `jq` или написать простой анализатор:

```bash
# Количество пройденных/упавших сценариев
jq 'select(.Action == "fail") | .Test' results.json | wc -l
jq 'select(.Action == "pass") | .Test' results.json | wc -l
```

### Уровень 3: Prometheus-метрики внутри тестов (для детального анализа)

**Мотивация:** в проекте уже есть `internal/metrics` с Prometheus-метриками. Можно экспортировать метрики сбоев внутри тестов и получить количественную картину.

```go
func TestNetworkFailuresWithMetrics(t *testing.T) {
    // Создаём реестр метрик для теста
    reg := prometheus.NewRegistry()
    m := metrics.NewMetrics(reg)

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            // ...
            // После завершения сценария проверяем метрики:
            // m.FailedRequestsTotal, m.ErrorCount и т.д.
        })
    }

    // Экспорт метрик в файл:
    f, _ := os.Create("test_metrics.prom")
    reg.WriteText(f, nil)
    f.Close()
}
```

**Какие метрики добавить для тестирования сбоев:**

| Метрика                 | Тип       | Метки                       | Описание                                                            |
|-------------------------|-----------|-----------------------------|---------------------------------------------------------------------|
| `test_failure_total`    | Counter   | `scenario`, `failure_point` | Количество запусков каждого сценария                                |
| `test_failure_duration` | Histogram | `scenario`                  | Время обработки сбоя (latency)                                      |
| `test_failure_recovery` | Gauge     | `scenario`                  | 1 если сбой обработан корректно, 0 если ошибка прорвалась к клиенту |

### Уровень 4: Отчёт в формате Markdown/HTML (CI-friendly)

Скрипт-обработчик `go test -json` → сводный отчёт:

```bash
#!/bin/bash
# generate_report.sh
go test -json -race -shuffle=on -count=1 ./internal/proxy/ -run TestNetwork | \
    python3 -c "
import json, sys

passed = 0
failed = 0
details = []
for line in sys.stdin:
    if line.startswith('{'):
        d = json.loads(line)
        if d.get('Action') == 'pass':
            passed += 1
        elif d.get('Action') == 'fail':
            failed += 1
            details.append(f'- ❌ {d[\"Test\"]}')

print(f'# Результаты тестирования сетевых сбоев')
print(f'- **Пройдено:** {passed}')
print(f'- **Упало:** {failed}')
print(f'- **Всего:** {passed + failed}')
if details:
    print()
    print('## Детали по упавшим тестам')
    for d in details:
        print(d)
"
```

### Рекомендуемая структура CI для тестирования сбоев

В `.github/workflows/test.yml` добавить шаг:

```yaml
- name: Network failure tests
  run: |
    go test -race -shuffle=on -count=1 -timeout 120s \
      -run 'TestNetwork' \
      ./internal/proxy/ ./internal/pool/ ./internal/readers/ \
      -json | tee network-test-results.json
- name: Upload network test results
  uses: actions/upload-artifact@v4
  with:
    name: network-test-results
    path: network-test-results.json
- name: Network test summary
  run: |
    go install github.com/jstemmer/go-junit-report@latest
    go test -race -shuffle=on -count=1 -timeout 120s \
      -run 'TestNetwork' \
      ./internal/proxy/ ./internal/pool/ ./internal/readers/ \
      2>&1 | go-junit-report > network-test-report.xml
```

---

## Итоговая таблица рекомендаций

| Вопрос          | Ответ                                                            | Обоснование                                                      |
|-----------------|------------------------------------------------------------------|------------------------------------------------------------------|
| 1. Инструмент   | `testing` + рукописные mock-типы + `net/http/httptest`           | Zero-зависимости, паттерн уже используется, достаточный контроль |
| 2. Сценарии     | Таблица сценариев для каждой точки сбоя (8 групп, ~25 сценариев) | Каждая точка handle() должна быть покрыта                        |
| 3. Реализация   | Расширение mockConn, startFaultyUpstream, субтесты с `t.Run`     | Единый стиль с существующими тестами, минимум нового кода        |
| 4. Отчёты       | `go test -json` + Prometheus-метрики в тестах + CI-артефакты     | Три уровня детализации: быстрый просмотр, метрики, CI            |

### Первоочередные сценарии (рекомендуется реализовать в первую очередь)

1. [x] **Upstream disconnect mid-response** — самый частый сбой в production
2. [x] **Client disconnect during streaming** — прокси должен корректно завершить upstream
3. [x] **Dial timeout with pool full** — комбинация двух условий
4. [x] **TLS handshake failure** — если используется HTTPS upstream
5. [x] **Content-Length under-read** — upstream не досылает обещанное количество байт
6. [ ] **Chunked encoding error** — upstream присылает некорректный chunked trailer
