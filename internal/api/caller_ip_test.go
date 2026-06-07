package api

import (
	"net/http"
	"testing"
)

func TestCallerIPResolverUsesRemoteAddressByDefault(t *testing.T) {
	resolver, err := newCallerIPResolver(nil)
	if err != nil {
		t.Fatalf("open caller IP resolver: %v", err)
	}
	header := http.Header{}
	header.Set("X-Forwarded-For", "203.0.113.10")

	got := resolver.resolve("127.0.0.1:12345", header)
	if got != "127.0.0.1" {
		t.Fatalf("expected untrusted remote address, got %q", got)
	}
}

func TestCallerIPResolverUsesForwardedForFromTrustedProxy(t *testing.T) {
	resolver, err := newCallerIPResolver([]string{"127.0.0.1", "10.0.0.0/8"})
	if err != nil {
		t.Fatalf("open caller IP resolver: %v", err)
	}
	header := http.Header{}
	header.Set("X-Forwarded-For", "203.0.113.10, 10.0.0.2")
	header.Set("X-Real-IP", "198.51.100.20")

	got := resolver.resolve("127.0.0.1:12345", header)
	if got != "203.0.113.10" {
		t.Fatalf("expected first forwarded IP, got %q", got)
	}
}

func TestCallerIPResolverUsesRealIPWhenForwardedForIsMissing(t *testing.T) {
	resolver, err := newCallerIPResolver([]string{"127.0.0.1"})
	if err != nil {
		t.Fatalf("open caller IP resolver: %v", err)
	}
	header := http.Header{}
	header.Set("X-Real-IP", "198.51.100.20")

	got := resolver.resolve("127.0.0.1:12345", header)
	if got != "198.51.100.20" {
		t.Fatalf("expected real IP, got %q", got)
	}
}

func TestCallerIPResolverRejectsInvalidTrustedProxy(t *testing.T) {
	_, err := newCallerIPResolver([]string{"not-an-ip"})
	if err == nil {
		t.Fatal("expected invalid trusted proxy to fail")
	}
}
