package main

import (
	"os"
	"testing"
	"time"

	"github.com/vskurikhin/fasthttpproxy/internal/config"
	"github.com/vskurikhin/fasthttpproxy/internal/pool"
)

// TestPoolDialerTimeoutIntegration проверяет, что pool.HTTPDialerTimeout
// корректно сохраняет значение и возвращает его через pool.HTTPDialTimeout.
// Это косвенная проверка кода runWith, который вызывает pool.HTTPDialerTimeout
// с values.DialerTimeout.
func TestPoolDialerTimeoutIntegration(t *testing.T) {
	prevDial := pool.HTTPDialTimeout()
	t.Logf("dial timeout before: %v", prevDial)

	// Имитируем то, что делает runWith — вызываем pool.HTTPDialerTimeout
	// со значением из конфигурации.
	pool.HTTPDialerTimeout(15 * time.Second)

	afterDial := pool.HTTPDialTimeout()
	if afterDial != 15*time.Second {
		t.Fatalf("expected dial timeout 15s, got %v", afterDial)
	}

	// Проверяем с дефолтным значением 30s.
	pool.HTTPDialerTimeout(30 * time.Second)

	afterDial2 := pool.HTTPDialTimeout()
	if afterDial2 != 30*time.Second {
		t.Fatalf("expected dial timeout 30s, got %v", afterDial2)
	}
}

// TestParseFlagsDialerTimeout проверяет, что флаг --dialer-timeout
// корректно парсится и передаётся в Values.DialerTimeout.
func TestParseFlagsDialerTimeout(t *testing.T) {
	origArgs := os.Args
	os.Args = []string{"test", "--dialer-timeout", "60s"}
	defer func() { os.Args = origArgs }()

	v := config.ParseFlags()
	if v.DialerTimeout != 60*time.Second {
		t.Fatalf("expected 60s, got %v", v.DialerTimeout)
	}
}

func TestParseFlagsDialerTimeoutDefault(t *testing.T) {
	origArgs := os.Args
	os.Args = []string{"test"}
	defer func() { os.Args = origArgs }()

	v := config.ParseFlags()
	if v.DialerTimeout != 30*time.Second {
		t.Fatalf("expected default 30s, got %v", v.DialerTimeout)
	}
}
