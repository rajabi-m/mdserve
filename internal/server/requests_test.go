package server

import (
	"net"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRequestGuard(t *testing.T) {
	srv, _ := newTestServer(t)
	h, err := GuardRequests(srv, &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 8123}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, route := range []string{"/", "/api/tree", "/api/poll", "/raw?path=README.md", "/file?path=README.md", "/vendor/marked.min.js"} {
		for _, c := range []struct {
			host, origin, site, method string
			want                       int
		}{
			{"127.0.0.1:8123", "", "", "GET", 200},
			{"localhost:8123", "http://localhost:8123", "same-origin", "GET", 200},
			{"[::1]:8123", "", "", "HEAD", 200},
			{"attacker.invalid:8123", "", "", "GET", 403},
			{"localhost:8124", "", "", "GET", 403},
			{"localhost", "", "", "GET", 403},
			{"localhost:8123", "http://attacker.invalid:8123", "", "GET", 403},
			{"localhost:8123", "http://127.0.0.1:8123", "", "GET", 403},
			{"localhost:8123", "null", "", "GET", 403},
			{"localhost:8123", "http://localhost:8123/", "", "GET", 403},
			{"localhost:8123", "https://localhost:8123", "", "GET", 403},
			{"localhost:8123", "", "cross-site", "GET", 403},
			{"localhost:8123", "", "same-site", "GET", 403},
			{"localhost:8123", "", "none", "GET", 200},
			{"localhost:8123", "", "", "POST", 405},
		} {
			t.Run(route+"/"+c.host+"/"+c.origin+"/"+c.method+"/"+c.site, func(t *testing.T) {
				r := httptest.NewRequest(c.method, "http://"+c.host+route, nil)
				if c.origin != "" {
					r.Header.Set("Origin", c.origin)
				}
				if c.site != "" {
					r.Header.Set("Sec-Fetch-Site", c.site)
				}
				w := httptest.NewRecorder()
				h.ServeHTTP(w, r)
				if w.Code != c.want {
					t.Fatalf("status %d, want %d", w.Code, c.want)
				}
				if c.want != 200 && (strings.Contains(w.Body.String(), "welcome") || strings.Contains(w.Body.String(), srv.Dir())) {
					t.Fatal("rejection leaks document/metadata")
				}
				if w.Header().Get("Access-Control-Allow-Origin") != "" {
					t.Fatal("unexpected CORS")
				}
				if c.method == "HEAD" && w.Body.Len() != 0 {
					t.Fatal("HEAD body")
				}
			})
		}
	}
}

func TestCustomRequestHosts(t *testing.T) {
	srv, _ := newTestServer(t)
	for _, ip := range []string{"0.0.0.0", "::", "192.0.2.10", "::1"} {
		h, err := GuardRequests(srv, &net.TCPAddr{IP: net.ParseIP(ip), Port: 9000}, []string{"docs.example"})
		if err != nil {
			t.Fatal(err)
		}
		for host, want := range map[string]int{"docs.example:9000": 200, "evil.example:9000": 403, "localhost:9000": 200} {
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest("GET", "http://"+host+"/api/tree", nil))
			if w.Code != want {
				t.Errorf("%s %s = %d", ip, host, w.Code)
			}
		}
	}
	for _, bad := range []string{"*", "http://docs.example", "docs.example:80", "docs.example/path", "", "docs.example.", "a b"} {
		if _, err := GuardRequests(srv, &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 9000}, []string{bad}); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}
