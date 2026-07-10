package pool

import (
	"net"
	"time"

	"github.com/valyala/fasthttp"
	"github.com/valyala/fasthttp/fasthttpproxy"
)

// customDial — не глобальная диал-функция, устанавливаемая через HTTPDialerTimeout.
// Если nil, используется fasthttp.Dial.
var customDial func(addr string) (net.Conn, error)

// HTTPDialerTimeout устанавливает кастомную диал-функцию для всех соединений пула.
// Обычно сюда передают fasthttpproxy.FasthttpHTTPDialer* или аналоги.
//
// Пример:
//
//	pool.HTTPDialerTimeout(30 * time.Second)
//
// Сохраняет переданный timeout в dialTimeout для тестирования.
func HTTPDialerTimeout(timeout time.Duration) {
	customDial = fasthttpproxy.FasthttpHTTPDialerDualStackTimeout("", timeout)
}

// dial возвращает соединение, используя customDial или fasthttp.Dial по умолчанию.
func dial(addr string) (net.Conn, error) {
	if customDial != nil {
		return customDial(addr)
	}
	return fasthttp.Dial(addr)
}
