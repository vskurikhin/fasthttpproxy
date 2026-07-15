package pool

import (
	"net"
	"testing"
	"time"
)

// SetMaxUpstreamConnectionsForTest устанавливает maxUpstreamConnsPerHost для тестов
// и возвращает функцию восстановления исходного значения.
func SetMaxUpstreamConnectionsForTest(t *testing.T, n int) func() {
	t.Helper()
	old := maxUpstreamConnectionsPerHost
	maxUpstreamConnectionsPerHost = n
	return func() { maxUpstreamConnectionsPerHost = old }
}

// SetIdleTimeoutForTest устанавливает idleTimeout для тестов напрямую (без валидации)
// и возвращает функцию восстановления исходного значения.
func SetIdleTimeoutForTest(t *testing.T, d time.Duration) func() {
	t.Helper()
	old := idleTimeout
	idleTimeout = d
	return func() { idleTimeout = old }
}

// SetCustomDialForTest устанавливает customDial для тестов
// и возвращает функцию восстановления исходного значения.
func SetCustomDialForTest(t *testing.T, fn func(addr string) (net.Conn, error)) func() {
	t.Helper()
	old := customDial
	customDial = fn
	return func() { customDial = old }
}
