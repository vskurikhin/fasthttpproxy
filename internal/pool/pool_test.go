package pool

import (
	"testing"
	"time"
)

// --- Тесты для SetIdleTimeout ---

func TestSetIdleTimeoutDefault(t *testing.T) {
	if idleTimeout != 45*time.Second {
		t.Fatalf("expected default 45s, got %v", idleTimeout)
	}
}

func TestSetIdleTimeoutValid(t *testing.T) {
	restore := SetIdleTimeoutForTest(t, 10*time.Second)
	defer restore()

	if idleTimeout != 10*time.Second {
		t.Fatalf("expected 10s, got %v", idleTimeout)
	}
}

func TestSetIdleTimeoutInvalid(t *testing.T) {
	restore := SetIdleTimeoutForTest(t, 45*time.Second)
	defer restore()

	IdleTimeout(0)
	if idleTimeout != 45*time.Second {
		t.Fatalf("expected value to stay 45s, got %v", idleTimeout)
	}

	IdleTimeout(500 * time.Millisecond)
	if idleTimeout != 45*time.Second {
		t.Fatalf("expected value to stay 45s, got %v", idleTimeout)
	}
}

func TestSetIdleTimeoutMinimum(t *testing.T) {
	restore := SetIdleTimeoutForTest(t, 45*time.Second)
	defer restore()

	IdleTimeout(time.Second)
	if idleTimeout != time.Second {
		t.Fatalf("expected 1s, got %v", idleTimeout)
	}
}

// --- Тесты для SetMaxConnsPerHost ---

func TestSetMaxConnsPerHostDefault(t *testing.T) {
	if maxUpstreamConnectionsPerHost != 100 {
		t.Fatalf("expected default 100, got %d", maxUpstreamConnectionsPerHost)
	}
}

func TestSetMaxConnsPerHostInvalid(t *testing.T) {
	restore := SetMaxConnsForTest(t, 100)
	defer restore()

	MaxConnectionsPerHost(0)
	if maxUpstreamConnectionsPerHost != 100 {
		t.Fatalf("expected value to stay 100, got %d", maxUpstreamConnectionsPerHost)
	}

	MaxConnectionsPerHost(-5)
	if maxUpstreamConnectionsPerHost != 100 {
		t.Fatalf("expected value to stay 100, got %d", maxUpstreamConnectionsPerHost)
	}
}
