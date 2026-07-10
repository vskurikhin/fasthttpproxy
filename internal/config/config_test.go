package config

import (
	"os"
	"testing"
	"time"
)

func TestParseFlagsEmpty(t *testing.T) {
	origArgs := os.Args
	os.Args = []string{"test"}
	defer func() { os.Args = origArgs }()

	v := ParseFlags()
	if len(v.Upstreams) != 0 {
		t.Fatalf("expected empty upstreams, got %v", v.Upstreams)
	}
	if v.MetricsAddr != ":7070" {
		t.Fatalf("expected default metrics-addr :7070, got %q", v.MetricsAddr)
	}
	if v.ProxyAddr != ":8080" {
		t.Fatalf("expected default proxy-addr :8080, got %q", v.ProxyAddr)
	}
	if v.MaxConnections != 100 {
		t.Fatalf("expected default max-conns 100, got %d", v.MaxConnections)
	}
}

func TestParseFlagsWithUpstreams(t *testing.T) {
	origArgs := os.Args
	os.Args = []string{"test", "-upstreams", "http://example.com"}
	defer func() { os.Args = origArgs }()

	v := ParseFlags()
	if len(v.Upstreams) != 1 || v.Upstreams[0] != "http://example.com" {
		t.Fatalf("expected [http://example.com], got %v", v.Upstreams)
	}
}

func TestParseFlagsMultipleUpstreams(t *testing.T) {
	origArgs := os.Args
	os.Args = []string{"test", "-upstreams", "http://a.com,http://b.com"}
	defer func() { os.Args = origArgs }()

	v := ParseFlags()
	if len(v.Upstreams) != 2 {
		t.Fatalf("expected 2 upstreams, got %d: %v", len(v.Upstreams), v.Upstreams)
	}
	if v.Upstreams[0] != "http://a.com" {
		t.Fatalf("expected first=http://a.com, got %s", v.Upstreams[0])
	}
	if v.Upstreams[1] != "http://b.com" {
		t.Fatalf("expected second=http://b.com, got %s", v.Upstreams[1])
	}
}

func TestParseFlagsWithAddrs(t *testing.T) {
	origArgs := os.Args
	os.Args = []string{"test", "-metrics-addr=:9090", "-proxy-addr=:8081"}
	defer func() { os.Args = origArgs }()

	v := ParseFlags()
	if v.MetricsAddr != ":9090" {
		t.Fatalf("expected :9090, got %q", v.MetricsAddr)
	}
	if v.ProxyAddr != ":8081" {
		t.Fatalf("expected :8081, got %q", v.ProxyAddr)
	}
}

func TestParseFlagsWithSpaces(t *testing.T) {
	origArgs := os.Args
	os.Args = []string{"test", "-upstreams", " http://a.com , http://b.com "}
	defer func() { os.Args = origArgs }()

	v := ParseFlags()
	if len(v.Upstreams) != 2 {
		t.Fatalf("expected 2 upstreams, got %d: %v", len(v.Upstreams), v.Upstreams)
	}
	if v.Upstreams[0] != "http://a.com" {
		t.Fatalf("expected first=http://a.com, got %s", v.Upstreams[0])
	}
	if v.Upstreams[1] != "http://b.com" {
		t.Fatalf("expected second=http://b.com, got %s", v.Upstreams[1])
	}
}

func TestParseFlagsWithMaxConns(t *testing.T) {
	origArgs := os.Args
	os.Args = []string{"test", "-max-conns", "200"}
	defer func() { os.Args = origArgs }()

	v := ParseFlags()
	if v.MaxConnections != 200 {
		t.Fatalf("expected 200, got %d", v.MaxConnections)
	}
}

func TestParseFlagsWithMaxConnsDefault(t *testing.T) {
	origArgs := os.Args
	os.Args = []string{"test"}
	defer func() { os.Args = origArgs }()

	v := ParseFlags()
	if v.MaxConnections != 100 {
		t.Fatalf("expected default 100, got %d", v.MaxConnections)
	}
}

func TestParseFlagsWithIdleTimeout(t *testing.T) {
	origArgs := os.Args
	os.Args = []string{"test", "--idle-timeout", "60s"}
	defer func() { os.Args = origArgs }()

	v := ParseFlags()
	if v.IdleTimeout != 60*time.Second {
		t.Fatalf("expected 60s, got %v", v.IdleTimeout)
	}
}

func TestParseFlagsWithIdleTimeoutDefault(t *testing.T) {
	origArgs := os.Args
	os.Args = []string{"test"}
	defer func() { os.Args = origArgs }()

	v := ParseFlags()
	if v.IdleTimeout != 45*time.Second {
		t.Fatalf("expected default 45s, got %v", v.IdleTimeout)
	}
}

func TestParseFlagsWithConcurrency(t *testing.T) {
	origArgs := os.Args
	os.Args = []string{"test", "--concurrency", "512"}
	defer func() { os.Args = origArgs }()

	v := ParseFlags()
	if v.Concurrency != 512 {
		t.Fatalf("expected 512, got %d", v.Concurrency)
	}
}

func TestParseFlagsWithConcurrencyDefault(t *testing.T) {
	origArgs := os.Args
	os.Args = []string{"test"}
	defer func() { os.Args = origArgs }()

	v := ParseFlags()
	// fasthttp.DefaultConcurrency = 262144
	if v.Concurrency != 262144 {
		t.Fatalf("expected default 262144 (fasthttp.DefaultConcurrency), got %d", v.Concurrency)
	}
}

func TestParseFlagsWithMaxBodySize(t *testing.T) {
	origArgs := os.Args
	os.Args = []string{"test", "--max-body-size", "10485760"}
	defer func() { os.Args = origArgs }()

	v := ParseFlags()
	if v.MaxRequestBodySize != 10485760 {
		t.Fatalf("expected 10485760, got %d", v.MaxRequestBodySize)
	}
}

func TestParseFlagsWithDialerTimeout(t *testing.T) {
	origArgs := os.Args
	os.Args = []string{"test", "--dialer-timeout", "60s"}
	defer func() { os.Args = origArgs }()

	v := ParseFlags()
	if v.DialerTimeout != 60*time.Second {
		t.Fatalf("expected 60s, got %v", v.DialerTimeout)
	}
}

func TestParseFlagsWithDialerTimeoutDefault(t *testing.T) {
	origArgs := os.Args
	os.Args = []string{"test"}
	defer func() { os.Args = origArgs }()

	v := ParseFlags()
	if v.DialerTimeout != 30*time.Second {
		t.Fatalf("expected default 30s, got %v", v.DialerTimeout)
	}
}

func TestParseFlagsWithMaxBodySizeDefault(t *testing.T) {
	origArgs := os.Args
	os.Args = []string{"test"}
	defer func() { os.Args = origArgs }()

	v := ParseFlags()
	if v.MaxRequestBodySize != 4194304 {
		t.Fatalf("expected default 4194304, got %d", v.MaxRequestBodySize)
	}
}

func TestParseFlagsWithDisableHeaderNorm(t *testing.T) {
	origArgs := os.Args
	os.Args = []string{"test", "--disable-header-norm=false"}
	defer func() { os.Args = origArgs }()

	v := ParseFlags()
	if v.DisableHeaderNamesNormalizing {
		t.Fatal("expected false, got true")
	}
}

func TestParseFlagsWithDisableKeepalive(t *testing.T) {
	origArgs := os.Args
	os.Args = []string{"test", "--disable-keepalive=true"}
	defer func() { os.Args = origArgs }()

	v := ParseFlags()
	if !v.DisableKeepalive {
		t.Fatal("expected true, got false")
	}
}

func TestParseFlagsWithGetOnly(t *testing.T) {
	origArgs := os.Args
	os.Args = []string{"test", "--get-only=true"}
	defer func() { os.Args = origArgs }()

	v := ParseFlags()
	if !v.GetOnly {
		t.Fatal("expected true, got false")
	}
}

func TestParseFlagsWithLogAllErrors(t *testing.T) {
	origArgs := os.Args
	os.Args = []string{"test", "--log-all-errors=false"}
	defer func() { os.Args = origArgs }()

	v := ParseFlags()
	if v.LogAllErrors {
		t.Fatal("expected false, got true")
	}
}

func TestParseFlagsWithMaxConnsPerIP(t *testing.T) {
	origArgs := os.Args
	os.Args = []string{"test", "--max-conns-per-ip", "50"}
	defer func() { os.Args = origArgs }()

	v := ParseFlags()
	if v.MaxConnectionsPerIP != 50 {
		t.Fatalf("expected 50, got %d", v.MaxConnectionsPerIP)
	}
}

func TestParseFlagsWithMaxRequestsPerConn(t *testing.T) {
	origArgs := os.Args
	os.Args = []string{"test", "--max-reqs-per-conn", "10"}
	defer func() { os.Args = origArgs }()

	v := ParseFlags()
	if v.MaxRequestsPerConn != 10 {
		t.Fatalf("expected 10, got %d", v.MaxRequestsPerConn)
	}
}

func TestParseFlagsWithReadBufferSize(t *testing.T) {
	origArgs := os.Args
	os.Args = []string{"test", "--read-buffer-size", "4096"}
	defer func() { os.Args = origArgs }()

	v := ParseFlags()
	if v.ReadBufferSize != 4096 {
		t.Fatalf("expected 4096, got %d", v.ReadBufferSize)
	}
}

func TestParseFlagsWithReadTimeout(t *testing.T) {
	origArgs := os.Args
	os.Args = []string{"test", "--read-timeout", "30s"}
	defer func() { os.Args = origArgs }()

	v := ParseFlags()
	if v.ReadTimeout != 30*time.Second {
		t.Fatalf("expected 30s, got %v", v.ReadTimeout)
	}
}

func TestParseFlagsWithReduceMemoryUsage(t *testing.T) {
	origArgs := os.Args
	os.Args = []string{"test", "--reduce-memory-usage=false"}
	defer func() { os.Args = origArgs }()

	v := ParseFlags()
	if v.ReduceMemoryUsage {
		t.Fatal("expected false, got true")
	}
}

func TestParseFlagsWithSecureErrorLog(t *testing.T) {
	origArgs := os.Args
	os.Args = []string{"test", "--secure-error-log=false"}
	defer func() { os.Args = origArgs }()

	v := ParseFlags()
	if v.SecureErrorLogMessage {
		t.Fatal("expected false, got true")
	}
}

func TestParseFlagsWithWriteBufferSize(t *testing.T) {
	origArgs := os.Args
	os.Args = []string{"test", "--write-buffer-size", "8192"}
	defer func() { os.Args = origArgs }()

	v := ParseFlags()
	if v.WriteBufferSize != 8192 {
		t.Fatalf("expected 8192, got %d", v.WriteBufferSize)
	}
}

func TestParseFlagsWithWriteTimeout(t *testing.T) {
	origArgs := os.Args
	os.Args = []string{"test", "--write-timeout", "60s"}
	defer func() { os.Args = origArgs }()

	v := ParseFlags()
	if v.WriteTimeout != 60*time.Second {
		t.Fatalf("expected 60s, got %v", v.WriteTimeout)
	}
}

func TestParseFlagsAllDefaults(t *testing.T) {
	origArgs := os.Args
	os.Args = []string{"test"}
	defer func() { os.Args = origArgs }()

	v := ParseFlags()
	if !v.DisableHeaderNamesNormalizing {
		t.Fatal("expected DisableHeaderNamesNormalizing=true")
	}
	if v.DisableKeepalive {
		t.Fatal("expected DisableKeepalive=false")
	}
	if v.DisablePreParseMultipartForm {
		t.Fatal("expected DisablePreParseMultipartForm=false")
	}
	if v.GetOnly {
		t.Fatal("expected GetOnly=false")
	}
	if !v.LogAllErrors {
		t.Fatal("expected LogAllErrors=true")
	}
	if v.MaxConnectionsPerIP != 0 {
		t.Fatalf("expected MaxConnsPerIP=0, got %d", v.MaxConnectionsPerIP)
	}
	if v.MaxRequestsPerConn != 0 {
		t.Fatalf("expected MaxRequestsPerConn=0, got %d", v.MaxRequestsPerConn)
	}
	if v.NoDefaultContentType {
		t.Fatal("expected NoDefaultContentType=false")
	}
	if v.NoDefaultDate {
		t.Fatal("expected NoDefaultDate=false")
	}
	if !v.NoDefaultServerHeader {
		t.Fatal("expected NoDefaultServerHeader=true")
	}
	if v.ReadBufferSize != 0 {
		t.Fatalf("expected ReadBufferSize=0, got %d", v.ReadBufferSize)
	}
	if v.ReadTimeout != 0 {
		t.Fatalf("expected ReadTimeout=0, got %v", v.ReadTimeout)
	}
	if !v.ReduceMemoryUsage {
		t.Fatal("expected ReduceMemoryUsage=true")
	}
	if !v.SecureErrorLogMessage {
		t.Fatal("expected SecureErrorLogMessage=true")
	}
	if v.WriteBufferSize != 0 {
		t.Fatalf("expected WriteBufferSize=0, got %d", v.WriteBufferSize)
	}
	if v.WriteTimeout != 0 {
		t.Fatalf("expected WriteTimeout=0, got %v", v.WriteTimeout)
	}
}

func TestParseUpstreamsDirect(t *testing.T) {
	result := parseUpstreams("http://example.com", nil)
	if len(result) != 1 || result[0] != "http://example.com" {
		t.Fatalf("expected [http://example.com], got %v", result)
	}
}

func TestParseUpstreamsMultipleDirect(t *testing.T) {
	result := parseUpstreams("http://a.com,http://b.com", nil)
	if len(result) != 2 {
		t.Fatalf("expected 2 upstreams, got %d: %v", len(result), result)
	}
	if result[0] != "http://a.com" {
		t.Fatalf("expected first=http://a.com, got %s", result[0])
	}
	if result[1] != "http://b.com" {
		t.Fatalf("expected second=http://b.com, got %s", result[1])
	}
}

func TestAddrAppendTrimsSpace(t *testing.T) {
	var result []string
	result = addrAppend(result, "  http://example.com  ")
	if len(result) != 1 || result[0] != "http://example.com" {
		t.Fatalf("expected [http://example.com], got %v", result)
	}
}

func TestAddrAppendValidURL(t *testing.T) {
	var result []string
	result = addrAppend(result, "http://example.com")
	if len(result) != 1 || result[0] != "http://example.com" {
		t.Fatalf("expected [http://example.com], got %v", result)
	}
}
