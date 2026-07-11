# ChangeLog PR-17 — Пулы bufio/PipeCopy + CLI-флаги для размеров буферов

## Сводка

Создан `internal/pool/bufio_pool.go` с пулами `bufIOWriterPool`, `bufIOReaderPool`, `pipeCopyBufPool` и функциями `AcquireBufIOWriter`/`ReleaseBufIOWriter`, `AcquireBufIOReader`/`ReleaseBufIOReader`, `PipeCopy`. Размеры буферов (`defaultBufSize`, `pipeCopyBufferSize`) настраиваются через CLI-флаги `--io-buffers-size` и `--copy-buffers-size`. В `handler.go` все локальные вызовы bufio и PipeCopy заменены на вызовы пулов. В `pool_reader.go` добавлен `onDone`-колбэк для освобождения bufio.Reader.

## Ключевые изменения

| Изменение                   | Файл                                   | Описание                                                                                                 |
|-----------------------------|----------------------------------------|----------------------------------------------------------------------------------------------------------|
| Создание bufio_pool.go      | `internal/pool/bufio_pool.go`          | Новый файл с тремя sync.Pool, acquire/release функциями и setter-функциями BufferSize/PipeCopyBufferSize |
| Создание bufio_pool_test.go | `internal/pool/bufio_pool_test.go`     | Тесты PipeCopy, acquire/release и setter-функций                                                         |
| Флаги --io-buffers-size     | `internal/config/config.go`            | Добавлен флаг (default 4096, min 64), поле Values.IOBuffersSize                                          |
| Флаги --copy-buffers-size   | `internal/config/config.go`            | Добавлен флаг (default 65536, min 256), поле Values.CopyBuffersSize                                      |
| Тесты флагов                | `internal/config/config_test.go`       | TestParseFlagsWithIOBuffersSize, TestParseFlagsWithCopyBuffersSize                                       |
| Вызов pool в runWith        | `cmd/proxy/main.go`                    | Добавлены pool.BufferSize и pool.PipeCopyBufferSize                                                      |
| Интеграционные тесты        | `cmd/proxy/main_test.go`               | TestPoolBufferSizeIntegration, TestPoolCopyBuffersSizeIntegration                                        |
| Пул bufio.Writer            | `internal/proxy/handler.go`            | `bufio.NewWriter` → `pool.AcquireBufIOWriter` + `defer pool.ReleaseBufIOWriter`                          |
| Пул bufio.Reader            | `internal/proxy/handler.go`            | `bufio.NewReader` → `pool.AcquireBufIOReader`                                                            |
| Пул PipeCopy                | `internal/proxy/handler.go`            | `PipeCopy` → `pool.PipeCopy`, удалена локальная функция и `pipeCopyBufPool`                              |
| Prefix через config         | `internal/proxy/handler.go`            | `"http://"` → `config.PrefixHTTP`, `"https://"` → `config.PrefixHTTPS`                                   |
| Убраны старые тесты         | `internal/proxy/handler_test.go`       | Удалены `TestPipeCopy*` и вспомогательные типы (`writerConn`, `errReader`)                               |
| onDone в PoolReader         | `internal/readers/pool_reader.go`      | Добавлено поле `onDone`, вызов в `readWithLimit` и `readUntilEOF`                                        |
| cleanup параметр            | `internal/readers/pool_reader.go`      | `NewPoolReader` принимает `cleanup func()`                                                               |
| Обновление тестов           | `internal/readers/pool_reader_test.go` | Все вызовы `NewPoolReader` дополнены параметром `nil`                                                    |

## Изменённые файлы

| Файл                                   | Статус      |
|----------------------------------------|-------------|
| `internal/pool/bufio_pool.go`          | ✅ Created  |
| `internal/pool/bufio_pool_test.go`     | ✅ Created  |
| `internal/config/config.go`            | ✅ Modified |
| `internal/config/config_test.go`       | ✅ Modified |
| `cmd/proxy/main.go`                    | ✅ Modified |
| `cmd/proxy/main_test.go`               | ✅ Modified |
| `internal/proxy/handler.go`            | ✅ Modified |
| `internal/proxy/handler_test.go`       | ✅ Modified |
| `internal/readers/pool_reader.go`      | ✅ Modified |
| `internal/readers/pool_reader_test.go` | ✅ Modified |
