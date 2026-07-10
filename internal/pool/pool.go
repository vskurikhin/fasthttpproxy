// Package pool предоставляет пул TCP-соединений к upstream-серверам
// с поддержкой кастомной диал-функции (например, fasthttpproxy).
package pool

import (
	"log"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/valyala/fasthttp"
	"github.com/vskurikhin/fasthttpproxy/internal/metrics"
)

// upstreamPool хранит пул соединений для каждого upstream-адреса.
var upstreamPool sync.Map // addr -> *connPool

type idleConn struct {
	conn       net.Conn
	returnedAt time.Time
}

type connPool struct {
	mu    sync.Mutex
	free  []idleConn
	count int32
}

const maxUpstreamConnsPerHost = 100

// idleTimeout — максимальное время бездействия соединения в пуле.
// Если соединение простаивает дольше, оно считается мёртвым и отбрасывается.
// Значение 45 секунд выбрано как компромисс: меньше типичного keepalive timeout
// upstream-серверов (60-120 с), чтобы избежать записи в закрытый сокет.
var idleTimeout = 45 * time.Second

// Get возвращает соединение к upstream из пула или создаёт новое.
// Соединения, пробывшие в пуле дольше idleTimeout, отбрасываются.
func Get(addr string) (net.Conn, error) {
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

// Put возвращает соединение обратно в пул для повторного использования.
// Если пул переполнен, соединение закрывается и count декрементится.
func Put(addr string, conn net.Conn) {
	v, _ := upstreamPool.LoadOrStore(addr, &connPool{})
	cp := v.(*connPool)

	cp.mu.Lock()
	defer cp.mu.Unlock()

	if len(cp.free) >= maxUpstreamConnsPerHost {
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

// CloseAndDrop закрывает соединение и декрементит count.
// Используется, когда коннект гарантированно мёртв (broken pipe, EOF на upstream).
func CloseAndDrop(addr string, conn net.Conn) {
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

// dialNew создаёт новое соединение к addr.
// Без блокировки cp.mu — вызывающий должен гарантировать, что count уже проверен.
func (cp *connPool) dialNew(addr string) (net.Conn, error) {
	if atomic.LoadInt32(&cp.count) >= maxUpstreamConnsPerHost {
		return nil, fasthttp.ErrDialTimeout
	}

	atomic.AddInt32(&cp.count, 1)

	conn, err := dial(addr)
	if err != nil {
		atomic.AddInt32(&cp.count, -1)
		return nil, err
	}
	return conn, nil
}
