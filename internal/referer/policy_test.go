package referer

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRejectMalformedReferers(t *testing.T) {
	p, _ := New([]string{"example.com"})
	for _, values := range [][]string{
		{"/relative"}, {"not a URL"}, {"ftp://example.com/"}, {"https://example.com@evil.test/"},
		{"https://user@example.com/"}, {"https://example.com/#fragment"}, {"https://example.com:bad/"},
		{"https://example.com/", "https://evil.test/"}, {strings.Repeat("a", 8193)}, {"https://example.com./"},
	} {
		t.Run(strings.Join(values, ",")[:min(80, len(strings.Join(values, ",")))], func(t *testing.T) {
			request := httptest.NewRequest("HEAD", "/", nil)
			request.Header["Referer"] = values
			response := httptest.NewRecorder()
			p.Protect(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("denied request reached handler") })).ServeHTTP(response, request)
			if response.Code != 403 || response.Body.Len() != 0 {
				t.Fatalf("response = %d %q", response.Code, response.Body.String())
			}
		})
	}
}

func TestParseConfiguration(t *testing.T) {
	for _, value := range []string{"null", "{}", `[""]`, `["*.example.com"]`, `["https://example.com"]`, `["example.com:80"]`, `["a..com"]`, `["-bad.com"]`, `["example.com."]`, `["bad name"]`, strings.Repeat(" ", 16385)} {
		if _, err := Parse(value); err == nil {
			t.Fatalf("accepted invalid configuration %q", value[:min(80, len(value))])
		}
	}
	if _, err := New(make([]string, 65)); err == nil {
		t.Fatal("accepted too many domains")
	}
	for _, value := range []string{"", "[]", `["EXAMPLE.com","127.0.0.1","::1"]`} {
		if _, err := Parse(value); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPolicyDefaultsAndIPMatching(t *testing.T) {
	for _, tc := range []struct {
		domains []string
		referer string
		allowed bool
	}{
		{nil, "invalid", true}, {[]string{"EXAMPLE.COM"}, "https://Sub.Example.Com:8443/", true},
		{[]string{"example.com"}, "http://[::1]/", true}, {[]string{"example.com"}, "http://172.16.1.1/", true},
		{[]string{"example.com"}, "http://172.32.1.1/", false}, {[]string{"203.0.113.1"}, "http://203.0.113.1/", true},
		{[]string{"203.0.113.1"}, "http://evil.203.0.113.1/", false},
	} {
		p, err := New(tc.domains)
		if err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest("GET", "/", nil)
		request.Header.Set("Referer", tc.referer)
		called := false
		p.Protect(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true })).ServeHTTP(httptest.NewRecorder(), request)
		if called != tc.allowed {
			t.Fatalf("referer %q allowed = %v", tc.referer, called)
		}
	}
}
