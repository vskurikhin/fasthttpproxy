package proxy

import (
	"io"
	"net"
	"time"
)

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

// resetWriter — пишет до max байт, затем возвращает err (ECONNRESET).
type resetWriter struct {
	max     int64
	written int64
	err     error
}

func (w *resetWriter) Write(p []byte) (int, error) {
	avail := w.max - w.written
	if avail <= 0 {
		return 0, w.err
	}
	if int64(len(p)) > avail {
		n := int(avail)
		w.written = w.max
		return n, w.err
	}
	w.written += int64(len(p))
	return len(p), nil
}

// closeTrackConn — net.Conn, которая отслеживает вызов Close.
type closeTrackConn struct {
	net.Conn
	closeFn func()
	closed  bool
}

func (c *closeTrackConn) Close() error {
	c.closed = true
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
