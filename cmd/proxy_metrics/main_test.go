package main

import (
	"testing"

	"github.com/vskurikhin/fasthttpproxy/internal/proxy"
)

func TestHandlerReturnsNonNil(t *testing.T) {
	h := proxy.Handler()
	if h == nil {
		t.Fatal("expected non-nil handler")
	}
}
