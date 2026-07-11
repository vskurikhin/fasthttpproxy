package readers

import (
	"io"
	"net"

	"github.com/vskurikhin/fasthttpproxy/internal/pool"
)

// PoolReader оборачивает io.Reader и управляет возвратом соединения в пул.
//
// Поведение зависит от параметра remain:
//   - remain >= 0 (Content-Length): после чтения ровно remain байт
//     соединение возвращается в пул (pool.ReleaseUpstreamConnection). Соединение считается живым.
//   - remain < 0 (chunked/identity): при EOF соединение закрывается,
//     так как upstream явно закрыл сокет.
type PoolReader struct {
	reader       io.Reader
	upstreamAddr string
	connection   net.Conn
	returned     bool
	remain       int64  // сколько ещё байт прочитать; < 0 = читать до EOF
	onDone       func() // вызывается после возврата/закрытия соединения
}

// NewPoolReader создаёт PoolReader, оборачивающий заданный io.Reader.
//
// Параметр remain задаёт стратегию возврата соединения:
// положительное значение — лимит байт для Content-Length;
// отрицательное — чтение до EOF (chunked/identity), при EOF соединение закрывается.
//
// cleanup — функция, вызываемая после завершения чтения (освобождение ресурсов).
func NewPoolReader(r io.Reader, upstreamAddr string, conn net.Conn, remain int64, cleanup func()) *PoolReader {
	return &PoolReader{
		reader:       r,
		upstreamAddr: upstreamAddr,
		connection:   conn,
		remain:       remain,
		onDone:       cleanup,
	}
}

func (pr *PoolReader) Read(p []byte) (int, error) {
	if pr.remain >= 0 {
		return pr.readWithLimit(p)
	}
	return pr.readUntilEOF(p)
}

// readWithLimit читает не более remain байт. После достижения лимита
// соединение возвращается в пул (считается живым при Content-Length).
//
// Поведение на границе лимита (как у io.LimitReader):
//   - если лимит исчерпан ровно на этом Read — возвращаем (n, nil) без EOF;
//     при следующем Read — (0, io.EOF).
//   - если лимит уже был 0 — (0, io.EOF).
func (pr *PoolReader) readWithLimit(p []byte) (int, error) {
	if pr.remain == 0 {
		return 0, io.EOF
	}
	if int64(len(p)) > pr.remain {
		p = p[:pr.remain]
	}

	n, err := pr.reader.Read(p)
	pr.remain -= int64(n)

	// Достигли лимита — возвращаем соединение в пул
	if pr.remain <= 0 && !pr.returned {
		pr.returned = true
		pool.ReleaseUpstreamConnection(pr.upstreamAddr, pr.connection)
		if pr.onDone != nil {
			pr.onDone()
		}
	}

	// Ошибка до достижения лимита — всё равно возвращаем (коннект мог умереть)
	if err != nil && !pr.returned {
		pr.returned = true
		pool.ReleaseUpstreamConnection(pr.upstreamAddr, pr.connection)
		if pr.onDone != nil {
			pr.onDone()
		}
	}

	// Если лимит исчерпан и нет ошибки — ведём себя как io.LimitReader:
	// первый Read после исчерпания лимита возвращает (n, nil),
	// второй Read (remain == 0) вернёт (0, io.EOF).
	return n, err
}

// readUntilEOF читает до EOF. При EOF или любой ошибке соединение
// закрывается — upstream явно закрыл сокет, коннект мёртв.
func (pr *PoolReader) readUntilEOF(p []byte) (int, error) {
	n, err := pr.reader.Read(p)

	// Закрываем соединение при EOF или любой ошибке (даже с n == 0).
	// Исключение: nil-ошибка без EOF — соединение ещё живо, продолжаем.
	if !pr.returned && err != nil {
		pr.returned = true
		pool.CloseAndDropUpstreamConnection(pr.upstreamAddr, pr.connection)
		if pr.onDone != nil {
			pr.onDone()
		}
	}

	return n, err
}
