# fasthttpproxy

Быстрый HTTP reverse proxy на `valyala/fasthttp` со стримингом, пулом соединений, метриками Prometheus и поддержкой прокси-диалера (HTTP CONNECT / SOCKS5).

---

## Архитектура

```
cmd/proxy/main.go
├── internal/config/      — Парсинг CLI-флагов, единая структура Values
├── internal/metrics/     — Prometheus-счётчики и гистограммы
├── internal/upstream/    — Список upstream-серверов со случайным выбором
├── internal/pool/        — Пул TCP-соединений (per-upstream, лимиты/таймауты)
│   └── dial.go           — HTTP-прокси диалер через fasthttpproxy
├── internal/proxy/       — Стриминговый proxy handler (7 методов, SRP)
│   └── handler.go        — Оркестрация запросов: resolve → acquire → write → read → stream
├── internal/readers/     — PoolReader (жизненный цикл соединения) + TimedReader (метрики)
└── fasthttpproxy/        — SOCKS5/HTTP proxy dialers (из fasthttp upstream)
```

---

## Возможности

- **Сквозной стриминг**: тело запроса — `PipeCopy` (64KB буфер), тело ответа — `PoolReader` + `ImmediateHeaderFlush`.
- **Пул соединений**: per-upstream пул с LIFO-извлечением, idle timeout (по умолч. 45s), макс. соединений (по умолч. 100).
- **Метрики Prometheus**: counters (dial errors, close errors, write/read errors, upstream 5xx, idle drops) и histograms (длительность записи/чтения тела).
- **5xx → 502**: upstream 5xx ответы заменяются на 502 Bad Gateway.
- **Выбор upstream**: из CLI-списка (рандом) или динамически из Host заголовка.
- **Прокси-диалер**: поддержка HTTP CONNECT и SOCKS5 через `fasthttpproxy` (из fasthttp).

---

## CLI-флаги

Все флаги парсятся через `--` (long form). Полный список:

| Флаг | По умолчанию | Описание |
|------|-------------|----------|
| `--concurrency` | 262144 | Макс. одновременных запросов |
| `--dialer-timeout` | 30s | Таймаут TCP-диалера к upstream |
| `--idle-timeout` | 45s | Таймаут бездействия соединения в пуле (мин. 1s) |
| `--max-conns` | 100 | Макс. соединений на upstream (мин. 1) |
| `--max-body-size` | 4 MiB | Макс. размер тела запроса |
| `--max-conns-per-ip` | 0 (без лимита) | Макс. соединений на IP |
| `--max-reqs-per-conn` | 0 (без лимита) | Макс. запросов на соединение |
| `--metrics-addr` | `:7070` | Адрес metrics HTTP-сервера |
| `--proxy-addr` | `:8080` | Адрес proxy HTTP-сервера |
| `--read-buffer-size` | 0 (fasthttp default) | Размер буфера чтения |
| `--write-buffer-size` | 0 (fasthttp default) | Размер буфера записи |
| `--read-timeout` | 0 (без лимита) | Таймаут чтения |
| `--write-timeout` | 0 (без лимита) | Таймаут записи |
| `--upstreams` | `""` | Список upstream через запятую (host:port) |
| `--disable-header-norm` | true | Отключить нормализацию заголовков |
| `--disable-keepalive` | false | Отключить keepalive |
| `--disable-preparse-multipart` | false | Отключить предпарсинг multipart |
| `--get-only` | false | Только GET-запросы |
| `--log-all-errors` | true | Логировать все ошибки |
| `--no-default-content-type` | false | Не устанавливать Content-Type по умолч. |
| `--no-default-date` | false | Не устанавливать Date по умолч. |
| `--no-default-server-header` | true | Не устанавливать Server-заголовок |
| `--reduce-memory-usage` | true | Режим пониженного потребления памяти |
| `--secure-error-log` | true | Безопасный лог ошибок |

---

## Быстрый старт

```sh
# Запуск с одним upstream
./cmd/proxy/fasthttpproxy-server --upstreams "127.0.0.1:8081"

# Запуск с несколькими upstream и кастомным таймаутом
./cmd/proxy/fasthttpproxy-server \
  --upstreams "10.0.0.1:3000,10.0.0.2:3000" \
  --dialer-timeout 10s \
  --idle-timeout 60s \
  --max-conns 200
```

---

## Разработка

### Команды

```sh
go test -shuffle=on ./...           # все тесты
go test -race -shuffle=on ./...     # с детектором гонок
golangci-lint run --verbose         # линтер
gosec -exclude=G103,G104,G304,G402 ./...  # безопасность
go test -bench=. -benchmem ./...    # бенчмарки
```

### Структура пакетов

- `cmd/proxy/` — точка входа, парсинг флагов, запуск сервера
- `internal/config/` — конфигурация (Values, ParseFlags)
- `internal/metrics/` — Prometheus-метрики
- `internal/pool/` — пул соединений (pool.go + upstream_pool.go + dial.go)
- `internal/proxy/` — стриминговый handler (handler.go)
- `internal/readers/` — PoolReader + TimedReader
- `internal/upstream/` — селектор upstream-серверов
- `fasthttpproxy/` — SOCKS5/HTTP proxy dialers (из fasthttp upstream)

### Ключевые особенности реализации

- `internal/pool/pool.go` — базовые типы, сеттеры, dialNew
- `internal/pool/upstream_pool.go` — глобальный пул, Acquire/Release/CloseAndDrop
- `internal/pool/dial.go` — кастомный dial через fasthttpproxy или fasthttp.Dial
- `internal/proxy/handler.go` — 7 методов по SRP, оркестрация handle()
- `internal/readers/pool_reader.go` — возврат/закрытие соединения после чтения тела
- `internal/readers/timed_reader.go` — замер длительности чтения ответа
