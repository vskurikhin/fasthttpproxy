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
)

type idleConn struct {
	conn       net.Conn
	returnedAt time.Time
}

type connPool struct {
	mu    sync.Mutex
	free  []idleConn
	count int32
}

// maxUpstreamConnectionsPerHost — максимальное количество соединений к одному upstream.
// Значение по умолчанию — 100. Может быть изменено через MaxConnectionsPerHost.
//
// Изменять значение рекомендуется до запуска сервера, поскольку connPool-ы
// создаются лениво при первом вызове AcquireUpstreamConnection для каждого адреса. Уже существующие
// пулы используют старое значение до перезагрузки приложения.
//
// Минимальное значение — 1. При попытке установить меньше 1 функция
// MaxConnectionsPerHost игнорирует значение и логирует предупреждение.
var maxUpstreamConnectionsPerHost = 100

// MaxConnectionsPerHost устанавливает максимальное количество соединений на один
// upstream-адрес. По умолчанию — 100. Минимальное допустимое значение — 1.
//
// Значение должно быть установлено до вызова AcquireUpstreamConnection для любого адреса, иначе
// существующие connPool-ы продолжат использовать предыдущее значение.
//
// Пример:
//
//	pool.MaxConnectionsPerHost(200)
//
// Если переданное значение меньше 1, функция логирует предупреждение и
// сохраняет текущее значение без изменений.
func MaxConnectionsPerHost(n int) {
	if n < 1 {
		log.Printf("pool: invalid maxConnectionsPerHost=%d, minimum is 1", n)
		return
	}
	maxUpstreamConnectionsPerHost = n
}

// idleTimeout — максимальное время бездействия соединения в пуле.
// Если соединение простаивает дольше, оно считается мёртвым и отбрасывается.
// Значение 45 секунд выбрано как компромисс: меньше типичного keepalive timeout
// upstream-серверов (60-120 с), чтобы избежать записи в закрытый сокет.
// Может быть изменено через IdleTimeout. Минимальное значение — 1 секунда.
var idleTimeout = 45 * time.Second

// IdleTimeout устанавливает максимальное время бездействия соединения в пуле.
// Соединения, пробывшие в пуле дольше этого времени, отбрасываются при извлечении.
// По умолчанию — 45 секунд. Минимальное допустимое значение — 1 секунда.
//
// Пример:
//
//	pool.IdleTimeout(60 * time.Second)
//
// Если переданное значение меньше 1 секунды, функция логирует предупреждение
// и сохраняет текущее значение без изменений.
func IdleTimeout(d time.Duration) {
	if d < time.Second {
		log.Printf("pool: invalid idleTimeout=%v, minimum is 1s", d)
		return
	}
	idleTimeout = d
}

// dialNew создаёт новое соединение к addr.
// Без блокировки cp.mu — вызывающий должен гарантировать, что count уже проверен.
func (cp *connPool) dialNew(addr string) (net.Conn, error) {
	if atomic.LoadInt32(&cp.count) >= int32(maxUpstreamConnectionsPerHost) {
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
