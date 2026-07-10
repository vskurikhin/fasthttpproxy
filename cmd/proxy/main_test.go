package main

import (
	"os"
	"testing"

	"github.com/valyala/fasthttp"
	"github.com/vskurikhin/fasthttpproxy/internal/proxy"
)

func TestRunWithUpstreamsInvalidPort(t *testing.T) {
	err := runWithUpstreams("", ":99999", nil)
	if err == nil {
		t.Fatal("expected error with invalid port")
	}
}

func TestRunWithUpstreamsInvalidMetrics(t *testing.T) {
	err := runWithUpstreams(":-1", ":99999", nil)
	if err == nil {
		t.Fatal("expected error with invalid address")
	}
}

func TestRunWithUpstreamsWithUpstreams(t *testing.T) {
	err := runWithUpstreams("", ":99999", []string{"http://example.com"})
	if err == nil {
		t.Fatal("expected error with invalid port")
	}
}

func TestHandlerReturnsNonNil(t *testing.T) {
	h := proxy.Handler(nil)
	if h == nil {
		t.Fatal("expected non-nil handler")
	}
}

func TestHandlerWithUpstreams(t *testing.T) {
	upstreams := []string{"127.0.0.1:8081", "127.0.0.1:8082"}
	h := proxy.Handler(upstreams)
	if h == nil {
		t.Fatal("expected non-nil handler")
	}
}

func TestRunWithUpstreamsEmptyUpstreams(t *testing.T) {
	err := runWithUpstreams("", ":99999", []string{})
	if err == nil {
		t.Fatal("expected error with invalid port")
	}
}

func TestServeMetricsError(t *testing.T) {
	serveMetrics(":-1", func(ctx *fasthttp.RequestCtx) {})
}

func TestRunWithBadAddrs(t *testing.T) {
	err := runWithUpstreams(":-1", ":-1", nil)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestRunFunction(t *testing.T) {
	// run() calls config.ParseFlags() which registers flags on flag.CommandLine.
	// This is safe to call once. We set os.Args to avoid test flag conflicts.
	origArgs := os.Args
	os.Args = []string{"test", "--metrics-addr=:-1", "--proxy-addr=:-1"}
	defer func() { os.Args = origArgs }()

	err := run()
	if err == nil {
		t.Fatal("expected error")
	}
}
