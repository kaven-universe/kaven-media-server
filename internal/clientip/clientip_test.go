package clientip

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestResolver(t *testing.T) {
	resolver, err := New([]string{"172.17.0.0/16", "10.0.0.0/8", "2001:db8:1::/48"})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, remote, forwarded, want string
	}{
		{name: "direct client", remote: "192.0.2.10:1234", forwarded: "198.51.100.4", want: "192.0.2.10"},
		{name: "trusted docker proxy", remote: "172.17.0.1:4321", forwarded: "198.51.100.4", want: "198.51.100.4"},
		{name: "trusted proxy chain", remote: "172.17.0.1:4321", forwarded: "198.51.100.4, 10.0.0.8", want: "198.51.100.4"},
		{name: "untrusted hop stops chain", remote: "172.17.0.1:4321", forwarded: "203.0.113.7, 198.51.100.4", want: "198.51.100.4"},
		{name: "malformed header falls back", remote: "172.17.0.1:4321", forwarded: "not-an-ip", want: "172.17.0.1"},
		{name: "IPv6", remote: "[2001:db8:1::1]:4321", forwarded: "2001:db8:2::7", want: "2001:db8:2::7"},
		{name: "missing peer", remote: "", forwarded: "198.51.100.4", want: "unknown"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := resolver.Resolve(test.remote, test.forwarded); got != test.want {
				t.Fatalf("Resolve(%q, %q) = %q, want %q", test.remote, test.forwarded, got, test.want)
			}
		})
	}
	tooLong := strings.Repeat("198.51.100.1,", maxForwardedAddresses) + "198.51.100.2"
	if got := resolver.Resolve("172.17.0.1:4321", tooLong); got != "172.17.0.1" {
		t.Fatalf("oversized chain resolved to %q", got)
	}
}

func TestMiddlewareStoresResolvedAddress(t *testing.T) {
	resolver, err := New([]string{"172.17.0.0/16"})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.RemoteAddr = "172.17.0.1:4321"
	request.Header.Set("X-Forwarded-For", "198.51.100.4")
	response := httptest.NewRecorder()
	resolver.Middleware(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = writer.Write([]byte(FromRequest(request)))
	})).ServeHTTP(response, request)
	if response.Body.String() != "198.51.100.4" {
		t.Fatalf("resolved request address = %q", response.Body.String())
	}
}

func TestParseTrustedPrefixesRejectsInvalidValues(t *testing.T) {
	for _, values := range [][]string{
		{"172.17.0.1"},
		{"172.17.1.0/16"},
		{"172.17.0.0/16", "172.17.0.0/16"},
		{"0.0.0.0/0"},
		{"::/0"},
	} {
		if _, err := ParseTrustedPrefixes(values); err == nil {
			t.Fatalf("accepted invalid prefixes %#v", values)
		}
	}
}
