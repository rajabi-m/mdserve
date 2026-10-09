package server

import (
	"bytes"
	"crypto/rand"
	"encoding/xml"
	"fmt"
	"html"
	"io"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/mostafa-re/mdserve/internal/markdown"
	"github.com/mostafa-re/mdserve/internal/web"
)

var svgFragmentURL = regexp.MustCompile(`^url\(#[A-Za-z][A-Za-z0-9_-]*\)$`)

var assetAttribute = regexp.MustCompile(`(?i)\b(?:src|href)\s*=\s*(?:"([^"]*)"|'([^']*)')`)

// BuildStatic creates a complete export privately and publishes it only after
// all reads and writes succeed. Existing populated destinations are never merged.
func (s *Server) BuildStatic(outDir string) error {
	out, err := filepath.Abs(outDir)
	if err != nil {
		return err
	}
	// Resolve the OS temporary directory alias (/var on macOS), but reject
	// document-controlled destination symlinks, including intermediate components.
	temp, _ := filepath.Abs(os.TempDir())
	canonicalTemp, _ := filepath.EvalSymlinks(temp)
	if canonicalTemp != "" && (out == temp || strings.HasPrefix(out, temp+string(os.PathSeparator))) {
		out = canonicalTemp + strings.TrimPrefix(out, temp)
	}
	parent := filepath.Dir(out)
	resolved, err := filepath.EvalSymlinks(parent)
	if err != nil || resolved != parent {
		return fmt.Errorf("export parent must exist without symlinks")
	}
	srcInfo, err := s.root.Stat(".")
	if err != nil {
		return err
	}
	for p := parent; ; p = filepath.Dir(p) {
		info, err := os.Stat(p)
		if err != nil {
			return err
		}
		if os.SameFile(srcInfo, info) {
			return fmt.Errorf("export destination must be outside the document root")
		}
		if filepath.Dir(p) == p {
			break
		}
	}
	// Reject ancestors of the anchored input as well as same-directory output.
	for p := s.docDir; ; p = filepath.Dir(p) {
		info, err := os.Stat(p)
		dst, e := os.Lstat(out)
		if err == nil && e == nil && os.SameFile(info, dst) {
			return fmt.Errorf("export destination overlaps the document root")
		}
		if filepath.Dir(p) == p {
			break
		}
	}
	parentInfo, err := os.Stat(parent)
	if err != nil {
		return err
	}
	parentRoot, err := os.OpenRoot(parent)
	if err != nil {
		return err
	}
	defer parentRoot.Close()
	openedParent, err := parentRoot.Stat(".")
	if err != nil || !os.SameFile(parentInfo, openedParent) {
		return fmt.Errorf("export parent changed")
	}
	name := filepath.Base(out)
	if err := emptyDestination(parentRoot, name); err != nil {
		return err
	}
	stage := ".mdserve-export-" + rand.Text()
	if err := parentRoot.Mkdir(stage, 0700); err != nil {
		return err
	}
	defer parentRoot.RemoveAll(stage)
	root, err := parentRoot.OpenRoot(stage)
	if err != nil {
		return err
	}
	defer root.Close()
	if err := web.CopyVendorRoot(root); err != nil {
		return err
	}
	entries := s.flattenMtimes()
	rels := make([]string, 0, len(entries))
	for rel := range entries {
		rels = append(rels, rel)
	}
	sort.Strings(rels)
	assets := map[string]bool{}
	for _, rel := range rels {
		raw, err := s.readPath(rel)
		if err != nil {
			return fmt.Errorf("read document %q: unavailable", rel)
		}
		rendered := markdown.Render(raw)
		for _, m := range assetAttribute.FindAllSubmatch(rendered, -1) {
			value := string(m[1])
			if value == "" {
				value = string(m[2])
			}
			u, err := url.Parse(html.UnescapeString(value))
			if err != nil || u.IsAbs() || u.Host != "" || u.Path == "" || strings.HasPrefix(u.Path, "/") {
				continue
			}
			candidate := path.Join(path.Dir(rel), u.Path)
			if strings.EqualFold(path.Ext(candidate), ".md") {
				continue
			}
			if !s.permittedPath(candidate) || strings.EqualFold(strings.Split(candidate, "/")[0], "vendor") || !exportImage(candidate) {
				fmt.Fprintf(os.Stderr, "mdserve: skipped unsupported export asset in %q\n", rel)
				continue
			}
			assets[candidate] = true
		}
		title := extractTitle(raw)
		if title == "" {
			title = path.Base(rel)
		}
		prefix := strings.Repeat("../", strings.Count(rel, "/"))
		shell := strings.NewReplacer("__ROOT__", prefix, "__TITLE__", html.EscapeString(title), "__BODY__", jsonString(string(rendered))).Replace(web.BuildShell)
		if err := writeExport(root, rel+".html", []byte(web.ProtectStatic(shell))); err != nil {
			return err
		}
	}
	names := make([]string, 0, len(assets))
	for name := range assets {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		data, err := s.readPath(name)
		if err != nil {
			fmt.Fprintf(os.Stderr, "mdserve: skipped unavailable export asset %q\n", name)
			continue
		}
		if !safeExportImage(name, data) {
			return fmt.Errorf("export image %q has unsupported content", name)
		}
		if err := writeExport(root, name, data); err != nil {
			return err
		}
	}
	// Recheck immediately before publishing; rename replaces a terminal symlink
	// rather than following it. Staged writes never touch existing output entries.
	if err := emptyDestination(parentRoot, name); err != nil {
		return err
	}
	if err := root.Chmod(".", 0755); err != nil {
		return err
	}
	if err := parentRoot.Remove(name); err != nil && !os.IsNotExist(err) {
		return err
	}
	return parentRoot.Rename(stage, name)
}

func emptyDestination(root *os.Root, name string) error {
	info, err := root.Lstat(name)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("export destination must be an empty directory, not a symlink")
	}
	f, err := root.Open(name)
	if err != nil {
		return err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return fmt.Errorf("export destination changed")
	}
	entries, err := f.ReadDir(-1)
	if err != nil {
		return err
	}
	if len(entries) != 0 {
		return fmt.Errorf("export destination must be empty")
	}
	return nil
}

func writeExport(root *os.Root, rel string, data []byte) error {
	if err := root.MkdirAll(path.Dir(rel), 0755); err != nil {
		return err
	}
	f, err := root.OpenFile(rel, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	closeErr := f.Close()
	if err != nil {
		return err
	}
	return closeErr
}

func exportImage(name string) bool {
	switch strings.ToLower(path.Ext(name)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".avif", ".ico", ".svg":
		return true
	}
	return false
}

// SVG exports support basic self-contained drawing primitives. Reject active
// content, CSS, links, external resources, entities, and foreign namespaces.
func inertSVG(data []byte) bool {
	d := xml.NewDecoder(bytes.NewReader(data))
	depth := 0
	seen := false
	tags := strings.Fields("svg g defs title desc rect circle ellipse line polyline polygon path text tspan linearGradient radialGradient stop clipPath mask")
	attrs := strings.Fields("id x y x1 y1 x2 y2 cx cy r rx ry width height viewBox d points fill stroke stroke-width stroke-linecap stroke-linejoin opacity fill-opacity stroke-opacity transform font-size font-family text-anchor offset stop-color stop-opacity gradientUnits gradientTransform clip-path")
	allowed := func(list []string, s string) bool {
		for _, v := range list {
			if v == s {
				return true
			}
		}
		return false
	}
	for {
		tok, err := d.Token()
		if err == io.EOF {
			return seen && depth == 0
		}
		if err != nil {
			return false
		}
		switch v := tok.(type) {
		case xml.StartElement:
			if depth == 0 {
				if seen || v.Name.Local != "svg" {
					return false
				}
				seen = true
			}
			depth++
			if v.Name.Space != "" && v.Name.Space != "http://www.w3.org/2000/svg" || !allowed(tags, v.Name.Local) {
				return false
			}
			for _, a := range v.Attr {
				if a.Name.Local == "xmlns" && a.Name.Space == "" && a.Value == "http://www.w3.org/2000/svg" {
					continue
				}
				if a.Name.Space != "" || !allowed(attrs, a.Name.Local) {
					return false
				}
				if strings.ContainsAny(a.Value, "\\") || (strings.Contains(strings.ToLower(a.Value), "url") && !svgFragmentURL.MatchString(a.Value)) {
					return false
				}
			}
		case xml.EndElement:
			depth--
		case xml.Directive:
			return false
		case xml.ProcInst:
			if v.Target != "xml" || seen {
				return false
			}
		case xml.CharData:
			if depth == 0 && strings.TrimSpace(string(v)) != "" {
				return false
			}
		}
	}
}

// Check raster signatures so an HTML document renamed to an image is rejected.
func safeExportImage(name string, data []byte) bool {
	switch strings.ToLower(path.Ext(name)) {
	case ".svg":
		return inertSVG(data)
	case ".png":
		return bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n"))
	case ".jpg", ".jpeg":
		return bytes.HasPrefix(data, []byte{0xff, 0xd8, 0xff})
	case ".gif":
		return bytes.HasPrefix(data, []byte("GIF87a")) || bytes.HasPrefix(data, []byte("GIF89a"))
	case ".webp":
		return len(data) >= 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP"
	case ".avif":
		return len(data) >= 16 && string(data[4:8]) == "ftyp" && (string(data[8:12]) == "avif" || string(data[8:12]) == "avis")
	case ".ico":
		return len(data) >= 6 && bytes.Equal(data[:4], []byte{0, 0, 1, 0})
	}
	return false
}
