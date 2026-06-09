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

func TestCallerIPResolverRejectsForwardedForWhenFirstEntryIsInvalid(t *testing.T) {
	resolver, err := newCallerIPResolver([]string{"127.0.0.1"})
	if err != nil {
		t.Fatalf("open caller IP resolver: %v", err)
	}
	header := http.Header{}
	header.Set("X-Forwarded-For", "unknown, 203.0.113.10")

	got := resolver.resolve("127.0.0.1:12345", header)
	if got != "127.0.0.1" {
		t.Fatalf("expected malformed forwarded header to fall back to remote address, got %q", got)
	}
}

func TestCallerIPResolverForwardedForTakesPriorityOverRealIP(t *testing.T) {
	resolver, err := newCallerIPResolver([]string{"127.0.0.1"})
	if err != nil {
		t.Fatalf("open caller IP resolver: %v", err)
	}
	header := http.Header{}
	header.Set("X-Forwarded-For", "203.0.113.10, unknown")
	header.Set("X-Real-IP", "198.51.100.20")

	got := resolver.resolve("127.0.0.1:12345", header)
	if got != "203.0.113.10" {
		t.Fatalf("expected valid first forwarded IP to take priority, got %q", got)
	}
}

func TestCallerIPResolverFallsBackToRealIPWhenForwardedForHasNoValidIP(t *testing.T) {
	resolver, err := newCallerIPResolver([]string{"127.0.0.1"})
	if err != nil {
		t.Fatalf("open caller IP resolver: %v", err)
	}
	header := http.Header{}
	header.Set("X-Forwarded-For", "unknown, also-invalid")
	header.Set("X-Real-IP", "198.51.100.20")

	got := resolver.resolve("127.0.0.1:12345", header)
	if got != "198.51.100.20" {
		t.Fatalf("expected real IP fallback, got %q", got)
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

func TestCallerIPResolverTrustsIPv4MappedIPv6ExactPrefix(t *testing.T) {
	resolver, err := newCallerIPResolver([]string{"::ffff:127.0.0.1/128"})
	if err != nil {
		t.Fatalf("open caller IP resolver: %v", err)
	}
	header := http.Header{}
	header.Set("X-Forwarded-For", "203.0.113.10")

	got := resolver.resolve("127.0.0.1:12345", header)
	if got != "203.0.113.10" {
		t.Fatalf("expected IPv4-mapped trusted proxy prefix to match remote address, got %q", got)
	}
}

func TestCallerIPResolverTrustsIPv4MappedIPv6NetworkPrefix(t *testing.T) {
	resolver, err := newCallerIPResolver([]string{"::ffff:127.0.0.0/120"})
	if err != nil {
		t.Fatalf("open caller IP resolver: %v", err)
	}
	header := http.Header{}
	header.Set("X-Forwarded-For", "203.0.113.10")

	got := resolver.resolve("127.0.0.42:12345", header)
	if got != "203.0.113.10" {
		t.Fatalf("expected IPv4-mapped trusted proxy network to match remote address, got %q", got)
	}
}

func TestCallerIPResolverRejectsInvalidTrustedProxy(t *testing.T) {
	_, err := newCallerIPResolver([]string{"not-an-ip"})
	if err == nil {
		t.Fatal("expected invalid trusted proxy to fail")
	}
}

func TestCallerIPResolverRejectsBroadIPv4MappedIPv6Prefix(t *testing.T) {
	_, err := newCallerIPResolver([]string{"::ffff:0.0.0.0/95"})
	if err == nil {
		t.Fatal("expected overly broad IPv4-mapped trusted proxy prefix to fail")
	}
}

func TestCallerIPResolverRejectsAllAddressTrustedProxyPrefixes(t *testing.T) {
	for _, proxy := range []string{"0.0.0.0/0", "::/0", "::ffff:0.0.0.0/96"} {
		t.Run(proxy, func(t *testing.T) {
			_, err := newCallerIPResolver([]string{proxy})
			if err == nil {
				t.Fatal("expected all-address trusted proxy prefix to fail")
			}
		})
	}
}
