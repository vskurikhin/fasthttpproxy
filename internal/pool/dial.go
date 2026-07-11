package pool

import (
	"crypto/tls"
	"net"
	"strings"
	"time"

	"github.com/valyala/fasthttp"
	"github.com/valyala/fasthttp/fasthttpproxy"
	"github.com/vskurikhin/fasthttpproxy/internal/config"
)

// customDial — не глобальная диал-функция, устанавливаемая через HTTPDialerTimeout.
// Если nil, используется fasthttp.Dial.
var customDial func(addr string) (net.Conn, error)

// tlsConfig — глобальная TLS-конфигурация для HTTPS соединений.
// Устанавливается через TLSConfig.
var tlsConfig *tls.Config

// dialTimeout — сохранённое значение таймаута для тестирования.
var dialTimeout time.Duration

// HTTPDialerTimeout устанавливает кастомную диал-функцию для всех соединений пула.
// Обычно сюда передают fasthttpproxy.FasthttpHTTPDialer* или аналоги.
//
// Пример:
//
//	pool.HTTPDialerTimeout(30 * time.Second)
//
// Сохраняет переданный timeout в dialTimeout для тестирования.
func HTTPDialerTimeout(timeout time.Duration) {
	dialTimeout = timeout
	customDial = fasthttpproxy.FasthttpHTTPDialerDualStackTimeout("", timeout)
}

// HTTPDialTimeout возвращает текущее значение таймаута диал-функции (для тестирования).
func HTTPDialTimeout() time.Duration {
	return dialTimeout
}

// TLSConfig устанавливает глобальную TLS-конфигурацию для HTTPS соединений.
// Если cfg равен nil, TLS отключается.
func TLSConfig(cfg *tls.Config) {
	tlsConfig = cfg
}

// dial возвращает соединение, используя customDial или fasthttp.Dial по умолчанию.
// addr может быть в формате "http://host:port" или "https://host:port".
func dial(addr string) (net.Conn, error) {
	cleanAddr := parseUpstreamAddressForDial(addr)

	if customDial != nil {
		return customDial(cleanAddr)
	}

	// Определяем схему из адреса
	if strings.HasPrefix(addr, config.PrefixHTTPS) && tlsConfig != nil {
		// Для HTTPS используем fasthttp.Dial + TLS поверх
		// Linter Warning:(61, 25) Potential resource leak: ensure the resource is closed on all execution paths
		// Утечки нет. tls.Client — синхронная, безошибочная операция: она просто оборачивает conn в *tls.Conn и
		// не выполняет handshake. Возвращённый tlsConn владеет базовым TCP-соединением.
		// Когда tlsConn позже закрывается через ReleaseUpstreamConnection или CloseAndDropUpstreamConnection,
		// оба слоя (TLS + TCP) закрываются корректно.
		// — ложное срабатывание линтера.
		// Линтер не видит, что tls.Client не имеет возврата ошибки и
		// предполагает гипотетический сценарий отказа.
		conn, err := fasthttp.Dial(cleanAddr)
		if err != nil {
			return nil, err
		}
		tlsConn := tls.Client(conn, tlsConfig)
		return tlsConn, nil
	}

	return fasthttp.Dial(cleanAddr)
}

// parseUpstreamAddressForDial извлекает чистый host:port из адреса со схемой.
// Например, "https://example.com:443" -> "example.com:443".
func parseUpstreamAddressForDial(addr string) string {
	trimmed := addr
	if strings.HasPrefix(trimmed, config.PrefixHTTP) {
		trimmed = trimmed[7:]
	} else if strings.HasPrefix(trimmed, config.PrefixHTTPS) {
		trimmed = trimmed[8:]
	}
	return trimmed
}
