package server

import (
	"io"
	"io/fs"
	"os"
	"strings"
)

// permittedPath is shared by requests, discovery, polling, and export inputs.
// URL paths are slash-relative on every OS. Reject ambiguous Windows names even
// on Unix so a document has the same access policy on supported platforms.
func (s *Server) permittedPath(rel string) bool {
	if !fs.ValidPath(rel) || rel == "." || strings.ContainsAny(rel, "\\:\x00") {
		return false
	}
	parts := strings.Split(rel, "/")
	for i, part := range parts {
		if strings.HasPrefix(part, ".") || strings.EqualFold(part, "_site") || strings.HasSuffix(part, ".") || strings.HasSuffix(part, " ") || s.isExcluded(strings.Join(parts[:i+1], "/")) {
			return false
		}
	}
	return true
}

// openPath returns an already-opened handle; callers must never reopen by name.
// Root confines the actual open, including concurrent symlink replacements. At
// each component, compare the non-symlink inode with the opened handle to reject
// swaps to internal hidden/excluded targets as well as ordinary symlink entries.
func (s *Server) openPath(rel string) (*os.File, error) {
	if rel != "." && !s.permittedPath(rel) {
		return nil, fs.ErrPermission
	}
	root := s.root
	parts := strings.Split(rel, "/")
	for _, part := range parts[:len(parts)-1] {
		info, err := root.Lstat(part)
		if err != nil || !info.IsDir() {
			return nil, fs.ErrPermission
		}
		next, err := root.OpenRoot(part)
		if err != nil {
			return nil, err
		}
		defer next.Close()
		opened, err := next.Stat(".")
		if err != nil || !os.SameFile(info, opened) {
			return nil, fs.ErrPermission
		}
		root = next
	}
	name := parts[len(parts)-1]
	info, err := root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() && !info.IsDir() {
		return nil, fs.ErrPermission
	}
	f, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	opened, err := f.Stat()
	if err != nil || !os.SameFile(info, opened) {
		f.Close()
		return nil, fs.ErrPermission
	}
	return f, nil
}

func (s *Server) readPath(rel string) ([]byte, error) {
	f, err := s.openPath(rel)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(f)
}

// walkFiles discovers only permitted regular files using rooted handles for
// directory reads and metadata. Entries removed/replaced during a walk are skipped.
func (s *Server) walkFiles(rel string, visit func(string, os.FileInfo) error) error {
	f, err := s.openPath(rel)
	if err != nil {
		return err
	}
	entries, err := f.ReadDir(-1)
	f.Close()
	if err != nil {
		return err
	}
	for _, entry := range entries {
		name := entry.Name()
		if rel != "." {
			name = rel + "/" + name
		}
		if !s.permittedPath(name) {
			continue
		}
		child, err := s.openPath(name)
		if err != nil {
			continue
		}
		info, err := child.Stat()
		child.Close()
		if err != nil {
			continue
		}
		if info.IsDir() {
			if err := s.walkFiles(name, visit); err != nil {
				return err
			}
		} else if err := visit(name, info); err != nil {
			return err
		}
	}
	return nil
}

// Close releases the anchored document root after requests/builds have stopped.
func (s *Server) Close() error { return s.root.Close() }
