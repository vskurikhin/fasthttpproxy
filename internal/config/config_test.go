package config

import (
	"os"
	"testing"
)

func TestParseFlagsEmpty(t *testing.T) {
	origArgs := os.Args
	os.Args = []string{"test"}
	defer func() { os.Args = origArgs }()

	upstreams, metricsAddr, proxyAddr := ParseFlags()
	if len(upstreams) != 0 {
		t.Fatalf("expected empty upstreams, got %v", upstreams)
	}
	if metricsAddr != ":7070" {
		t.Fatalf("expected default metrics-addr :7070, got %q", metricsAddr)
	}
	if proxyAddr != ":8080" {
		t.Fatalf("expected default proxy-addr :8080, got %q", proxyAddr)
	}
}

func TestParseFlagsWithUpstreams(t *testing.T) {
	origArgs := os.Args
	os.Args = []string{"test", "-upstreams", "http://example.com"}
	defer func() { os.Args = origArgs }()

	upstreams, _, _ := ParseFlags()
	if len(upstreams) != 1 || upstreams[0] != "http://example.com" {
		t.Fatalf("expected [http://example.com], got %v", upstreams)
	}
}

func TestParseFlagsMultipleUpstreams(t *testing.T) {
	origArgs := os.Args
	os.Args = []string{"test", "-upstreams", "http://a.com,http://b.com"}
	defer func() { os.Args = origArgs }()

	upstreams, _, _ := ParseFlags()
	if len(upstreams) != 2 {
		t.Fatalf("expected 2 upstreams, got %d: %v", len(upstreams), upstreams)
	}
	if upstreams[0] != "http://a.com" {
		t.Fatalf("expected first=http://a.com, got %s", upstreams[0])
	}
	if upstreams[1] != "http://b.com" {
		t.Fatalf("expected second=http://b.com, got %s", upstreams[1])
	}
}

func TestParseFlagsWithAddrs(t *testing.T) {
	origArgs := os.Args
	os.Args = []string{"test", "-metrics-addr=:9090", "-proxy-addr=:8081"}
	defer func() { os.Args = origArgs }()

	_, metricsAddr, proxyAddr := ParseFlags()
	if metricsAddr != ":9090" {
		t.Fatalf("expected :9090, got %q", metricsAddr)
	}
	if proxyAddr != ":8081" {
		t.Fatalf("expected :8081, got %q", proxyAddr)
	}
}

func TestParseFlagsWithSpaces(t *testing.T) {
	origArgs := os.Args
	os.Args = []string{"test", "-upstreams", " http://a.com , http://b.com "}
	defer func() { os.Args = origArgs }()

	upstreams, _, _ := ParseFlags()
	if len(upstreams) != 2 {
		t.Fatalf("expected 2 upstreams, got %d: %v", len(upstreams), upstreams)
	}
	if upstreams[0] != "http://a.com" {
		t.Fatalf("expected first=http://a.com, got %s", upstreams[0])
	}
	if upstreams[1] != "http://b.com" {
		t.Fatalf("expected second=http://b.com, got %s", upstreams[1])
	}
}

func TestParseUpstreams(t *testing.T) {
	origArgs := os.Args
	os.Args = []string{"test", "-upstreams", "http://example.com"}
	defer func() { os.Args = origArgs }()

	result := ParseUpstreams()
	if len(result) != 1 || result[0] != "http://example.com" {
		t.Fatalf("expected [http://example.com], got %v", result)
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
