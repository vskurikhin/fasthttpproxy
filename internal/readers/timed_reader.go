package readers

import (
	"io"
	"log"
	"net"
	"time"

	"github.com/vskurikhin/fasthttpproxy/internal/metrics"
)

// TimedReader оборачивает io.Reader и записывает время от первого Read до EOF в гистограмму.
type TimedReader struct {
	connection net.Conn
	reader     io.Reader
	size       int64
	started    bool
	start      time.Time
}

// NewTimedReader создаёт TimedReader, оборачивающий заданный io.Reader.
func NewTimedReader(r io.Reader, connection net.Conn) *TimedReader {
	return &TimedReader{connection: connection, reader: r}
}

func (tr *TimedReader) Read(p []byte) (int, error) {
	if !tr.started {
		tr.started = true
		tr.start = time.Now()
	}

	n, err := tr.reader.Read(p)
	tr.size += int64(n)
	if err == io.EOF || (n > 0 && err != nil) {
		since := time.Since(tr.start)
		metrics.ResponseBodyReadDuration.Observe(since.Seconds())
		log.Printf("connection: %v, size: %d, timed reader finished in %s", tr.connection, tr.size, since)
	}
	return n, err
}
