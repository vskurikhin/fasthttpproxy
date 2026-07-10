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

// SetIdleTimeoutForTest устанавливает idleTimeout для тестов (восстанавливает
// исходное значение через cleanup).
func SetIdleTimeoutForTest(t *testing.T, d time.Duration) func() {
	t.Helper()
	old := idleTimeout
	idleTimeout = d
	return func() { idleTimeout = old }
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

// TestCloseAndDrop проверяет, что CloseAndDrop закрывает коннект и декрементит count.
func TestCloseAndDrop(t *testing.T) {
	upstreamPool = sync.Map{}
	addr, cleanup := testDialer(t)
	defer cleanup()

	conn, err := Get(addr)
	if err != nil {
		t.Fatalf("unexpected dial error: %v", err)
	}
	if conn == nil {
		t.Fatal("expected non-nil conn")
	}

	beforeCount := poolConnCount(addr)

	CloseAndDrop(addr, conn)

	afterCount := poolConnCount(addr)
	if afterCount != beforeCount-1 {
		t.Fatalf("expected count %d, got %d", beforeCount-1, afterCount)
	}
}

// TestPutFullDecrementsCount проверяет, что Put при переполнении free
// декрементит count.
func TestPutFullDecrementsCount(t *testing.T) {
	upstreamPool = sync.Map{}
	addr := "127.0.0.1:9999"

	// Заполняем free до maxUpstreamConnsPerHost
	for range maxUpstreamConnsPerHost {
		Put(addr, &fakeConn{})
	}

	beforeCount := poolConnCount(addr)

	// Кладём ещё один — Put закроет и декрементит count
	Put(addr, &fakeConn{})

	afterCount := poolConnCount(addr)
	if afterCount != beforeCount-1 {
		t.Fatalf("expected count %d (decremented by 1), got %d", beforeCount-1, afterCount)
	}
}

// TestGetAfterCloseAndDropAllowsNewDial проверяет, что после CloseAndDrop
// (count уменьшен) Get может создать новый коннект, а не выдаёт ErrDialTimeout.
func TestGetAfterCloseAndDropAllowsNewDial(t *testing.T) {
	upstreamPool = sync.Map{}
	addr, cleanup := testDialer(t)
	defer cleanup()

	// Создаём maxUpstreamConnsPerHost коннектов
	var conns []net.Conn
	for range maxUpstreamConnsPerHost {
		c, err := Get(addr)
		if err != nil {
			t.Fatalf("unexpected dial error: %v", err)
		}
		conns = append(conns, c)
	}

	// Закрываем один через CloseAndDrop — count уменьшается
	CloseAndDrop(addr, conns[0])

	// Теперь Get должен создать новый коннект (count < max)
	_, err := Get(addr)
	if err != nil {
		t.Fatalf("expected new dial to succeed after CloseAndDrop, got: %v", err)
	}
}

// TestGetStaleThenDial проверяет: idle timeout дропает коннект,
// затем dialNew создаёт новый (count уменьшен после дропа).
func TestGetStaleThenDial(t *testing.T) {
	upstreamPool = sync.Map{}
	addr, cleanup := testDialer(t)
	defer cleanup()

	restore := SetIdleTimeoutForTest(t, 10*time.Millisecond)
	defer restore()

	// Создаём коннект, кладём в пул
	conn1, err := Get(addr)
	if err != nil {
		t.Fatalf("unexpected dial error: %v", err)
	}
	Put(addr, conn1)

	// Ждём дольше idleTimeout
	time.Sleep(20 * time.Millisecond)

	// Get должен: найти stale в free → дропнуть → dialNew → новый коннект
	conn2, err := Get(addr)
	if err != nil {
		t.Fatalf("expected new dial after stale drop, got: %v", err)
	}
	if conn2 == conn1 {
		t.Fatal("expected a different connection (stale was dropped and new one dialed)")
	}
}

// TestGetMultipleFreeLIFO проверяет LIFO-порядок извлечения из free.
func TestGetMultipleFreeLIFO(t *testing.T) {
	upstreamPool = sync.Map{}
	addr, cleanup := testDialer(t)
	defer cleanup()

	// Создаём 3 разных коннекта
	conns := make([]net.Conn, 3)
	for i := range conns {
		c, err := Get(addr)
		if err != nil {
			t.Fatalf("unexpected dial error: %v", err)
		}
		conns[i] = c
	}

	// Кладём все три в пул
	for _, c := range conns {
		Put(addr, c)
	}

	// Забираем — порядок должен быть LIFO (последний положен = первый получен)
	for i := 2; i >= 0; i-- {
		got, err := Get(addr)
		if err != nil {
			t.Fatalf("unexpected dial error: %v", err)
		}
		if got != conns[i] {
			t.Fatalf("expected conns[%d], got a different connection (not LIFO)", i)
		}
	}
}

// TestPutWithCloseError проверяет, что Put при переполнении обрабатывает
// ошибку Close() без паники и декрементит count.
func TestPutWithCloseError(t *testing.T) {
	upstreamPool = sync.Map{}
	addr := "127.0.0.1:9999"

	// Заполняем free до maxUpstreamConnsPerHost обычными fakeConn
	for range maxUpstreamConnsPerHost {
		Put(addr, &fakeConn{})
	}

	beforeCount := poolConnCount(addr)

	// Кладём errCloseConn — Close() вернёт ошибку
	ec := &errCloseConn{}
	Put(addr, ec)

	afterCount := poolConnCount(addr)
	if afterCount != beforeCount-1 {
		t.Fatalf("expected count %d (decremented), got %d", beforeCount-1, afterCount)
	}
	if !ec.closed.Load() {
		t.Fatal("expected errCloseConn to be closed")
	}
}

// TestCloseAndDropWithCloseError проверяет, что CloseAndDrop обрабатывает
// ошибку Close() без паники и декрементит count.
func TestCloseAndDropWithCloseError(t *testing.T) {
	upstreamPool = sync.Map{}
	addr, cleanup := testDialer(t)
	defer cleanup()

	conn, err := Get(addr)
	if err != nil {
		t.Fatalf("unexpected dial error: %v", err)
	}

	beforeCount := poolConnCount(addr)

	// Оборачиваем в errCloseConn для имитации ошибки Close
	errConn := wrapConnForCloseError(conn)
	CloseAndDrop(addr, errConn)

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

	conn1, err := Get(addr)
	if err != nil {
		t.Fatalf("unexpected dial error: %v", err)
	}

	Put(addr, conn1)

	// Ждём дольше idleTimeout, чтобы соединение устарело
	time.Sleep(20 * time.Millisecond)

	conn2, err := Get(addr)
	if err != nil {
		t.Fatalf("unexpected dial error: %v", err)
	}

	if conn2 == conn1 {
		t.Fatal("expected a new connection (stale was dropped), but got the same")
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
