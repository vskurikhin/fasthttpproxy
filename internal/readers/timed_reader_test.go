package readers

import (
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

type mockReader struct {
	data  []byte
	delay time.Duration
	err   error
	call  func()
}

func (m *mockReader) Read(p []byte) (int, error) {
	if m.call != nil {
		m.call()
	}
	time.Sleep(m.delay)
	if m.err != nil {
		return 0, m.err
	}
	if len(m.data) == 0 {
		return 0, io.EOF
	}
	n := copy(p, m.data)
	m.data = m.data[n:]
	return n, nil
}

func TestTimedReaderReadsFull(t *testing.T) {
	input := "hello world"
	tr := NewTimedReader(strings.NewReader(input), nil)

	buf := make([]byte, 64)
	n, err := tr.Read(buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != len(input) {
		t.Fatalf("expected %d bytes, got %d", len(input), n)
	}

	n, err = tr.Read(buf)
	if err != io.EOF {
		t.Fatalf("expected EOF, got %v", err)
	}
	if n != 0 {
		t.Fatalf("expected 0 bytes, got %d", n)
	}
}

func TestTimedReaderPartialRead(t *testing.T) {
	tr := NewTimedReader(strings.NewReader("hello"), nil)

	buf := make([]byte, 2)
	n, err := tr.Read(buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 2 {
		t.Fatalf("expected 2 bytes, got %d", n)
	}

	n, err = tr.Read(buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 2 {
		t.Fatalf("expected 2 bytes, got %d", n)
	}
}

func TestTimedReaderError(t *testing.T) {
	expectedErr := errors.New("read error")
	tr := NewTimedReader(&mockReader{err: expectedErr}, nil)

	buf := make([]byte, 64)
	n, err := tr.Read(buf)
	if err != expectedErr {
		t.Fatalf("expected %v, got %v", expectedErr, err)
	}
	if n != 0 {
		t.Fatalf("expected 0 bytes on error, got %d", n)
	}
}

func TestTimedReaderRecordsDuration(t *testing.T) {
	tr := NewTimedReader(&mockReader{
		data:  []byte("hello"),
		delay: 10 * time.Millisecond,
	}, nil)

	buf := make([]byte, 64)
	n, err := tr.Read(buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 5 {
		t.Fatalf("expected 5 bytes, got %d", n)
	}

	n, err = tr.Read(buf)
	if err != io.EOF {
		t.Fatalf("expected EOF, got %v", err)
	}
	if n != 0 {
		t.Fatalf("expected 0 bytes, got %d", n)
	}
}

func TestTimedReaderEmptyReader(t *testing.T) {
	tr := NewTimedReader(strings.NewReader(""), nil)

	buf := make([]byte, 64)
	n, err := tr.Read(buf)
	if err != io.EOF {
		t.Fatalf("expected EOF, got %v", err)
	}
	if n != 0 {
		t.Fatalf("expected 0 bytes, got %d", n)
	}
}
