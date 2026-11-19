package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCheckServerRefusesPlainHTTPExceptLoopback(t *testing.T) {
	for _, s := range []string{
		"https://api.mktauto.site", "https://example.com:8443/base", "HTTPS://example.com",
		"http://localhost:8080", "http://LOCALHOST", "http://127.0.0.1:5000", "http://127.1.2.3", "http://[::1]:8080",
	} {
		if err := CheckServer(NormalizeServer(s)); err != nil {
			t.Errorf("%s must be allowed: %v", s, err)
		}
	}
	for _, s := range []string{
		"http://api.mktauto.site", "http://example.com:8080", "http://10.0.0.5", "http://192.168.1.2:8080",
		"http://[::2]", "http://localhost.evil.com", "http://127.0.0.1.nip.io", "http://0.0.0.0",
	} {
		err := CheckServer(NormalizeServer(s))
		if err == nil || !strings.Contains(err.Error(), "use https://") {
			t.Errorf("%s must be refused with advice to use https://: %v", s, err)
		}
	}
	for _, s := range []string{"ftp://example.com", "https://", "http://"} {
		if CheckServer(s) == nil {
			t.Errorf("%q isn't a usable server", s)
		}
	}
	if NormalizeServer("api.mktauto.site/") != "https://api.mktauto.site" {
		t.Error("no scheme means https://")
	}
}

func TestClientNeverSendsTheTokenOverPlainHTTP(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++ }))
	defer srv.Close()
	// The test server listens on 127.0.0.1: allowed.
	if _, err := New(srv.URL, "mkc_x", "1").do(context.Background(), http.MethodGet, "/x", nil, nil); err != nil || calls != 1 {
		t.Fatalf("loopback http: %v (%d calls)", err, calls)
	}
	c := New("http://example.com", "mkc_secret", "1")
	c.HTTP = srv.Client()
	if _, err := c.do(context.Background(), http.MethodGet, "/x", nil, nil); err == nil || !strings.Contains(err.Error(), "use https://") {
		t.Errorf("plain http to another computer must be refused: %v", err)
	}
	if calls != 1 {
		t.Error("nothing may be sent")
	}
}
