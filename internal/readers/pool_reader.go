package readers

import (
	"io"
	"net"

	"github.com/vskurikhin/fasthttpproxy/internal/pool"
)

// PoolReader оборачивает io.Reader и возвращает соединение в пул после EOF.
type PoolReader struct {
	reader       io.Reader
	upstreamAddr string
	connection   net.Conn
	returned     bool
}

// NewPoolReader создаёт PoolReader, оборачивающий заданный io.Reader.
func NewPoolReader(r io.Reader, upstreamAddr string, conn net.Conn) *PoolReader {
	return &PoolReader{
		reader:       r,
		upstreamAddr: upstreamAddr,
		connection:   conn,
	}
}

func (pr *PoolReader) Read(p []byte) (int, error) {
	n, err := pr.reader.Read(p)
	if !pr.returned && (err == io.EOF || (n > 0 && err != nil)) {
		pr.returned = true
		pool.Put(pr.upstreamAddr, pr.connection)
	}
	return n, err
}
