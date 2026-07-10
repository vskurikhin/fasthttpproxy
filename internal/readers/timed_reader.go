package readers

import (
	"io"
	"time"

	"github.com/vskurikhin/fasthttpproxy/internal/metrics"
)

// TimedReader оборачивает io.Reader и записывает время от первого Read до EOF в гистограмму.
type TimedReader struct {
	reader  io.Reader
	started bool
	start   time.Time
}

// NewTimedReader создаёт TimedReader, оборачивающий заданный io.Reader.
func NewTimedReader(r io.Reader) *TimedReader {
	return &TimedReader{reader: r}
}

func (tr *TimedReader) Read(p []byte) (int, error) {
	if !tr.started {
		tr.started = true
		tr.start = time.Now()
	}

	n, err := tr.reader.Read(p)
	if err == io.EOF || (n > 0 && err != nil) {
		metrics.ResponseBodyReadDuration.Observe(time.Since(tr.start).Seconds())
	}
	return n, err
}
