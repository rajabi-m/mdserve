package web

import (
	"embed"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
)

// vendorFS holds the embedded front-end vendor bundle (marked, highlight.js,
// mermaid, KaTeX + its woff2 fonts, and the two stylesheets). Embedding them
// keeps mdserve fully offline — no CDN, no pip, no network — exactly like the
// reader this UI was ported from. Served at /vendor/<name>.
//
//go:embed assets
var vendorFS embed.FS

// vendorSub is the assets/ subtree, so /vendor/marked.min.js maps to
// assets/marked.min.js.
var vendorSub = func() fs.FS {
	sub, err := fs.Sub(vendorFS, "assets")
	if err != nil {
		panic(err)
	}
	return sub
}()

// vendorType is the Content-Type for an embedded asset by extension.
func vendorType(name string) string {
	switch filepath.Ext(name) {
	case ".js":
		return "text/javascript; charset=utf-8"
	case ".css":
		return "text/css; charset=utf-8"
	case ".woff2":
		return "font/woff2"
	default:
		return "application/octet-stream"
	}
}

// ServeVendor serves an embedded asset at /vendor/<name>. The name is cleaned so
// a "../" can't escape the embedded tree (defense in depth — embed.FS is already
// sandboxed).
func ServeVendor(w http.ResponseWriter, r *http.Request, name string) {
	clean := path.Clean("/" + name)[1:]
	b, err := fs.ReadFile(vendorSub, clean)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", vendorType(clean))
	w.Header().Set("Cache-Control", "public, max-age=86400")
	_, _ = w.Write(b)
}

// CopyVendor writes the embedded vendor tree under outDir/vendor (used by the
// static build so the generated site is also fully offline).
func CopyVendor(outDir string) error {
	root, err := os.OpenRoot(outDir)
	if err != nil {
		return err
	}
	defer root.Close()
	return CopyVendorRoot(root)
}

// CopyVendorRoot writes through an already anchored, private export root.
func CopyVendorRoot(root *os.Root) error {
	return fs.WalkDir(vendorSub, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		dst := path.Join("vendor", p)
		if err := root.MkdirAll(path.Dir(dst), 0755); err != nil {
			return err
		}
		data, err := fs.ReadFile(vendorSub, p)
		if err != nil {
			return err
		}
		f, err := root.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
		if err != nil {
			return err
		}
		_, err = f.Write(data)
		closeErr := f.Close()
		if err != nil {
			return err
		}
		return closeErr
	})
}
