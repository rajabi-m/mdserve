// Package server serves the single-page Markdown reader and its data endpoints,
// and renders the static build. Markdown is rendered in the browser (marked.js)
// for the live reader; the static build renders it with internal/markdown.
package server

import (
	"bufio"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mostafa-re/mdserve/internal/ansi"
	"github.com/mostafa-re/mdserve/internal/web"
)

// faviconSVG is the mdserve mark, served at /favicon.svg and /favicon.ico so a
// browser's default favicon probe gets a 200 instead of a 404. (The in-page
// favicon is a theme-colored data-URI managed by the client.)
const faviconSVG = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 32 32"><rect width="32" height="32" rx="7" fill="#2f81f7"/><path d="M9 11h14M9 16h14M9 21h9" fill="none" stroke="#fff" stroke-width="2.4" stroke-linecap="round"/></svg>`

// Options configures a Server.
type Options struct {
	Dir        string   // directory of .md files
	DefaultDoc string   // rel path opened first when none is in the URL hash / history
	Reload     bool     // when true (serve), the page polls /api/poll for live-reload
	Exclude    []string // glob patterns of dirs/files to hide from serving and build
	Version    string   // build version string, substituted into the reader's footer
}

// Server serves the single-page Markdown reader and its data endpoints. Markdown
// is rendered in the browser (marked.js) from /raw; the file tree comes from
// /api/tree; change detection from /api/poll; vendor assets from /vendor/. No
// file is cached server-side — each request re-reads from disk.
type Server struct {
	root    *os.Root
	docDir  string
	opts    Options
	exclude []string // validated, lowercased copy of opts.Exclude (empty dropped)
	policy  string
	page    string // pageHTML with the per-server sentinels substituted
}

// NewServer constructs a Server rooted at opts.Dir.
func NewServer(opts Options) (*Server, error) {
	if opts.DefaultDoc == "" {
		opts.DefaultDoc = "README.md"
	}
	abs, err := filepath.Abs(opts.Dir)
	if err != nil {
		return nil, fmt.Errorf("abs(%s): %w", opts.Dir, err)
	}
	if info, err := os.Stat(abs); err != nil {
		return nil, fmt.Errorf("stat %s: %w", abs, err)
	} else if !info.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", abs)
	}
	var exclude []string
	for _, pat := range opts.Exclude {
		pat = strings.TrimSpace(pat)
		if pat == "" {
			continue
		}
		if _, err := path.Match(pat, "x"); err != nil {
			return nil, fmt.Errorf("invalid --exclude pattern %q: %w", pat, err)
		}
		// lowercase so matching is case-insensitive, matching how case-insensitive
		// filesystems (macOS/Windows) resolve the path — otherwise an excluded file
		// stays directly fetchable via a differently-cased URL (e.g. Secret/x.md).
		exclude = append(exclude, strings.ToLower(pat))
	}
	reload := "false"
	if opts.Reload {
		reload = "true"
	}
	page := strings.NewReplacer(
		"__RELOAD__", reload,
		"__DEFAULT__", jsonString(opts.DefaultDoc),
		"__VERSION__", html.EscapeString(opts.Version),
	).Replace(web.PageHTML)
	root, err := os.OpenRoot(abs)
	if err != nil {
		return nil, err
	}
	return &Server{root: root, docDir: abs, opts: opts, exclude: exclude, page: page, policy: web.ContentPolicy(page) + "; frame-ancestors 'none'"}, nil
}

// isExcluded reports whether a slash-separated path under docDir matches any
// --exclude pattern: either the full relpath, or (for slashless patterns) any
// single path segment — so "node_modules" prunes that dir and its whole subtree
// at any depth, "*.private.md" matches by filename, "drafts/*" by path. Matching
// is case-insensitive (patterns are lowercased in NewServer).
func (s *Server) isExcluded(rel string) bool {
	if len(s.exclude) == 0 {
		return false
	}
	rel = strings.ToLower(path.Clean(rel))
	if rel == "." || rel == "/" {
		return false
	}
	segs := strings.Split(rel, "/")
	for _, pat := range s.exclude {
		if ok, _ := path.Match(pat, rel); ok {
			return true
		}
		if !strings.Contains(pat, "/") {
			for _, seg := range segs {
				if ok, _ := path.Match(pat, seg); ok {
					return true
				}
			}
		}
	}
	return false
}

// ServeHTTP routes:
//
//	GET /                 → the reader shell (HTML)
//	GET /api/tree         → JSON folder tree {root, rootPath, tree:[...]}
//	GET /api/poll         → JSON {relpath: mtime} for live-reload
//	GET /raw?path=<rel>   → raw Markdown bytes (path-safe, .md only)
//	GET /vendor/<name>    → embedded vendor asset
//	anything else         → 404
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Content-Security-Policy", s.policy)
	if r.URL.Path == "/file" || r.URL.Path == "/raw" {
		// Standalone HTML/SVG assets must not become executable same-origin pages.
		w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox; frame-ancestors 'none'")
	}
	switch p := r.URL.Path; {
	case p == "/" || p == "/index.html":
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, s.page)
	case p == "/favicon.svg" || p == "/favicon.ico":
		w.Header().Set("Content-Type", "image/svg+xml")
		w.Header().Set("Cache-Control", "public, max-age=86400")
		_, _ = io.WriteString(w, faviconSVG)
	case p == "/api/tree":
		writeJSON(w, map[string]any{
			"root":     filepath.Base(s.docDir),
			"rootPath": s.docDir,
			"tree":     s.buildAPITree(),
		})
	case p == "/api/poll":
		writeJSON(w, s.flattenMtimes())
	case p == "/raw":
		s.serveRaw(w, r)
	case p == "/file":
		s.serveFile(w, r)
	case strings.HasPrefix(p, "/vendor/"):
		web.ServeVendor(w, r, strings.TrimPrefix(p, "/vendor/"))
	default:
		http.NotFound(w, r)
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(v)
}

// serveRaw returns the raw Markdown for ?path=<rel>, resolved safely inside
// docDir.
func (s *Server) serveRaw(w http.ResponseWriter, r *http.Request) {
	rel := r.URL.Query().Get("path")
	if !strings.HasSuffix(strings.ToLower(rel), ".md") {
		http.NotFound(w, r)
		return
	}
	b, err := s.readPath(rel)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(b)
}

// serveFile serves any file referenced by a doc (images, etc.) from ?path=<rel>,
// resolved safely inside docDir — so ![](diagram.png), <img src="x.svg"> and
// links to local assets work. http.ServeContent handles content types and ranges on the opened handle.
func (s *Server) serveFile(w http.ResponseWriter, r *http.Request) {
	f, err := s.openPath(r.URL.Query().Get("path"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		http.NotFound(w, r)
		return
	}
	http.ServeContent(w, r, info.Name(), info.ModTime(), f)
}

// apiNode is one entry in the /api/tree JSON: a directory (with Children) or a
// .md leaf (with Mtime). Shapes match the reader's client expectations.
type apiNode struct {
	Type     string    `json:"type"`
	Name     string    `json:"name"`
	Relpath  string    `json:"relpath"`
	Mtime    float64   `json:"mtime,omitempty"`
	Children []apiNode `json:"children,omitempty"`
}

// buildAPITree builds a recursive .md tree under the anchored root: directories first
// (pruned when they contain no markdown), then files, each group sorted
// case-insensitively. Hidden dirs and the build output (_site) are skipped.
func (s *Server) buildAPITree() []apiNode {
	tree := []apiNode{}
	_ = s.walkFiles(".", func(rel string, info os.FileInfo) error {
		if !strings.HasSuffix(strings.ToLower(rel), ".md") {
			return nil
		}
		nodes := &tree
		parts := strings.Split(rel, "/")
		for i, part := range parts[:len(parts)-1] {
			idx := -1
			for j := range *nodes {
				if (*nodes)[j].Type == "dir" && (*nodes)[j].Name == part {
					idx = j
					break
				}
			}
			if idx < 0 {
				*nodes = append(*nodes, apiNode{Type: "dir", Name: part, Relpath: strings.Join(parts[:i+1], "/"), Children: []apiNode{}})
				idx = len(*nodes) - 1
			}
			nodes = &(*nodes)[idx].Children
		}
		*nodes = append(*nodes, apiNode{Type: "file", Name: info.Name(), Relpath: rel, Mtime: float64(info.ModTime().UnixNano()) / 1e9})
		return nil
	})
	var order func([]apiNode)
	order = func(nodes []apiNode) {
		sort.Slice(nodes, func(i, j int) bool {
			if nodes[i].Type != nodes[j].Type {
				return nodes[i].Type == "dir"
			}
			return strings.ToLower(nodes[i].Name) < strings.ToLower(nodes[j].Name)
		})
		for i := range nodes {
			order(nodes[i].Children)
		}
	}
	order(tree)
	return tree
}

// Dir returns the absolute root directory the server is serving.
func (s *Server) Dir() string { return s.docDir }

// Stats counts the .md files served and the distinct directories that contain
// them (including the root) — reported at startup.
func (s *Server) Stats() (files, dirs int) {
	m := s.flattenMtimes()
	files = len(m)
	seen := map[string]struct{}{".": {}}
	for rel := range m {
		for d := path.Dir(rel); d != "." && d != "/" && d != ""; d = path.Dir(d) {
			seen[d] = struct{}{}
		}
	}
	return files, len(seen)
}

// extractTitle returns the first H1, or "" if the file doesn't lead with one.
func extractTitle(src []byte) string {
	scanner := bufio.NewScanner(strings.NewReader(string(src)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "# ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "# "))
		}
	}
	return ""
}

// jsonString uses encoding/json HTML escaping so even script terminators remain data.
func jsonString(value string) string {
	b, _ := json.Marshal(value)
	return string(b)
}

// statusWriter records the response status code for request logging.
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(b)
}

func statusColor(c int) string {
	switch {
	case c >= 500:
		return "31" // red
	case c >= 400:
		return "33" // yellow
	case c >= 300:
		return "36" // cyan
	default:
		return "32" // green
	}
}

func durColor(d time.Duration) string {
	switch {
	case d < time.Millisecond:
		return "90" // gray (fast)
	case d < 25*time.Millisecond:
		return "32" // green
	case d < 150*time.Millisecond:
		return "33" // yellow
	default:
		return "31" // red
	}
}

// logRequests prints one aligned, colorized line per request — like a structured
// access logger:
//
//	15:04:05.000  200  GET   1.204ms  /raw?path=README.md
//
// Columns: time (gray), status (by class), method (gray), duration (by speed),
// path. The 2s poll and the static vendor assets are skipped so the log stays a
// readable record of page / doc / API views.
func LogRequests(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/poll" || strings.HasPrefix(r.URL.Path, "/vendor/") {
			h.ServeHTTP(w, r)
			return
		}
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w}
		h.ServeHTTP(sw, r)
		if sw.status == 0 {
			sw.status = http.StatusOK
		}
		dur := time.Since(start).Round(time.Microsecond)
		target := r.URL.Path
		if r.URL.RawQuery != "" {
			target += "?" + r.URL.RawQuery
		}
		fmt.Printf("%s  %s  %s  %s  %s\n",
			ansi.Paint("90", start.Format("15:04:05.000")),
			ansi.Paint(statusColor(sw.status), fmt.Sprintf("%3d", sw.status)),
			ansi.Paint("90", fmt.Sprintf("%-4s", r.Method)),
			ansi.Paint(durColor(dur), fmt.Sprintf("%10s", dur.String())),
			target,
		)
	})
}
