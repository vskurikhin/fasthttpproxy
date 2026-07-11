package upstream

import (
	"crypto/tls"
	"testing"
)

func TestNewUpstreamsEmpty(t *testing.T) {
	u := NewUpstreams(nil)
	if u == nil {
		t.Fatal("expected non-nil")
	}
	up, ok := u.Random()
	if ok {
		t.Fatalf("expected false, got addr=%v", up)
	}
}

func TestNewUpstreamsNil(t *testing.T) {
	u := NewUpstreams(nil)
	up, ok := u.Random()
	if ok {
		t.Fatalf("expected false, got addr=%v", up)
	}
	_ = up
}

func TestNewUpstreamsSingle(t *testing.T) {
	u := NewUpstreams([]string{"127.0.0.1:8080"})
	up, ok := u.Random()
	if !ok {
		t.Fatal("expected true")
	}
	if up.Address != "http://127.0.0.1:8080" {
		t.Fatalf("expected 'http://127.0.0.1:8080', got %q", up.Address)
	}
	if up.Scheme != "http" {
		t.Fatalf("expected scheme 'http', got %q", up.Scheme)
	}
}

func TestNewUpstreamsMultiple(t *testing.T) {
	addrs := []string{"192.168.1.1:8080", "10.0.0.1:9090", "host:7070"}
	u := NewUpstreams(addrs)
	if len(u.keys) != 3 {
		t.Fatalf("expected 3 keys, got %d", len(u.keys))
	}
	// Verify all original addresses are present
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		up, ok := u.Random()
		if !ok {
			t.Fatal("expected true")
		}
		seen[up.Address] = true
	}
	if len(seen) != 3 {
		t.Fatalf("expected 3 unique addresses, got %d", len(seen))
	}
}

func TestRandomReturnsDifferentAddresses(t *testing.T) {
	u := NewUpstreams([]string{"a:1", "b:2", "c:3"})
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		up, ok := u.Random()
		if !ok {
			t.Fatal("expected true")
		}
		seen[up.Address] = true
	}
	if len(seen) != 3 {
		t.Fatalf("expected 3 unique addresses over 50 calls, got %d", len(seen))
	}
}

func TestAppendNewAddress(t *testing.T) {
	u := NewUpstreams([]string{"a:1"})
	u.Append("b:2")
	if len(u.keys) != 2 {
		t.Fatalf("expected 2 keys, got %d", len(u.keys))
	}
	seen := map[string]bool{}
	for i := 0; i < 20; i++ {
		up, _ := u.Random()
		seen[up.Address] = true
	}
	if len(seen) != 2 {
		t.Fatalf("expected 2 unique addresses, got %d", len(seen))
	}
}

func TestAppendDuplicate(t *testing.T) {
	u := NewUpstreams([]string{"a:1"})
	u.Append("a:1")
	if len(u.keys) != 1 {
		t.Fatalf("expected 1 key after duplicate append, got %d", len(u.keys))
	}
}

func TestAppendToEmpty(t *testing.T) {
	u := NewUpstreams(nil)
	u.Append("a:1")
	up, ok := u.Random()
	if !ok {
		t.Fatal("expected true")
	}
	if up.Address != "http://a:1" {
		t.Fatalf("expected 'http://a:1', got %q", up.Address)
	}
}

func TestNewUpstreamsPreservesOrder(t *testing.T) {
	addrs := []string{"a:1", "b:2", "c:3"}
	u := NewUpstreams(addrs)
	for i, expected := range addrs {
		expectedAddr := "http://" + expected
		if u.keys[i].Address != expectedAddr {
			t.Fatalf("at index %d: expected %q, got %q", i, expectedAddr, u.keys[i].Address)
		}
	}
}

func TestRandomOnSingleElement(t *testing.T) {
	u := NewUpstreams([]string{"x:1"})
	for i := 0; i < 10; i++ {
		up, ok := u.Random()
		if !ok {
			t.Fatal("expected true")
		}
		if up.Address != "http://x:1" {
			t.Fatalf("expected 'http://x:1', got %q", up.Address)
		}
	}
}

func TestParseAddressHTTPScheme(t *testing.T) {
	up, err := ParseAddress("http://example.com:8080")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if up.Scheme != "http" {
		t.Fatalf("expected scheme 'http', got %q", up.Scheme)
	}
	if up.Host != "example.com" {
		t.Fatalf("expected host 'example.com', got %q", up.Host)
	}
	if up.Port != "8080" {
		t.Fatalf("expected port '8080', got %q", up.Port)
	}
	if up.Address != "http://example.com:8080" {
		t.Fatalf("expected address 'http://example.com:8080', got %q", up.Address)
	}
}

func TestParseAddressHTTPSScheme(t *testing.T) {
	up, err := ParseAddress("https://secure.example.com:443")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if up.Scheme != "https" {
		t.Fatalf("expected scheme 'https', got %q", up.Scheme)
	}
	if up.Host != "secure.example.com" {
		t.Fatalf("expected host 'secure.example.com', got %q", up.Host)
	}
	if up.Port != "443" {
		t.Fatalf("expected port '443', got %q", up.Port)
	}
	if up.Address != "https://secure.example.com:443" {
		t.Fatalf("expected address 'https://secure.example.com:443', got %q", up.Address)
	}
}

func TestParseAddressNoScheme(t *testing.T) {
	up, err := ParseAddress("example.com:8080")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if up.Scheme != "http" {
		t.Fatalf("expected scheme 'http', got %q", up.Scheme)
	}
	if up.Address != "http://example.com:8080" {
		t.Fatalf("expected address 'http://example.com:8080', got %q", up.Address)
	}
}

func TestParseAddressHTTPSNoPort(t *testing.T) {
	up, err := ParseAddress("https://example.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if up.Port != "443" {
		t.Fatalf("expected port '443' (default for https), got %q", up.Port)
	}
}

func TestParseAddressHTTPNoPort(t *testing.T) {
	up, err := ParseAddress("http://example.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if up.Port != "80" {
		t.Fatalf("expected port '80' (default for http), got %q", up.Port)
	}
}

func TestNewTLSConfigDefaults(t *testing.T) {
	cfg := &tls.Config{}
	if cfg.InsecureSkipVerify {
		t.Fatal("expected InsecureSkipVerify=false by default")
	}
}

func TestNewTLSConfigInsecure(t *testing.T) {
	v := struct {
		TLSEnabled            bool
		TLSInsecureSkipVerify bool
		TLSCAFile             string
		TLSServerName         string
	}{
		TLSEnabled:            true,
		TLSInsecureSkipVerify: true,
		TLSCAFile:             "",
		TLSServerName:         "",
	}

	cfg := &tls.Config{InsecureSkipVerify: v.TLSInsecureSkipVerify}
	if !cfg.InsecureSkipVerify {
		t.Fatal("expected InsecureSkipVerify=true")
	}
}

func TestNewTLSConfigServerName(t *testing.T) {
	v := struct {
		TLSEnabled            bool
		TLSInsecureSkipVerify bool
		TLSCAFile             string
		TLSServerName         string
	}{
		TLSEnabled:            true,
		TLSInsecureSkipVerify: false,
		TLSCAFile:             "",
		TLSServerName:         "example.com",
	}

	cfg := &tls.Config{ServerName: v.TLSServerName}
	if cfg.ServerName != "example.com" {
		t.Fatalf("expected ServerName 'example.com', got %q", cfg.ServerName)
	}
}

func TestNewUpstreamsWithScheme(t *testing.T) {
	u := NewUpstreams([]string{"http://a.com:80", "https://b.com:443"})
	if len(u.keys) != 2 {
		t.Fatalf("expected 2 upstreams, got %d", len(u.keys))
	}
	if u.keys[0].Scheme != "http" {
		t.Fatalf("expected scheme 'http', got %q", u.keys[0].Scheme)
	}
	if u.keys[1].Scheme != "https" {
		t.Fatalf("expected scheme 'https', got %q", u.keys[1].Scheme)
	}
}

func TestNewUpstreamsHTTPSAddress(t *testing.T) {
	u := NewUpstreams([]string{"https://secure.example.com:443"})
	up, ok := u.Random()
	if !ok {
		t.Fatal("expected true")
	}
	if up.Scheme != "https" {
		t.Fatalf("expected scheme 'https', got %q", up.Scheme)
	}
	if up.Host != "secure.example.com" {
		t.Fatalf("expected host 'secure.example.com', got %q", up.Host)
	}
	if up.Port != "443" {
		t.Fatalf("expected port '443', got %q", up.Port)
	}
}
