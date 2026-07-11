# ChangeLog PR-15 — Оптимизация PipeCopy через sync.Pool

## Сводка

Аллокация 64KB буфера в `PipeCopy` вынесена в `sync.Pool`, исключая повторные выделения памяти при каждом стриминге тела запроса.

## Изменённые файлы

| Файл | Изменения |
|------|-----------|
| `internal/proxy/handler.go` | Добавлен импорт `sync`. Добавлена константа `PipeCopyBufferSize = 64 * 1024`. Добавлен `pipeCopyBufPool` (`sync.Pool` с New-функцией). `PipeCopy` переписана на Get/Put из пула вместо `make([]byte, 64*1024)`. |
| `docs/sync-pool-bytebufferpool.md` | Новый файл — сравнение `sync.Pool` vs `valyala/bytebufferpool` для фиксированного буфера. |

## Ключевые изменения

1. **sync.Pool для PipeCopy**: буфер 64KB аллоцируется один раз и переиспользуется между вызовами, следуя паттерну `copyBufPool` из fasthttp.
2. **Константа `PipeCopyBufferSize`**: размер буфера вынесен в именованную константу.
3. **Документация**: новый файл с обоснованием выбора `sync.Pool` вместо `bytebufferpool`.
