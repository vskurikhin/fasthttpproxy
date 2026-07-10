package pool

import (
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"testing"

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

func TestGetAndPut(t *testing.T) {
	// Сброс пула между тестами
	upstreamPool = sync.Map{}

	addr, cleanup := testDialer(t)
	defer cleanup()

	conn1, err := Get(addr)
	if err != nil {
		t.Fatalf("unexpected dial error: %v", err)
	}
	if conn1 == nil {
		t.Fatal("expected non-nil conn")
	}

	Put(addr, conn1)

	conn2, err := Get(addr)
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
	for range maxUpstreamConnsPerHost {
		c, err := Get(addr)
		if err != nil {
			t.Fatalf("unexpected dial error at %d: %v", len(conns), err)
		}
		conns = append(conns, c)
	}

	_, err := Get(addr)
	if err == nil {
		t.Fatal("expected ErrDialTimeout after reaching limit")
	}
	if !errors.Is(err, fasthttp.ErrDialTimeout) {
		t.Fatalf("expected ErrDialTimeout, got: %v", err)
	}

	Put(addr, conns[0])

	c, err := Get(addr)
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
	for range maxUpstreamConnsPerHost {
		fc := &fakeConn{}
		Put(addr, fc)
		conns = append(conns, fc)
	}

	fc2 := &fakeConn{}
	Put(addr, fc2)
	if !fc2.closed.Load() {
		t.Fatal("expected excess connection to be closed")
	}
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
			conn, err := Get(addr)
			if err == nil && conn != nil {
				Put(addr, conn)
			}
		}()
	}
	wg.Wait()

	v, _ := upstreamPool.Load(addr)
	cp := v.(*connPool)
	cp.mu.Lock()
	free := len(cp.free)
	cp.mu.Unlock()

	if free > maxUpstreamConnsPerHost {
		t.Fatalf("pool grew beyond limit: %d > %d", free, maxUpstreamConnsPerHost)
	}
}
