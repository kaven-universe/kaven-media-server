// Package referer implements optional image hotlink filtering, not authentication.
package referer

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
)

type Policy struct{ domains []string }

// Parse accepts a bounded JSON array of DNS names or IP literals.
func Parse(value string) ([]string, error) {
	if value == "" {
		return nil, nil
	}
	if len(value) > 16*1024 || !strings.HasPrefix(strings.TrimSpace(value), "[") {
		return nil, fmt.Errorf("expected a domain-name JSON array of at most 16 KiB")
	}
	var domains []string
	if err := json.Unmarshal([]byte(value), &domains); err != nil {
		return nil, fmt.Errorf("parse domain-name array: %w", err)
	}
	p, err := New(domains)
	if err != nil {
		return nil, err
	}
	return p.domains, nil
}

func New(domains []string) (*Policy, error) {
	if len(domains) > 64 {
		return nil, fmt.Errorf("at most 64 allowed domain names are supported")
	}
	p := &Policy{}
	for _, domain := range domains {
		domain = strings.ToLower(domain)
		if !validHost(domain) {
			return nil, fmt.Errorf("allowed domain must be a DNS name or IP literal without scheme, port, wildcard, or path")
		}
		p.domains = append(p.domains, domain)
	}
	return p, nil
}

func validHost(host string) bool {
	if ip, err := netip.ParseAddr(host); err == nil {
		return ip.Zone() == ""
	}
	if len(host) == 0 || len(host) > 253 {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, c := range label {
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
				return false
			}
		}
	}
	return true
}

func (p *Policy) Protect(next http.Handler) http.Handler {
	if len(p.domains) == 0 {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Vary", "Referer")
		values := r.Header.Values("Referer")
		if len(values) == 0 || len(values) == 1 && values[0] == "" {
			next.ServeHTTP(w, r)
			return
		}
		if len(values) == 1 && p.allowed(values[0]) {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusForbidden)
		if r.Method != http.MethodHead {
			_, _ = w.Write([]byte("Forbidden"))
		}
	})
}

func (p *Policy) allowed(value string) bool {
	if len(value) > 8192 {
		return false
	}
	u, err := url.Parse(value)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.Fragment != "" || u.Host == "" {
		return false
	}
	host := strings.ToLower(u.Hostname())
	if !validHost(host) {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip, ipErr := netip.ParseAddr(host)
	if ipErr == nil && (ip.Unmap().IsPrivate() || ip.Unmap().IsLoopback()) {
		return true
	}
	for _, domain := range p.domains {
		if host == domain {
			return true
		}
		if _, err := netip.ParseAddr(domain); err != nil && ipErr != nil && strings.HasSuffix(host, "."+domain) {
			return true
		}
	}
	return false
}
