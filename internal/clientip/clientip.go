package clientip

import (
	"context"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

const maxForwardedAddresses = 32

type contextKey struct{}

// Resolver accepts forwarded client addresses only from explicitly trusted
// proxy networks. An empty Resolver always returns the direct socket peer.
type Resolver struct {
	trusted []netip.Prefix
}

func New(prefixes []string) (*Resolver, error) {
	trusted, err := ParseTrustedPrefixes(prefixes)
	if err != nil {
		return nil, err
	}
	return &Resolver{trusted: trusted}, nil
}

func (resolver *Resolver) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		ip := resolver.Resolve(request.RemoteAddr, request.Header.Get("X-Forwarded-For"))
		ctx := context.WithValue(request.Context(), contextKey{}, ip)
		next.ServeHTTP(writer, request.WithContext(ctx))
	})
}

func FromRequest(request *http.Request) string {
	if value, ok := request.Context().Value(contextKey{}).(string); ok && value != "" {
		return value
	}
	return directAddress(request.RemoteAddr)
}

func (resolver *Resolver) Resolve(remoteAddress, forwardedFor string) string {
	direct := directAddress(remoteAddress)
	peer, err := netip.ParseAddr(direct)
	if err != nil || resolver == nil || !resolver.contains(peer) || forwardedFor == "" {
		return direct
	}

	parts := strings.Split(forwardedFor, ",")
	if len(parts) > maxForwardedAddresses {
		return direct
	}
	forwarded := make([]netip.Addr, len(parts))
	for index, part := range parts {
		address, err := netip.ParseAddr(strings.TrimSpace(part))
		if err != nil {
			return direct
		}
		forwarded[index] = address.Unmap()
	}

	current := peer.Unmap()
	for index := len(forwarded) - 1; index >= 0 && resolver.contains(current); index-- {
		current = forwarded[index]
	}
	return current.String()
}

func (resolver *Resolver) contains(address netip.Addr) bool {
	address = address.Unmap()
	for _, prefix := range resolver.trusted {
		if prefix.Contains(address) {
			return true
		}
	}
	return false
}

func directAddress(remoteAddress string) string {
	if host, _, err := net.SplitHostPort(remoteAddress); err == nil {
		if address, parseErr := netip.ParseAddr(host); parseErr == nil {
			return address.Unmap().String()
		}
		return host
	}
	if value := strings.TrimSpace(remoteAddress); value != "" {
		if address, err := netip.ParseAddr(value); err == nil {
			return address.Unmap().String()
		}
		return value
	}
	return "unknown"
}
