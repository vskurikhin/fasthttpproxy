// Package pool предоставляет пул TCP-соединений к upstream-серверам
// с поддержкой кастомной диал-функции (например, fasthttpproxy).
package pool

import (
	"log"
	"net"
	"sync"
	"sync/atomic"

	"github.com/valyala/fasthttp"
	"github.com/vskurikhin/fasthttpproxy/internal/metrics"
)

// upstreamPool хранит пул соединений для каждого upstream-адреса.
var upstreamPool sync.Map // addr -> *connPool

type connPool struct {
	mu    sync.Mutex
	free  []net.Conn
	count int32
}

const maxUpstreamConnsPerHost = 100

// customDial — не глобальная диал-функция, устанавливаемая через SetDial.
// Если nil, используется fasthttp.Dial.
var customDial func(addr string) (net.Conn, error)

// SetDial устанавливает кастомную диал-функцию для всех соединений пула.
// Обычно сюда передают fasthttpproxy.FasthttpHTTPDialer* или аналоги.
//
// Пример:
//
//	pool.SetDial(fasthttpproxy.FasthttpHTTPDialerDualStackTimeout("", 30*time.Second))
//
// Если dial равен nil, пул возвращается к fasthttp.Dial.
func SetDial(dial func(addr string) (net.Conn, error)) {
	customDial = dial
}

// dial возвращает соединение, используя customDial или fasthttp.Dial по умолчанию.
func dial(addr string) (net.Conn, error) {
	if customDial != nil {
		return customDial(addr)
	}
	return fasthttp.Dial(addr)
}

// Get возвращает соединение к upstream из пула или создаёт новое.
func Get(addr string) (net.Conn, error) {
	v, _ := upstreamPool.LoadOrStore(addr, &connPool{})
	cp := v.(*connPool)

	cp.mu.Lock()
	if n := len(cp.free); n > 0 {
		conn := cp.free[n-1]
		cp.free = cp.free[:n-1]
		cp.mu.Unlock()
		return conn, nil
	}
	cp.mu.Unlock()

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

// Put возвращает соединение обратно в пул для повторного использования.
// Если пул переполнен, соединение закрывается.
func Put(addr string, conn net.Conn) {
	v, _ := upstreamPool.LoadOrStore(addr, &connPool{})
	cp := v.(*connPool)

	cp.mu.Lock()
	defer cp.mu.Unlock()

	if len(cp.free) >= maxUpstreamConnsPerHost {
		if err := conn.Close(); err != nil {
			metrics.CloseErrors.Inc()
			log.Printf("pool: close error for %s: %v", addr, err)
		}
		return
	}
	cp.free = append(cp.free, conn)
}
