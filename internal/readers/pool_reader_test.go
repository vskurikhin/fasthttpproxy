package readers

import (
	"errors"
	"io"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestPoolReaderDelegatesRead(t *testing.T) {
	pr := NewPoolReader(strings.NewReader("hello"), "example.com:8080", &dummyConn{}, -1)

	buf := make([]byte, 64)
	n, err := pr.Read(buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 5 {
		t.Fatalf("expected 5 bytes, got %d", n)
	}
}

func TestPoolReaderReturnsEOF(t *testing.T) {
	pr := NewPoolReader(strings.NewReader(""), "example.com:8080", &dummyConn{}, -1)

	buf := make([]byte, 64)
	_, err := pr.Read(buf)
	if err != io.EOF {
		t.Fatalf("expected EOF, got %v", err)
	}
}

func TestPoolReaderReturnsError(t *testing.T) {
	expectedErr := errors.New("read error")
	pr := NewPoolReader(&errReader{err: expectedErr}, "example.com:8080", &dummyConn{}, -1)

	buf := make([]byte, 64)
	_, err := pr.Read(buf)
	if err != expectedErr {
		t.Fatalf("expected %v, got %v", expectedErr, err)
	}
}

func TestPoolReaderMultipleReads(t *testing.T) {
	pr := NewPoolReader(strings.NewReader("hello"), "example.com:8080", &dummyConn{}, -1)

	buf := make([]byte, 2)
	n, err := pr.Read(buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 2 {
		t.Fatalf("expected 2 bytes, got %d", n)
	}

	n, err = pr.Read(buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 2 {
		t.Fatalf("expected 2 bytes, got %d", n)
	}

	n, err = pr.Read(buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 byte, got %d", n)
	}

	_, err = pr.Read(buf)
	if err != io.EOF {
		t.Fatalf("expected EOF, got %v", err)
	}
}

// --- Тесты для Content-Length (remain >= 0) ---

func TestPoolReaderContentLengthReturnsToPool(t *testing.T) {
	// При remain >= 0 после чтения лимита соединение должно быть
	// возвращено в пул (живое), а не закрыто.
	var closed atomic.Bool
	conn := &closeTrackConn{closeFn: func() { closed.Store(true) }}

	pr := NewPoolReader(strings.NewReader("hello"), "example.com:8080", conn, 5)

	buf := make([]byte, 64)
	n, err := pr.Read(buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 5 {
		t.Fatalf("expected 5 bytes, got %d", n)
	}

	// После чтения 5 байт должен быть EOF (лимит исчерпан)
	_, err = pr.Read(buf)
	if err != io.EOF {
		t.Fatalf("expected EOF after limit, got %v", err)
	}

	// Соединение НЕ должно быть закрыто — оно живое и возвращено в пул
	if closed.Load() {
		t.Fatal("expected connection to stay open (returned to pool), but it was closed")
	}
}

func TestPoolReaderContentLengthPartialRead(t *testing.T) {
	// Чтение меньшего количества, чем remain, затем ещё один Read должен дать EOF
	var closed atomic.Bool
	conn := &closeTrackConn{closeFn: func() { closed.Store(true) }}

	pr := NewPoolReader(strings.NewReader("hello"), "example.com:8080", conn, 5)

	buf := make([]byte, 2)
	n, err := pr.Read(buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 2 {
		t.Fatalf("expected 2 bytes, got %d", n)
	}

	n, err = pr.Read(buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 2 {
		t.Fatalf("expected 2 bytes, got %d", n)
	}

	n, err = pr.Read(buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 byte, got %d", n)
	}

	_, err = pr.Read(buf)
	if err != io.EOF {
		t.Fatalf("expected EOF after limit, got %v", err)
	}

	if closed.Load() {
		t.Fatal("expected connection to stay open (returned to pool), but it was closed")
	}
}

func TestPoolReaderContentLengthExactLimit(t *testing.T) {
	// Чтение ровно remain байт за один вызов.
	// Первый Read возвращает (n, nil), второй — (0, io.EOF).
	var closed atomic.Bool
	conn := &closeTrackConn{closeFn: func() { closed.Store(true) }}

	pr := NewPoolReader(strings.NewReader("abc"), "example.com:8080", conn, 3)

	buf := make([]byte, 64)
	n, err := pr.Read(buf)
	if err != nil {
		t.Fatalf("expected no error after reading exactly limit, got: %v", err)
	}
	if n != 3 {
		t.Fatalf("expected 3 bytes, got %d", n)
	}

	// Второй Read должен вернуть EOF (лимит исчерпан)
	_, err = pr.Read(buf)
	if err != io.EOF {
		t.Fatalf("expected EOF on second read, got: %v", err)
	}

	if closed.Load() {
		t.Fatal("expected connection to stay open (returned to pool), but it was closed")
	}
}

// --- Тесты для chunked/identity (remain < 0) ---

func TestPoolReaderChunkedClosesOnEOF(t *testing.T) {
	// При remain < 0 на EOF соединение должно закрываться (upstream закрыл сокет)
	var closed atomic.Bool
	conn := &closeTrackConn{closeFn: func() { closed.Store(true) }}

	pr := NewPoolReader(strings.NewReader(""), "example.com:8080", conn, -1)

	buf := make([]byte, 64)
	_, err := pr.Read(buf)
	if err != io.EOF {
		t.Fatalf("expected EOF, got %v", err)
	}

	if !closed.Load() {
		t.Fatal("expected connection to be closed on EOF (server closed it), but it was not")
	}
}

func TestPoolReaderChunkedClosesOnError(t *testing.T) {
	// При remain < 0 на ошибке с частичным чтением соединение закрывается
	var closed atomic.Bool
	conn := &closeTrackConn{closeFn: func() { closed.Store(true) }}

	pr := NewPoolReader(&errReader{err: io.ErrUnexpectedEOF}, "example.com:8080", conn, -1)

	buf := make([]byte, 64)
	_, err := pr.Read(buf)
	if err != io.ErrUnexpectedEOF {
		t.Fatalf("expected ErrUnexpectedEOF, got %v", err)
	}

	if !closed.Load() {
		t.Fatal("expected connection to be closed on error (server closed it), but it was not")
	}
}

func TestPoolReaderChunkedReadThenEOF(t *testing.T) {
	// Читаем данные, потом EOF — соединение должно закрыться
	var closed atomic.Bool
	conn := &closeTrackConn{closeFn: func() { closed.Store(true) }}

	pr := NewPoolReader(strings.NewReader("hello"), "example.com:8080", conn, -1)

	buf := make([]byte, 64)
	n, err := pr.Read(buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 5 {
		t.Fatalf("expected 5 bytes, got %d", n)
	}

	// Второй Read должен вернуть EOF и закрыть соединение
	_, err = pr.Read(buf)
	if err != io.EOF {
		t.Fatalf("expected EOF, got %v", err)
	}

	if !closed.Load() {
		t.Fatal("expected connection to be closed on EOF, but it was not")
	}
}

// --- Вспомогательные типы ---

type errReader struct {
	err error
}

func (e *errReader) Read(p []byte) (int, error) {
	return 0, e.err
}

// closeTrackConn — net.Conn, которая отслеживает вызов Close.
type closeTrackConn struct {
	closeFn func()
}

func (c *closeTrackConn) Close() error {
	if c.closeFn != nil {
		c.closeFn()
	}
	return nil
}
func (c *closeTrackConn) Read(b []byte) (int, error)         { return 0, io.EOF }
func (c *closeTrackConn) Write(b []byte) (int, error)        { return len(b), nil }
func (c *closeTrackConn) LocalAddr() net.Addr                { return nil }
func (c *closeTrackConn) RemoteAddr() net.Addr               { return nil }
func (c *closeTrackConn) SetDeadline(t time.Time) error      { return nil }
func (c *closeTrackConn) SetReadDeadline(t time.Time) error  { return nil }
func (c *closeTrackConn) SetWriteDeadline(t time.Time) error { return nil }

// dummyConn implements net.Conn with no-op methods for testing PoolReader.
type dummyConn struct{}

func (d *dummyConn) Close() error                       { return nil }
func (d *dummyConn) Read(b []byte) (int, error)         { return 0, io.EOF }
func (d *dummyConn) Write(b []byte) (int, error)        { return len(b), nil }
func (d *dummyConn) LocalAddr() net.Addr                { return nil }
func (d *dummyConn) RemoteAddr() net.Addr               { return nil }
func (d *dummyConn) SetDeadline(t time.Time) error      { return nil }
func (d *dummyConn) SetReadDeadline(t time.Time) error  { return nil }
func (d *dummyConn) SetWriteDeadline(t time.Time) error { return nil }
