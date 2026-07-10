package upstream

import (
	"testing"
)

func TestNewUpstreamsEmpty(t *testing.T) {
	u := NewUpstreams(nil)
	if u == nil {
		t.Fatal("expected non-nil")
	}
	addr, ok := u.Random()
	if ok {
		t.Fatalf("expected false, got addr=%q", addr)
	}
}

func TestNewUpstreamsNil(t *testing.T) {
	u := NewUpstreams(nil)
	addr, ok := u.Random()
	if ok {
		t.Fatalf("expected false, got addr=%q", addr)
	}
	_ = addr
}

func TestNewUpstreamsSingle(t *testing.T) {
	u := NewUpstreams([]string{"127.0.0.1:8080"})
	addr, ok := u.Random()
	if !ok {
		t.Fatal("expected true")
	}
	if addr != "127.0.0.1:8080" {
		t.Fatalf("expected 127.0.0.1:8080, got %q", addr)
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
		addr, ok := u.Random()
		if !ok {
			t.Fatal("expected true")
		}
		seen[addr] = true
	}
	if len(seen) != 3 {
		t.Fatalf("expected 3 unique addresses, got %d", len(seen))
	}
}

func TestRandomReturnsDifferentAddresses(t *testing.T) {
	u := NewUpstreams([]string{"a:1", "b:2", "c:3"})
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		addr, ok := u.Random()
		if !ok {
			t.Fatal("expected true")
		}
		seen[addr] = true
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
		addr, _ := u.Random()
		seen[addr] = true
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
	addr, ok := u.Random()
	if !ok {
		t.Fatal("expected true")
	}
	if addr != "a:1" {
		t.Fatalf("expected a:1, got %q", addr)
	}
}

func TestNewUpstreamsPreservesOrder(t *testing.T) {
	addrs := []string{"a:1", "b:2", "c:3"}
	u := NewUpstreams(addrs)
	for i, expected := range addrs {
		if u.keys[i] != expected {
			t.Fatalf("at index %d: expected %q, got %q", i, expected, u.keys[i])
		}
	}
}

func TestRandomOnSingleElement(t *testing.T) {
	u := NewUpstreams([]string{"x:1"})
	for i := 0; i < 10; i++ {
		addr, ok := u.Random()
		if !ok {
			t.Fatal("expected true")
		}
		if addr != "x:1" {
			t.Fatalf("expected x:1, got %q", addr)
		}
	}
}
