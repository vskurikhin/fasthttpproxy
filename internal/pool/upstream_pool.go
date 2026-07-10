package pool

import (
	"log"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/vskurikhin/fasthttpproxy/internal/metrics"
)

// upstreamPool хранит пул соединений для каждого upstream-адреса.
var upstreamPool sync.Map // addr -> *connPool

// AcquireUpstreamConnection возвращает соединение к upstream из пула или создаёт новое.
// Соединения, пробывшие в пуле дольше idleTimeout, отбрасываются.
func AcquireUpstreamConnection(addr string) (net.Conn, error) {
	v, _ := upstreamPool.LoadOrStore(addr, &connPool{})
	cp := v.(*connPool)

	cp.mu.Lock()
	if n := len(cp.free); n > 0 {
		last := cp.free[n-1]
		cp.free = cp.free[:n-1]
		cp.mu.Unlock()

		// Проверяем, не простаивало ли соединение слишком долго.
		if time.Since(last.returnedAt) > idleTimeout {
			metrics.IdleDropErrors.Inc()
			log.Printf("pool: dropping idle connection to %s (idle %v > %v)",
				addr, time.Since(last.returnedAt), idleTimeout)
			// Закрываем и декрементим count, как CloseAndDrop.
			if err := last.conn.Close(); err != nil {
				metrics.CloseErrors.Inc()
				log.Printf("pool: close error for %s: %v", addr, err)
			}
			atomic.AddInt32(&cp.count, -1)
			// Идём на создание нового соединения.
			return cp.dialNew(addr)
		}

		return last.conn, nil
	}
	cp.mu.Unlock()

	return cp.dialNew(addr)
}

// ReleaseUpstreamConnection возвращает соединение обратно в пул для повторного использования.
// Если пул переполнен, соединение закрывается и count декрементится.
func ReleaseUpstreamConnection(addr string, conn net.Conn) {
	v, _ := upstreamPool.LoadOrStore(addr, &connPool{})
	cp := v.(*connPool)

	cp.mu.Lock()
	defer cp.mu.Unlock()

	if len(cp.free) >= maxUpstreamConnectionsPerHost {
		// Пул переполнен — закрываем коннект и уменьшаем счётчик.
		atomic.AddInt32(&cp.count, -1)
		if err := conn.Close(); err != nil {
			metrics.CloseErrors.Inc()
			log.Printf("pool: close error for %s: %v", addr, err)
		}
		return
	}
	cp.free = append(cp.free, idleConn{
		conn:       conn,
		returnedAt: time.Now(),
	})
}

// CloseAndDropUpstreamConnection закрывает соединение и декрементит count.
// Используется, когда коннект гарантированно мёртв (broken pipe, EOF на upstream).
func CloseAndDropUpstreamConnection(addr string, conn net.Conn) {
	defer func() {
		if err := conn.Close(); err != nil {
			metrics.CloseErrors.Inc()
			log.Printf("pool: close error for %s: %v", addr, err)
		}
	}()
	v, _ := upstreamPool.LoadOrStore(addr, &connPool{})
	cp := v.(*connPool)

	cp.mu.Lock()
	defer cp.mu.Unlock()
	atomic.AddInt32(&cp.count, -1)
}
