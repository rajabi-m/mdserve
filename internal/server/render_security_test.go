package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTemplateValuesStayData(t *testing.T) {
	dir := t.TempDir()
	payload := `</script><img src=x onerror="alert(1)">\"` + "\u2028\u2029"
	srv, err := NewServer(Options{Dir: dir, DefaultDoc: payload, Version: payload})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	if strings.Contains(srv.page, payload) {
		t.Fatal("unescaped template value")
	}
	_, data, _ := strings.Cut(srv.page, "defaultDoc:")
	data, _, _ = strings.Cut(data, "};</script>")
	var value string
	if err := json.Unmarshal([]byte(data), &value); err != nil || value != payload {
		t.Fatalf("default doc not encoded as data: %v", err)
	}
	for _, title := range []string{payload, "__BODY__ __ROOT__ __TITLE__"} {
		mustWrite(t, filepath.Join(dir, "README.md"), "# "+title+"\n\n"+payload)
		out := t.TempDir()
		if err := srv.BuildStatic(out); err != nil {
			t.Fatal(err)
		}
		page, err := os.ReadFile(filepath.Join(out, "README.md.html"))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(page), `<img src=x onerror=`) {
			t.Fatal("active static payload")
		}
		if !strings.Contains(staticBody(t, page), "onerror") {
			t.Fatal("dummy fixture missing from inert JSON source")
		}
		if !strings.Contains(string(page), `http-equiv="Content-Security-Policy"`) {
			t.Fatal("static CSP missing")
		}
	}
}

func TestSecurityResponseHeaders(t *testing.T) {
	srv, dir := newTestServer(t)
	mustWrite(t, filepath.Join(dir, "active.html"), `<script>alert(1)</script>`)
	for _, route := range []string{"/", "/api/tree", "/raw?path=README.md", "/file?path=active.html", "/vendor/purify.min.js", "/missing"} {
		rec := get(t, srv, route)
		for key, want := range map[string]string{"X-Content-Type-Options": "nosniff", "Referrer-Policy": "no-referrer", "X-Frame-Options": "DENY"} {
			if rec.Header().Get(key) != want {
				t.Errorf("%s missing %s", route, key)
			}
		}
		csp := rec.Header().Get("Content-Security-Policy")
		if !strings.Contains(csp, "frame-ancestors 'none'") {
			t.Errorf("%s missing ancestry", route)
		}
		if strings.HasPrefix(route, "/file") || strings.HasPrefix(route, "/raw") {
			if !strings.Contains(csp, "sandbox") || strings.Contains(csp, "script-src") {
				t.Errorf("%s executable asset policy", route)
			}
		} else {
			if !strings.Contains(csp, "'sha256-") || strings.Contains(csp, "'unsafe-eval'") || strings.Contains(strings.Split(csp, "; style-src")[0], "'unsafe-inline'") {
				t.Errorf("%s weak script policy", route)
			}
		}
	}
}
