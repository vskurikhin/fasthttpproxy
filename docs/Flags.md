# Флаги командной строки прокси-сервера

Все флаги парсятся в `internal/config/config.go` через `flag.NewFlagSet`.

## Флаги пула соединений (`internal/pool`)

| Флаг             | Тип      | Default | Минимум | Описание                              |
|------------------|----------|---------|---------|---------------------------------------|
| `--idle-timeout` | duration | `45s`   | `1s`    | Таймаут бездействия соединения в пуле |
| `--max-conns`    | int      | `100`   | `1`     | Макс. соединений на upstream          |

## Флаги fasthttp.Server

| Флаг                           | Тип      | Default  | Описание                                              |
|--------------------------------|----------|----------|-------------------------------------------------------|
| `--concurrency`                | int      | `262144` | Макс. одновременных запросов к прокси                 |
| `--max-body-size`              | int      | `0`      | Макс. размер тела запроса в байтах (`0` = без лимита) |
| `--disable-header-norm`        | bool     | `true`   | Отключить нормализацию заголовков                     |
| `--disable-keepalive`          | bool     | `false`  | Отключить keepalive                                   |
| `--disable-preparse-multipart` | bool     | `false`  | Отключить предварительный парсинг multipart           |
| `--get-only`                   | bool     | `false`  | Только GET-запросы                                    |
| `--log-all-errors`             | bool     | `true`   | Логировать все ошибки                                 |
| `--max-conns-per-ip`           | int      | `0`      | Макс. соединений на IP (`0` = без лимита)             |
| `--max-reqs-per-conn`          | int      | `0`      | Макс. запросов на соединение (`0` = без лимита)       |
| `--no-default-content-type`    | bool     | `false`  | Не устанавливать Content-Type по умолчанию            |
| `--no-default-date`            | bool     | `false`  | Не устанавливать Date по умолчанию                    |
| `--no-default-server-header`   | bool     | `true`   | Не устанавливать Server-заголовок                     |
| `--read-buffer-size`           | int      | `0`      | Размер буфера чтения (`0` = default)                  |
| `--read-timeout`               | duration | `0`      | Таймаут чтения (`0` = без лимита)                     |
| `--reduce-memory-usage`        | bool     | `true`   | Режим пониженного потребления памяти                  |
| `--secure-error-log`           | bool     | `true`   | Безопасный лог ошибок                                 |
| `--write-buffer-size`          | int      | `0`      | Размер буфера записи (`0` = default)                  |
| `--write-timeout`              | duration | `0`      | Таймаут записи (`0` = без лимита)                     |

## Флаги конфигурации прокси

| Флаг             | Тип    | Default | Описание                                                                            |
|------------------|--------|---------|-------------------------------------------------------------------------------------|
| `--metrics-addr` | string | `:7070` | Адрес metrics-сервера (Prometheus `/metrics`)                                       |
| `--proxy-addr`   | string | `:8080` | Адрес proxy-сервера                                                                 |
| `--upstreams`    | string | `""`    | Список upstream-серверов через запятую (`http://host:port` или `https://host:port`) |

## Флаги TLS для upstream-соединений

| Флаг                        | Тип    | Default | Описание                                              |
|-----------------------------|--------|---------|-------------------------------------------------------|
| `--tls-enabled`             | bool   | `false` | Включить TLS для upstream-соединений                  |
| `--tls-insecure-skip-verify`| bool   | `false` | Пропускать проверку сертификатов upstream             |
| `--tls-ca-file`             | string | `""`    | Путь к CA-сертификату для проверки upstream           |
| `--tls-server-name`         | string | `""`    | Имя сервера для TLS (SNI)                             |

## Флаги TLS для прокси-сервера

| Флаг                                   | Тип    | Default | Описание                                               |
|----------------------------------------|--------|---------|--------------------------------------------------------|
| `--tls-server-enabled`                 | bool   | `false` | Включить TLS для прокси-сервера                        |
| `--tls-server-certificate-pem-file`    | string | `""`    | Путь к PEM-файлу сертификата сервера                  |
| `--tls-server-key-pem-file`            | string | `""`    | Путь к PEM-файлу ключа сервера                        |

## Пример запуска

```sh
# Минимальный запуск
go run ./cmd/proxy/

# С кастомными upstreams и таймаутами
go run ./cmd/proxy/ \
  --upstreams "192.168.1.1:8080,10.0.0.1:9090" \
  --idle-timeout 60s \
  --max-conns 200 \
  --concurrency 512

# С изменёнными портами и размером тела
go run ./cmd/proxy/ \
  --proxy-addr :8081 \
  --metrics-addr :9090 \
  --max-body-size 10485760

# Режим без метрик (отключает metrics-сервер)
go run ./cmd/proxy/ --metrics-addr ""

# HTTPS upstream с TLS
go run ./cmd/proxy/ \
  --upstreams "https://secure.example.com:443" \
  --tls-enabled \
  --tls-insecure-skip-verify

# Смешанные HTTP/HTTPS upstream
go run ./cmd/proxy/ \
  --upstreams "http://localhost:8080,https://secure.example.com:443" \
  --tls-enabled

# TLS upstream с CA-сертификатом
go run ./cmd/proxy/ \
  --upstreams "https://secure.example.com:443" \
  --tls-enabled \
  --tls-ca-file "/path/to/ca.pem" \
  --tls-server-name "example.com"

# TLS на стороне прокси-сервера
go run ./cmd/proxy/ \
  --tls-server-enabled \
  --tls-server-certificate-pem-file "/path/to/cert.pem" \
  --tls-server-key-pem-file "/path/to/key.pem" \
  --tls-server-name "example.com"
```
