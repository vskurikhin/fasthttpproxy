package pool

import (
	"bytes"
	"io"
	"net"
	"strings"
	"testing"
)

// --- Вспомогательные типы ---

// writerConn оборачивает io.Writer в net.Conn (только Write).
type writerConn struct {
	net.Conn
	w io.Writer
}

func (wc *writerConn) Write(b []byte) (int, error) { return wc.w.Write(b) }
func (wc *writerConn) Close() error                { return nil }

type errWriter struct {
	io.Writer
	err error
}

func (ew *errWriter) Write(b []byte) (int, error) { return 0, ew.err }

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

// --- Тесты PipeCopy ---

func TestPipeCopy(t *testing.T) {
	src := strings.NewReader("hello, world")
	var dst bytes.Buffer

	err := PipeCopy(src, &writerConn{w: &dst})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dst.String() != "hello, world" {
		t.Fatalf("unexpected output: got %q, want %q", dst.String(), "hello, world")
	}
}

func TestPipeCopyEmpty(t *testing.T) {
	src := strings.NewReader("")
	var dst bytes.Buffer

	err := PipeCopy(src, &writerConn{w: &dst})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dst.String() != "" {
		t.Fatalf("expected empty output, got %q", dst.String())
	}
}

func TestPipeCopyError(t *testing.T) {
	src := errReader{}
	var dst bytes.Buffer

	err := PipeCopy(src, &writerConn{w: &dst})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestPipeCopyWriteError(t *testing.T) {
	src := strings.NewReader("data")
	dst := &writerConn{w: &errWriter{err: io.ErrClosedPipe}}

	err := PipeCopy(src, dst)
	if err == nil {
		t.Fatal("expected error")
	}
}

// --- Тесты SetDefaultBufSize / SetPipeCopyBufferSize ---

func TestSetDefaultBufSizeValid(t *testing.T) {
	old := defaultBufSize
	defer func() { defaultBufSize = old }()

	BufferSize(8192)
	if defaultBufSize != 8192 {
		t.Fatalf("expected 8192, got %d", defaultBufSize)
	}
}

func TestSetDefaultBufSizeInvalid(t *testing.T) {
	old := defaultBufSize
	defer func() { defaultBufSize = old }()

	BufferSize(10) // less than minimum
	if defaultBufSize != 4096 {
		t.Fatalf("expected 4096 (unchanged), got %d", defaultBufSize)
	}
}

func TestSetPipeCopyBufferSizeValid(t *testing.T) {
	old := pipeCopyBufferSize
	oldPool := pipeCopyBufPool
	defer func() {
		pipeCopyBufferSize = old
		pipeCopyBufPool = oldPool
	}()

	PipeCopyBufferSize(128 * 1024)
	if pipeCopyBufferSize != 128*1024 {
		t.Fatalf("expected %d, got %d", 128*1024, pipeCopyBufferSize)
	}
}

func TestSetPipeCopyBufferSizeInvalid(t *testing.T) {
	old := pipeCopyBufferSize
	oldPool := pipeCopyBufPool
	defer func() {
		pipeCopyBufferSize = old
		pipeCopyBufPool = oldPool
	}()

	PipeCopyBufferSize(100) // less than minimum
	if pipeCopyBufferSize != 64*1024 {
		t.Fatalf("expected %d (unchanged), got %d", 64*1024, pipeCopyBufferSize)
	}
}

func TestSetPipeCopyBufferSizeResetsPool(t *testing.T) {
	old := pipeCopyBufferSize
	oldPool := pipeCopyBufPool
	defer func() {
		pipeCopyBufferSize = old
		pipeCopyBufPool = oldPool
	}()

	PipeCopyBufferSize(128 * 1024)
	// После смены размера новый буфер из пула должен иметь новый размер
	buf := pipeCopyBufPool.Get().([]byte)
	defer pipeCopyBufPool.Put(buf)
	if len(buf) != 128*1024 {
		t.Fatalf("expected buffer size %d, got %d", 128*1024, len(buf))
	}
}

// --- Тесты bufio acquire/release ---

func TestAcquireReleaseBufioWriter(t *testing.T) {
	var buf bytes.Buffer
	bw := AcquireBufIOWriter(&buf)
	if bw == nil {
		t.Fatal("expected non-nil bufio.Writer")
	}
	// Первое получение из пула — новый writer
	bw.WriteString("test")
	ReleaseBufIOWriter(bw)

	// Повторное получение — должен быть сброшен
	bw2 := AcquireBufIOWriter(&buf)
	if bw2 == nil {
		t.Fatal("expected non-nil bufio.Writer")
	}
	// Проверяем, что он использует новый writer
	if bw2.Available() == 0 {
		t.Fatal("expected available buffer")
	}
	ReleaseBufIOWriter(bw2)
}

func TestAcquireReleaseBufioReader(t *testing.T) {
	r := strings.NewReader("hello")
	br := AcquireBufIOReader(r)
	if br == nil {
		t.Fatal("expected non-nil bufio.Reader")
	}
	ReleaseBufIOReader(br)

	// Повторное получение
	br2 := AcquireBufIOReader(strings.NewReader("world"))
	if br2 == nil {
		t.Fatal("expected non-nil bufio.Reader")
	}
	ReleaseBufIOReader(br2)
}
