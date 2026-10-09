package server

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSelectiveExport(t *testing.T) {
	srv, dir := newTestServer(t)
	mustWrite(t, filepath.Join(dir, "README.md"), "# Home\n![image](images/pic.svg)\n")
	mustWrite(t, filepath.Join(dir, "images/pic.svg"), `<svg xmlns="http://www.w3.org/2000/svg"><rect width="1" height="1"/></svg>`)
	for _, name := range []string{"credentials.txt", "unused.svg", "active.html", ".env"} {
		mustWrite(t, filepath.Join(dir, name), "DUMMY_PRIVATE")
	}
	out := t.TempDir()
	if err := srv.BuildStatic(out); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"credentials.txt", "unused.svg", "active.html", ".env"} {
		if _, err := os.Stat(filepath.Join(out, name)); !os.IsNotExist(err) {
			t.Errorf("copied unrelated %s", name)
		}
	}
	if _, err := os.Stat(filepath.Join(out, "images/pic.svg")); err != nil {
		t.Fatal(err)
	}
}

func TestExportDestinationSafety(t *testing.T) {
	for _, kind := range []string{"same", "nested", "ancestor", "symlink", "populated"} {
		t.Run(kind, func(t *testing.T) {
			srv, dir := newTestServer(t)
			out := t.TempDir()
			switch kind {
			case "same":
				out = dir
			case "nested":
				out = filepath.Join(dir, "export")
			case "ancestor":
				out = filepath.Dir(dir)
			case "symlink":
				link := filepath.Join(t.TempDir(), "link")
				if err := os.Symlink(out, link); err != nil {
					t.Fatal(err)
				}
				out = link
			case "populated":
				mustWrite(t, filepath.Join(out, "keep"), "DUMMY_KEEP")
			}
			before, err := os.ReadFile(filepath.Join(dir, "README.md"))
			if err != nil {
				t.Fatal(err)
			}
			if err := srv.BuildStatic(out); err == nil {
				t.Fatal("unsafe destination accepted")
			}
			after, _ := os.ReadFile(filepath.Join(dir, "README.md"))
			if string(after) != string(before) {
				t.Fatal("source changed")
			}
		})
	}
}

func TestExportBlockedReferences(t *testing.T) {
	for _, ref := range []string{".env", "../outside.png", "missing.png", "active.html", "bad.svg", "vendor/pic.svg"} {
		t.Run(ref, func(t *testing.T) {
			srv, dir := newTestServer(t)
			mustWrite(t, filepath.Join(dir, "README.md"), "![image]("+ref+")")
			mustWrite(t, filepath.Join(dir, "bad.svg"), `<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`)
			out := t.TempDir()
			err := srv.BuildStatic(out)
			if ref == "bad.svg" {
				if err == nil {
					t.Fatal("active SVG accepted")
				}
			} else if err != nil {
				t.Fatal(err)
			} else if _, err := os.Stat(filepath.Join(out, ref)); err == nil {
				t.Fatal("blocked reference exported")
			}
		})
	}
}

func TestExportRejectsIntermediateDestinationSymlink(t *testing.T) {
	srv, _ := newTestServer(t)
	outside := t.TempDir()
	link := filepath.Join(t.TempDir(), "parent")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	if err := srv.BuildStatic(filepath.Join(link, "site")); err == nil {
		t.Fatal("followed parent symlink")
	}
	entries, err := os.ReadDir(outside)
	if err != nil || len(entries) != 0 {
		t.Fatal("wrote outside destination")
	}
}

func TestExportSVGPolicy(t *testing.T) {
	for _, markup := range []string{
		`<svg xmlns="http://www.w3.org/2000/svg" onload="alert(1)"/>`,
		`<svg xmlns="http://www.w3.org/2000/svg"><foreignObject/></svg>`,
		`<svg xmlns="http://www.w3.org/2000/svg"><image href="https://example.invalid/"/></svg>`,
		`<svg xmlns="http://www.w3.org/2000/svg"><rect fill="url(https://example.invalid/)"/></svg>`,
		`<svg xmlns="http://www.w3.org/2000/svg"><rect fill="u\72l(https://example.invalid/)"/></svg>`,
		`<!DOCTYPE svg><svg/>`, `<?xml-stylesheet href="style.css"?><svg/>`,
	} {
		t.Run(markup, func(t *testing.T) {
			srv, dir := newTestServer(t)
			mustWrite(t, filepath.Join(dir, "README.md"), "![image](image.svg)")
			mustWrite(t, filepath.Join(dir, "image.svg"), markup)
			out := t.TempDir()
			if err := srv.BuildStatic(out); err == nil {
				t.Fatal("active SVG exported")
			}
			entries, _ := os.ReadDir(out)
			if len(entries) != 0 {
				t.Fatal("failed build published files")
			}
		})
	}
}

func TestExportRejectsDisguisedHTML(t *testing.T) {
	srv, dir := newTestServer(t)
	mustWrite(t, filepath.Join(dir, "README.md"), "![image](image.png)")
	mustWrite(t, filepath.Join(dir, "image.png"), "<html><script>alert(1)</script></html>")
	if err := srv.BuildStatic(t.TempDir()); err == nil {
		t.Fatal("HTML disguised as image exported")
	}
}
