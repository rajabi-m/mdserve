package server

import (
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func TestFilesystemPolicy(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	for name, body := range map[string]string{"README.md": "# ordinary", "image.png": "image", ".env": "DUMMY_PRIVATE", ".hidden.md": "DUMMY_PRIVATE", ".hidden/doc.md": "DUMMY_PRIVATE", ".git/config": "DUMMY_PRIVATE", "secret/doc.md": "DUMMY_PRIVATE", "_site/doc.md": "DUMMY_PRIVATE"} {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(outside, "outside.md"), []byte("DUMMY_OUTSIDE"), 0644); err != nil {
		t.Fatal(err)
	}
	for name, target := range map[string]string{"link.md": filepath.Join(outside, "outside.md"), "link.png": filepath.Join(outside, "outside.md"), "linked-dir": outside, "internal.md": "README.md", "hidden-link.md": ".hidden.md"} {
		if err := os.Symlink(target, filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
	}
	srv, err := NewServer(Options{Dir: dir, Exclude: []string{"secret"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { srv.Close() })
	for _, route := range []string{"/raw", "/file"} {
		for _, name := range []string{".env", ".git/config", ".hidden.md", ".hidden/doc.md", "Secret/doc.md", "README.md.", "README.md ", "secret/doc.md", "_site/doc.md", "link.md", "link.png", "linked-dir/outside.md", "internal.md", "hidden-link.md", "../README.md", "guide/../README.md", "/README.md", `..\README.md`, `C:\README.md`, "C:README.md", "README.md:stream", "../" + filepath.Base(dir) + "/README.md"} {
			t.Run(route+"/"+name, func(t *testing.T) {
				w := httptest.NewRecorder()
				srv.ServeHTTP(w, httptest.NewRequest("GET", route+"?path="+url.QueryEscape(name), nil))
				if w.Code != 404 {
					t.Errorf("got %d, want 404", w.Code)
				}
				if strings.Contains(w.Body.String(), dir) || strings.Contains(w.Body.String(), "DUMMY_") {
					t.Error("private contents or absolute error path leaked")
				}
			})
		}
	}
	for _, route := range []string{"/raw", "/file"} {
		for _, encoded := range []string{"%2e%2e%2fREADME.md", "%2fREADME.md", "%2ehidden%2fdoc.md", "%2eenv", "%2e%2e%5cREADME.md"} {
			if got := get(t, srv, route+"?path="+encoded); got.Code != 404 {
				t.Errorf("encoded path %s: %d", encoded, got.Code)
			}
		}
	}
	for _, target := range []string{"/raw?path=README.md", "/file?path=image.png"} {
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, httptest.NewRequest("GET", target, nil))
		if w.Code != 200 {
			t.Fatalf("ordinary file %s: %d", target, w.Code)
		}
	}
	for _, route := range []string{"/api/tree", "/api/poll"} {
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, httptest.NewRequest("GET", route, nil))
		for _, forbidden := range []string{".hidden", "link.md", "internal.md", "hidden-link", "secret", "_site"} {
			if strings.Contains(w.Body.String(), forbidden) {
				t.Errorf("%s includes %s", route, forbidden)
			}
		}
	}
	out := t.TempDir()
	if err := srv.BuildStatic(out); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{".env", ".hidden.md.html", ".hidden/doc.md.html", "link.md.html", "link.png", "internal.md.html", "hidden-link.md.html", "linked-dir/outside.md.html", "secret/doc.md.html", "_site/doc.md.html"} {
		if _, err := os.Lstat(filepath.Join(out, name)); !os.IsNotExist(err) {
			t.Errorf("export includes %s (%v)", name, err)
		}
	}
}

func TestRootSymlinkRemainsAnchored(t *testing.T) {
	parent := t.TempDir()
	dir := filepath.Join(parent, ".documents")
	mustWrite(t, filepath.Join(dir, "README.md"), "# allowed")
	outside := t.TempDir()
	mustWrite(t, filepath.Join(outside, "README.md"), "DUMMY_OUTSIDE")
	link := filepath.Join(parent, "root")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	srv, err := NewServer(Options{Dir: link})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	if got := get(t, srv, "/raw?path=README.md"); got.Code != 200 || got.Body.String() != "# allowed" {
		t.Fatalf("root was not anchored: %d %s", got.Code, got.Body.String())
	}
	if _, ok := srv.flattenMtimes()["README.md"]; !ok {
		t.Fatal("explicit hidden root was pruned")
	}
	out := t.TempDir()
	if err := srv.BuildStatic(out); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(out, "README.md.html"))
	if err != nil || strings.Contains(string(b), "DUMMY_OUTSIDE") || !strings.Contains(string(b), "allowed") {
		t.Fatalf("export root changed: %v", err)
	}
}

func TestConcurrentSymlinkReplacement(t *testing.T) {
	for _, directory := range []bool{false, true} {
		t.Run(map[bool]string{false: "file", true: "directory"}[directory], func(t *testing.T) {
			dir := t.TempDir()
			outside := t.TempDir()
			mustWrite(t, filepath.Join(dir, ".private", "doc.md"), "DUMMY_PRIVATE")
			mustWrite(t, filepath.Join(outside, "doc.md"), "DUMMY_OUTSIDE")
			good := filepath.Join(dir, "entry")
			rel := "entry"
			targets := []string{filepath.Join(outside, "doc.md"), ".private/doc.md"}
			if directory {
				mustWrite(t, filepath.Join(good, "doc.md"), "allowed")
				rel += "/doc.md"
				targets = []string{outside, ".private"}
			} else {
				good += ".md"
				rel += ".md"
				mustWrite(t, good, "allowed")
			}
			srv, err := NewServer(Options{Dir: dir})
			if err != nil {
				t.Fatal(err)
			}
			defer srv.Close()
			parked := good + "-parked"
			var swaps atomic.Int64
			stop := make(chan struct{})
			var wg sync.WaitGroup
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := 0; ; i++ {
					select {
					case <-stop:
						return
					default:
					}
					if os.Rename(good, parked) != nil {
						return
					}
					os.Symlink(targets[i%len(targets)], good)
					swaps.Add(1)
					os.Remove(good)
					if os.Rename(parked, good) != nil {
						return
					}
				}
			}()
			defer func() {
				close(stop)
				wg.Wait()
				if swaps.Load() == 0 {
					t.Error("no concurrent replacements executed")
				}
			}()
			for i := 0; i < 300; i++ {
				for _, route := range []string{"/raw", "/file"} {
					rec := get(t, srv, route+"?path="+url.QueryEscape(rel))
					if rec.Code != 200 && rec.Code != 404 {
						t.Fatalf("unexpected status %d", rec.Code)
					}
					if rec.Code == 200 && rec.Body.String() != "allowed" {
						t.Fatalf("replacement leaked bytes: %q", rec.Body.String())
					}
				}
			}
		})
	}
}

func TestAssetRangesAndHead(t *testing.T) {
	srv, dir := newTestServer(t)
	mustWrite(t, filepath.Join(dir, "image.png"), "0123456789")
	for _, method := range []string{"GET", "HEAD"} {
		req := httptest.NewRequest(method, "/file?path=image.png", nil)
		req.Header.Set("Range", "bytes=2-4")
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		if rec.Code != 206 || rec.Header().Get("Content-Type") != "image/png" || rec.Header().Get("Content-Range") != "bytes 2-4/10" {
			t.Fatalf("%s range response: %d %v", method, rec.Code, rec.Header())
		}
		want := "234"
		if method == "HEAD" {
			want = ""
		}
		if rec.Body.String() != want {
			t.Fatalf("%s body %q", method, rec.Body.String())
		}
	}
}

func TestBuildStaticReportsNestedAssetFailure(t *testing.T) {
	srv, dir := newTestServer(t)
	mustWrite(t, filepath.Join(dir, "images", "pic.png"), "image")
	out := t.TempDir()
	mustWrite(t, filepath.Join(out, "images"), "conflicting file")
	if err := srv.BuildStatic(out); err == nil {
		t.Fatal("nested asset copy failure was ignored")
	}
}
