package api

import (
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

type callerIPResolver struct {
	trustedProxies trustedProxySet
}

func newCallerIPResolver(values []string) (callerIPResolver, error) {
	trustedProxies, err := newTrustedProxySet(values)
	if err != nil {
		return callerIPResolver{}, err
	}

	return callerIPResolver{trustedProxies: trustedProxies}, nil
}

func (r callerIPResolver) resolve(remoteAddr string, header http.Header) string {
	remoteIP, remoteAddrParsed := parseIPFromAddress(remoteAddr)
	if !remoteAddrParsed {
		return strings.TrimSpace(remoteAddr)
	}
	if !r.trustedProxies.contains(remoteIP) {
		return remoteIP.String()
	}

	if forwardedIP, ok := firstHeaderIP(header.Get("X-Forwarded-For")); ok {
		return forwardedIP.String()
	}
	if realIP, ok := parseIPFromAddress(header.Get("X-Real-IP")); ok {
		return realIP.String()
	}

	return remoteIP.String()
}

type trustedProxySet struct {
	prefixes []netip.Prefix
}

func newTrustedProxySet(values []string) (trustedProxySet, error) {
	prefixes := make([]netip.Prefix, 0, len(values))
	for _, value := range values {
		prefix, err := parseTrustedProxy(value)
		if err != nil {
			return trustedProxySet{}, err
		}
		prefixes = append(prefixes, prefix)
	}

	return trustedProxySet{prefixes: prefixes}, nil
}

func (s trustedProxySet) contains(addr netip.Addr) bool {
	addr = normalizeAddr(addr)
	for _, prefix := range s.prefixes {
		if prefix.Contains(addr) {
			return true
		}
	}

	return false
}

func parseTrustedProxy(value string) (netip.Prefix, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return netip.Prefix{}, fmt.Errorf("trusted proxy is empty")
	}
	if strings.Contains(value, "/") {
		prefix, err := netip.ParsePrefix(value)
		if err != nil {
			return netip.Prefix{}, fmt.Errorf("parse trusted proxy prefix %q: %w", value, err)
		}
		normalized, ok := normalizeTrustedProxyPrefix(prefix)
		if !ok {
			return netip.Prefix{}, fmt.Errorf("parse trusted proxy prefix %q: IPv4-mapped prefix is too broad", value)
		}
		return normalized, nil
	}

	addr, err := netip.ParseAddr(value)
	if err != nil {
		return netip.Prefix{}, fmt.Errorf("parse trusted proxy address %q: %w", value, err)
	}
	addr = normalizeAddr(addr)

	return netip.PrefixFrom(addr, addr.BitLen()), nil
}

func normalizeTrustedProxyPrefix(prefix netip.Prefix) (netip.Prefix, bool) {
	addr := prefix.Addr()
	bits := prefix.Bits()
	if addr.Is4In6() {
		if bits <= 96 {
			return netip.Prefix{}, false
		}
		addr = addr.Unmap()
		bits -= 96
	} else {
		addr = normalizeAddr(addr)
	}
	if bits == 0 {
		return netip.Prefix{}, false
	}

	normalized := netip.PrefixFrom(addr, bits)
	if !normalized.IsValid() {
		return netip.Prefix{}, false
	}

	return normalized.Masked(), true
}

func firstHeaderIP(value string) (netip.Addr, bool) {
	if strings.TrimSpace(value) == "" {
		return netip.Addr{}, false
	}

	first, _, _ := strings.Cut(value, ",")
	return parseIPFromAddress(first)
}

func parseIPFromAddress(value string) (netip.Addr, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return netip.Addr{}, false
	}

	if addr, err := netip.ParseAddr(value); err == nil {
		return normalizeAddr(addr), true
	}
	if host, _, err := net.SplitHostPort(value); err == nil {
		if addr, parseErr := netip.ParseAddr(host); parseErr == nil {
			return normalizeAddr(addr), true
		}
	}

	return netip.Addr{}, false
}

func normalizeAddr(addr netip.Addr) netip.Addr {
	if addr.Is4In6() {
		return addr.Unmap()
	}

	return addr
}
