package pool

import (
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/valyala/fasthttp"
)

// fakeConn — минимальная реализация net.Conn для тестов.
type fakeConn struct {
	net.Conn
	closed atomic.Bool
}

func (fc *fakeConn) Close() error {
	fc.closed.Store(true)
	return nil
}

// testDialer подменяет fasthttp.Dial через локальный TCP-сервер.
func testDialer(t *testing.T) (addr string, cleanup func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()
	return ln.Addr().String(), func() { ln.Close() }
}

// errCloseConn — обёртка над net.Conn, у которой Close() всегда возвращает ошибку.
type errCloseConn struct {
	raw    net.Conn
	closed atomic.Bool
}

func (ec *errCloseConn) Read(b []byte) (int, error)  { return ec.raw.Read(b) }
func (ec *errCloseConn) Write(b []byte) (int, error) { return ec.raw.Write(b) }
func (ec *errCloseConn) Close() error {
	ec.closed.Store(true)
	return errors.New("test close error")
}
func (ec *errCloseConn) LocalAddr() net.Addr                { return ec.raw.LocalAddr() }
func (ec *errCloseConn) RemoteAddr() net.Addr               { return ec.raw.RemoteAddr() }
func (ec *errCloseConn) SetDeadline(t time.Time) error      { return ec.raw.SetDeadline(t) }
func (ec *errCloseConn) SetReadDeadline(t time.Time) error  { return ec.raw.SetReadDeadline(t) }
func (ec *errCloseConn) SetWriteDeadline(t time.Time) error { return ec.raw.SetWriteDeadline(t) }

// wrapConnForCloseError оборачивает net.Conn в errCloseConn.
func wrapConnForCloseError(conn net.Conn) *errCloseConn {
	return &errCloseConn{raw: conn}
}

// countConn — net.Conn для подсчёта вызовов Close.
type countConn struct {
	net.Conn
	closeCount atomic.Int32
}

func (cc *countConn) Close() error {
	cc.closeCount.Add(1)
	return nil
}

// poolConnCount возвращает count для заданного addr.
func poolConnCount(addr string) int32 {
	v, _ := upstreamPool.Load(addr)
	if v == nil {
		return 0
	}
	cp := v.(*connPool)
	return atomic.LoadInt32(&cp.count)
}

func TestGetAndPut(t *testing.T) {
	upstreamPool = sync.Map{}

	addr, cleanup := testDialer(t)
	defer cleanup()

	conn1, err := AcquireUpstreamConnection(addr)
	if err != nil {
		t.Fatalf("unexpected dial error: %v", err)
	}
	if conn1 == nil {
		t.Fatal("expected non-nil conn")
	}

	ReleaseUpstreamConnection(addr, conn1)

	conn2, err := AcquireUpstreamConnection(addr)
	if err != nil {
		t.Fatalf("unexpected dial error: %v", err)
	}
	if conn2 != conn1 {
		t.Fatal("expected same connection from pool")
	}
}

func TestGetLimit(t *testing.T) {
	upstreamPool = sync.Map{}
	addr, cleanup := testDialer(t)
	defer cleanup()

	var conns []net.Conn
	for range maxUpstreamConnectionsPerHost {
		c, err := AcquireUpstreamConnection(addr)
		if err != nil {
			t.Fatalf("unexpected dial error at %d: %v", len(conns), err)
		}
		conns = append(conns, c)
	}

	_, err := AcquireUpstreamConnection(addr)
	if err == nil {
		t.Fatal("expected ErrDialTimeout after reaching limit")
	}
	if !errors.Is(err, fasthttp.ErrDialTimeout) {
		t.Fatalf("expected ErrDialTimeout, got: %v", err)
	}

	ReleaseUpstreamConnection(addr, conns[0])

	c, err := AcquireUpstreamConnection(addr)
	if err != nil {
		t.Fatalf("unexpected dial error after release: %v", err)
	}
	if c == nil {
		t.Fatal("expected non-nil conn")
	}
}

func TestPutClosesWhenFull(t *testing.T) {
	upstreamPool = sync.Map{}
	addr := "127.0.0.1:9999"

	var conns []*fakeConn
	for range maxUpstreamConnectionsPerHost {
		fc := &fakeConn{}
		ReleaseUpstreamConnection(addr, fc)
		conns = append(conns, fc)
	}

	fc2 := &fakeConn{}
	ReleaseUpstreamConnection(addr, fc2)
	if !fc2.closed.Load() {
		t.Fatal("expected excess connection to be closed")
	}
}

func TestCloseAndDrop(t *testing.T) {
	upstreamPool = sync.Map{}
	addr, cleanup := testDialer(t)
	defer cleanup()

	conn, err := AcquireUpstreamConnection(addr)
	if err != nil {
		t.Fatalf("unexpected dial error: %v", err)
	}
	if conn == nil {
		t.Fatal("expected non-nil conn")
	}

	beforeCount := poolConnCount(addr)

	CloseAndDropUpstreamConnection(addr, conn)

	afterCount := poolConnCount(addr)
	if afterCount != beforeCount-1 {
		t.Fatalf("expected count %d, got %d", beforeCount-1, afterCount)
	}
}

func TestPutFullDecrementsCount(t *testing.T) {
	upstreamPool = sync.Map{}
	addr := "127.0.0.1:9999"

	for range maxUpstreamConnectionsPerHost {
		ReleaseUpstreamConnection(addr, &fakeConn{})
	}

	beforeCount := poolConnCount(addr)

	ReleaseUpstreamConnection(addr, &fakeConn{})

	afterCount := poolConnCount(addr)
	if afterCount != beforeCount-1 {
		t.Fatalf("expected count %d (decremented by 1), got %d", beforeCount-1, afterCount)
	}
}

func TestGetAfterCloseAndDropAllowsNewDial(t *testing.T) {
	upstreamPool = sync.Map{}
	addr, cleanup := testDialer(t)
	defer cleanup()

	var conns []net.Conn
	for range maxUpstreamConnectionsPerHost {
		c, err := AcquireUpstreamConnection(addr)
		if err != nil {
			t.Fatalf("unexpected dial error: %v", err)
		}
		conns = append(conns, c)
	}

	CloseAndDropUpstreamConnection(addr, conns[0])

	_, err := AcquireUpstreamConnection(addr)
	if err != nil {
		t.Fatalf("expected new dial to succeed after CloseAndDrop, got: %v", err)
	}
}

func TestGetStaleThenDial(t *testing.T) {
	upstreamPool = sync.Map{}
	addr, cleanup := testDialer(t)
	defer cleanup()

	restore := SetIdleTimeoutForTest(t, 10*time.Millisecond)
	defer restore()

	conn1, err := AcquireUpstreamConnection(addr)
	if err != nil {
		t.Fatalf("unexpected dial error: %v", err)
	}
	ReleaseUpstreamConnection(addr, conn1)

	time.Sleep(20 * time.Millisecond)

	conn2, err := AcquireUpstreamConnection(addr)
	if err != nil {
		t.Fatalf("expected new dial after stale drop, got: %v", err)
	}
	if conn2 == conn1 {
		t.Fatal("expected a different connection (stale was dropped and new one dialed)")
	}
}

func TestGetMultipleFreeLIFO(t *testing.T) {
	upstreamPool = sync.Map{}
	addr, cleanup := testDialer(t)
	defer cleanup()

	conns := make([]net.Conn, 3)
	for i := range conns {
		c, err := AcquireUpstreamConnection(addr)
		if err != nil {
			t.Fatalf("unexpected dial error: %v", err)
		}
		conns[i] = c
	}

	for _, c := range conns {
		ReleaseUpstreamConnection(addr, c)
	}

	for i := 2; i >= 0; i-- {
		got, err := AcquireUpstreamConnection(addr)
		if err != nil {
			t.Fatalf("unexpected dial error: %v", err)
		}
		if got != conns[i] {
			t.Fatalf("expected conns[%d], got a different connection (not LIFO)", i)
		}
	}
}

func TestPutWithCloseError(t *testing.T) {
	upstreamPool = sync.Map{}
	addr := "127.0.0.1:9999"

	for range maxUpstreamConnectionsPerHost {
		ReleaseUpstreamConnection(addr, &fakeConn{})
	}

	beforeCount := poolConnCount(addr)

	ec := &errCloseConn{}
	ReleaseUpstreamConnection(addr, ec)

	afterCount := poolConnCount(addr)
	if afterCount != beforeCount-1 {
		t.Fatalf("expected count %d (decremented), got %d", beforeCount-1, afterCount)
	}
	if !ec.closed.Load() {
		t.Fatal("expected errCloseConn to be closed")
	}
}

func TestCloseAndDropWithCloseError(t *testing.T) {
	upstreamPool = sync.Map{}
	addr, cleanup := testDialer(t)
	defer cleanup()

	conn, err := AcquireUpstreamConnection(addr)
	if err != nil {
		t.Fatalf("unexpected dial error: %v", err)
	}

	beforeCount := poolConnCount(addr)

	errConn := wrapConnForCloseError(conn)
	CloseAndDropUpstreamConnection(addr, errConn)

	afterCount := poolConnCount(addr)
	if afterCount != beforeCount-1 {
		t.Fatalf("expected count %d (decremented), got %d", beforeCount-1, afterCount)
	}
	if !errConn.closed.Load() {
		t.Fatal("expected errCloseConn to be closed")
	}
}

func TestIdleTimeoutDropsStaleConnection(t *testing.T) {
	upstreamPool = sync.Map{}
	addr, cleanup := testDialer(t)
	defer cleanup()

	restore := SetIdleTimeoutForTest(t, 10*time.Millisecond)
	defer restore()

	conn1, err := AcquireUpstreamConnection(addr)
	if err != nil {
		t.Fatalf("unexpected dial error: %v", err)
	}

	ReleaseUpstreamConnection(addr, conn1)

	time.Sleep(20 * time.Millisecond)

	conn2, err := AcquireUpstreamConnection(addr)
	if err != nil {
		t.Fatalf("unexpected dial error: %v", err)
	}

	if conn2 == conn1 {
		t.Fatal("expected a new connection (stale was dropped), but got the same")
	}
}

// --- Тесты для SetIdleTimeout ---

func TestSetIdleTimeoutDropsStale(t *testing.T) {
	restore := SetIdleTimeoutForTest(t, 10*time.Millisecond)
	defer restore()

	addr, cleanup := testDialer(t)
	defer cleanup()

	conn1, err := AcquireUpstreamConnection(addr)
	if err != nil {
		t.Fatalf("unexpected dial error: %v", err)
	}
	ReleaseUpstreamConnection(addr, conn1)

	time.Sleep(20 * time.Millisecond)

	conn2, err := AcquireUpstreamConnection(addr)
	if err != nil {
		t.Fatalf("expected new dial after stale drop, got: %v", err)
	}
	if conn2 == conn1 {
		t.Fatal("expected a different connection (stale was dropped)")
	}
}

func TestSetIdleTimeoutLongLived(t *testing.T) {
	restore := SetIdleTimeoutForTest(t, time.Hour)
	defer restore()

	addr, cleanup := testDialer(t)
	defer cleanup()

	conn1, err := AcquireUpstreamConnection(addr)
	if err != nil {
		t.Fatalf("unexpected dial error: %v", err)
	}
	ReleaseUpstreamConnection(addr, conn1)

	time.Sleep(100 * time.Millisecond)

	conn2, err := AcquireUpstreamConnection(addr)
	if err != nil {
		t.Fatalf("unexpected dial error: %v", err)
	}
	if conn2 != conn1 {
		t.Fatal("expected the same connection (idleTimeout is very long)")
	}
}

// --- Тесты для SetMaxConnsPerHost ---

func TestSetMaxConnsPerHostValid(t *testing.T) {
	restore := SetMaxUpstreamConnectionsForTest(t, 50)
	defer restore()

	addr, cleanup := testDialer(t)
	defer cleanup()

	var conns []net.Conn
	for range 50 {
		c, err := AcquireUpstreamConnection(addr)
		if err != nil {
			t.Fatalf("unexpected dial error at %d: %v", len(conns), err)
		}
		conns = append(conns, c)
	}

	_, err := AcquireUpstreamConnection(addr)
	if err == nil {
		t.Fatal("expected ErrDialTimeout after reaching limit")
	}
	if !errors.Is(err, fasthttp.ErrDialTimeout) {
		t.Fatalf("expected ErrDialTimeout, got: %v", err)
	}
}

func TestSetMaxConnsPerHostMinimum(t *testing.T) {
	restore := SetMaxUpstreamConnectionsForTest(t, 100)
	defer restore()

	MaxConnectionsPerHost(1)
	if maxUpstreamConnectionsPerHost != 1 {
		t.Fatalf("expected 1, got %d", maxUpstreamConnectionsPerHost)
	}

	addr, cleanup := testDialer(t)
	defer cleanup()

	conn, err := AcquireUpstreamConnection(addr)
	if err != nil {
		t.Fatalf("unexpected dial error: %v", err)
	}
	if conn == nil {
		t.Fatal("expected non-nil conn")
	}

	_, err = AcquireUpstreamConnection(addr)
	if err == nil {
		t.Fatal("expected ErrDialTimeout (max=1, one conn already taken)")
	}
	if !errors.Is(err, fasthttp.ErrDialTimeout) {
		t.Fatalf("expected ErrDialTimeout, got: %v", err)
	}
}

func TestSetMaxConnsPerHostIsolated(t *testing.T) {
	restore := SetMaxUpstreamConnectionsForTest(t, 3)
	defer restore()

	addr1, cleanup1 := testDialer(t)
	defer cleanup1()
	addr2, cleanup2 := testDialer(t)
	defer cleanup2()

	var conns1 []net.Conn
	for range 3 {
		c, err := AcquireUpstreamConnection(addr1)
		if err != nil {
			t.Fatalf("unexpected dial error for addr1: %v", err)
		}
		conns1 = append(conns1, c)
	}

	_, err := AcquireUpstreamConnection(addr2)
	if err != nil {
		t.Fatalf("expected addr2 to have free slot, got: %v", err)
	}

	_, err = AcquireUpstreamConnection(addr1)
	if err == nil {
		t.Fatal("expected ErrDialTimeout for addr1")
	}
}

func TestSetMaxConnsPerHostAfterPut(t *testing.T) {
	restore := SetMaxUpstreamConnectionsForTest(t, 2)
	defer restore()

	addr, cleanup := testDialer(t)
	defer cleanup()

	c1, err := AcquireUpstreamConnection(addr)
	if err != nil {
		t.Fatalf("unexpected dial error: %v", err)
	}

	c2, err := AcquireUpstreamConnection(addr)
	if err != nil {
		t.Fatalf("unexpected dial error: %v", err)
	}

	ReleaseUpstreamConnection(addr, c2)

	got, err := AcquireUpstreamConnection(addr)
	if err != nil {
		t.Fatalf("unexpected dial error after Put: %v", err)
	}
	if got != c2 {
		t.Fatal("expected the returned connection")
	}
	_ = c1
}

func TestGetConcurrent(t *testing.T) {
	upstreamPool = sync.Map{}
	addr, cleanup := testDialer(t)
	defer cleanup()

	var wg sync.WaitGroup
	for range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			conn, err := AcquireUpstreamConnection(addr)
			if err == nil && conn != nil {
				ReleaseUpstreamConnection(addr, conn)
			}
		}()
	}
	wg.Wait()

	v, _ := upstreamPool.Load(addr)
	cp := v.(*connPool)
	cp.mu.Lock()
	free := len(cp.free)
	cp.mu.Unlock()

	if free > maxUpstreamConnectionsPerHost {
		t.Fatalf("pool grew beyond limit: %d > %d", free, maxUpstreamConnectionsPerHost)
	}
}
