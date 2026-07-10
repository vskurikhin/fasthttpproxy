package readers

import (
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

func TestPoolReaderDelegatesRead(t *testing.T) {
	pr := NewPoolReader(strings.NewReader("hello"), "example.com:8080", &dummyConn{})

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
	pr := NewPoolReader(strings.NewReader(""), "example.com:8080", &dummyConn{})

	buf := make([]byte, 64)
	_, err := pr.Read(buf)
	if err != io.EOF {
		t.Fatalf("expected EOF, got %v", err)
	}
}

func TestPoolReaderReturnsError(t *testing.T) {
	expectedErr := errors.New("read error")
	pr := NewPoolReader(&errReader{err: expectedErr}, "example.com:8080", &dummyConn{})

	buf := make([]byte, 64)
	_, err := pr.Read(buf)
	if err != expectedErr {
		t.Fatalf("expected %v, got %v", expectedErr, err)
	}
}

func TestPoolReaderMultipleReads(t *testing.T) {
	pr := NewPoolReader(strings.NewReader("hello"), "example.com:8080", &dummyConn{})

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

// errReader is a reader that always returns the given error.
type errReader struct {
	err error
}

func (e *errReader) Read(p []byte) (int, error) {
	return 0, e.err
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
