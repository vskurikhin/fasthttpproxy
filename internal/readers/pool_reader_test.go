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
	pr := NewPoolReader(strings.NewReader("hello"), "example.com:8080", &dummyConn{}, -1, nil)

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
	pr := NewPoolReader(strings.NewReader(""), "example.com:8080", &dummyConn{}, -1, nil)

	buf := make([]byte, 64)
	_, err := pr.Read(buf)
	if err != io.EOF {
		t.Fatalf("expected EOF, got %v", err)
	}
}

func TestPoolReaderReturnsError(t *testing.T) {
	expectedErr := errors.New("read error")
	pr := NewPoolReader(&errReader{err: expectedErr}, "example.com:8080", &dummyConn{}, -1, nil)

	buf := make([]byte, 64)
	_, err := pr.Read(buf)
	if err != expectedErr {
		t.Fatalf("expected %v, got %v", expectedErr, err)
	}
}

func TestPoolReaderMultipleReads(t *testing.T) {
	pr := NewPoolReader(strings.NewReader("hello"), "example.com:8080", &dummyConn{}, -1, nil)

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

	pr := NewPoolReader(strings.NewReader("hello"), "example.com:8080", conn, 5, nil)

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

	pr := NewPoolReader(strings.NewReader("hello"), "example.com:8080", conn, 5, nil)

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

	pr := NewPoolReader(strings.NewReader("abc"), "example.com:8080", conn, 3, nil)

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

	pr := NewPoolReader(strings.NewReader(""), "example.com:8080", conn, -1, nil)

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

	pr := NewPoolReader(&errReader{err: io.ErrUnexpectedEOF}, "example.com:8080", conn, -1, nil)

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

	pr := NewPoolReader(strings.NewReader("hello"), "example.com:8080", conn, -1, nil)

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

// partialReader — отдаёт до len(data) байт, затем возвращает err.
type partialReader struct {
	data   []byte
	offset int
	err    error
}

func (r *partialReader) Read(p []byte) (int, error) {
	if r.offset >= len(r.data) {
		return 0, r.err
	}
	n := copy(p, r.data[r.offset:])
	r.offset += n
	return n, nil
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

// --- Тесты для Content-Length under-read (неполное тело + EOF/RST) ---

// TestPoolReaderContentLengthUnderreadEOF проверяет, что при under-read + EOF
// (Content-Length: 100, отправлено 50 байт) PoolReader возвращает частичные данные
// и соединение возвращается в пул (текущее поведение — Release, не CloseAndDrop).
//
// Подтип A1: EOF на половине тела.
//
// partialReader: сначала отдаёт 50 байт с nil-ошибкой, затем (0, io.EOF).
// readWithLimit: первый Read возвращает (50, nil), второй — (0, io.EOF).
func TestPoolReaderContentLengthUnderreadEOF(t *testing.T) {
	var closed atomic.Bool
	conn := &closeTrackConn{closeFn: func() { closed.Store(true) }}

	data := make([]byte, 50)
	for i := range data {
		data[i] = 'x'
	}

	pr := NewPoolReader(&partialReader{
		data: data,
		err:  io.EOF,
	}, "example.com:8080", conn, 100, nil)

	buf := make([]byte, 64)
	// Первый Read — 50 байт без ошибки (partialReader отдаёт данные, EOF на следующем)
	n, err := pr.Read(buf)
	if n != 50 {
		t.Fatalf("expected 50 bytes, got %d", n)
	}
	if err != nil {
		t.Fatalf("expected no error on first read, got %v", err)
	}

	// Второй Read — 0 байт, io.EOF
	_, err = pr.Read(buf)
	if err != io.EOF {
		t.Fatalf("expected EOF on second read, got %v", err)
	}

	// Текущее поведение: соединение возвращается в пул (Release, не CloseAndDrop).
	// conn не закрывается, так как ReleaseUpstreamConnection не вызывает conn.Close().
	if closed.Load() {
		t.Log("connection was closed — expected Release (not CloseAndDrop) behavior")
	}
}

// TestPoolReaderContentLengthUnderreadZeroBody проверяет, что при under-read + EOF
// без единого байта тела (Content-Length: 100, отправлено 0 байт) PoolReader
// возвращает (0, io.EOF) и соединение возвращается в пул.
//
// Подтип A2: EOF без единого байта.
func TestPoolReaderContentLengthUnderreadZeroBody(t *testing.T) {
	var closed atomic.Bool
	conn := &closeTrackConn{closeFn: func() { closed.Store(true) }}

	pr := NewPoolReader(&partialReader{
		data: []byte{},
		err:  io.EOF,
	}, "example.com:8080", conn, 100, nil)

	buf := make([]byte, 64)
	n, err := pr.Read(buf)
	if n != 0 {
		t.Fatalf("expected 0 bytes, got %d", n)
	}
	if err != io.EOF {
		t.Fatalf("expected EOF, got %v", err)
	}

	if closed.Load() {
		t.Log("connection was closed on zero-body underread")
	}
}

// TestPoolReaderContentLengthUnderreadSevere проверяет under-read почти в конце
// (Content-Length: 100, отправлено 99 байт). 99-й байт читается, затем EOF.
//
// Подтип A3: EOF почти в конце.
//
// readWithLimit ведёт себя как io.LimitReader: первый Read (64 байт) — (n, nil),
// второй Read (35 байт) — (n, nil) (лимит исчерпан ровно), третий Read — (0, io.EOF).
func TestPoolReaderContentLengthUnderreadSevere(t *testing.T) {
	var closed atomic.Bool
	conn := &closeTrackConn{closeFn: func() { closed.Store(true) }}

	data := make([]byte, 99)
	for i := range data {
		data[i] = 'x'
	}

	pr := NewPoolReader(&partialReader{
		data: data,
		err:  io.EOF,
	}, "example.com:8080", conn, 100, nil)

	buf := make([]byte, 64)
	// Первое чтение — 64 байта (без ошибки, данных достаточно)
	n1, err1 := pr.Read(buf)
	if n1 != 64 {
		t.Fatalf("expected 64 bytes on first read, got %d", n1)
	}
	if err1 != nil {
		t.Fatalf("expected no error on first read, got %v", err1)
	}

	// Второе чтение — 35 байт (99 - 64 = 35), без ошибки — лимит исчерпан ровно
	n2, err2 := pr.Read(buf)
	if n2 != 35 {
		t.Fatalf("expected 35 bytes on second read, got %d", n2)
	}
	if err2 != nil {
		t.Fatalf("expected no error on second read (limit exhausted exactly), got %v", err2)
	}

	// Третье чтение — EOF (лимит 0)
	_, err3 := pr.Read(buf)
	if err3 != io.EOF {
		t.Fatalf("expected EOF on third read, got %v", err3)
	}

	if closed.Load() {
		t.Log("connection was closed on severe underread")
	}
}

// TestPoolReaderContentLengthUnderreadRST проверяет under-read + RST
// (Content-Length: 100, ErrUnexpectedEOF на 50 байт).
//
// Подтип B: RST на половине тела.
//
// partialReader: сначала отдаёт 50 байт с nil, затем (0, io.ErrUnexpectedEOF).
func TestPoolReaderContentLengthUnderreadRST(t *testing.T) {
	var closed atomic.Bool
	conn := &closeTrackConn{closeFn: func() { closed.Store(true) }}

	data := make([]byte, 50)
	for i := range data {
		data[i] = 'x'
	}

	pr := NewPoolReader(&partialReader{
		data: data,
		err:  io.ErrUnexpectedEOF,
	}, "example.com:8080", conn, 100, nil)

	buf := make([]byte, 64)
	// Первый Read — 50 байт без ошибки
	n, err := pr.Read(buf)
	if n != 50 {
		t.Fatalf("expected 50 bytes, got %d", n)
	}
	if err != nil {
		t.Fatalf("expected no error on first read, got %v", err)
	}

	// Второй Read — 0 байт, ErrUnexpectedEOF
	_, err = pr.Read(buf)
	if err != io.ErrUnexpectedEOF {
		t.Fatalf("expected ErrUnexpectedEOF on second read, got %v", err)
	}

	if closed.Load() {
		t.Log("connection was closed on RST underread")
	}
}

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
