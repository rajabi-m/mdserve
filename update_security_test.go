package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func tarFixture(t *testing.T, names []string, kind byte) []byte {
	t.Helper()
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)
	tw := tar.NewWriter(gz)
	for _, name := range names {
		h := &tar.Header{Name: name, Mode: 0755, Typeflag: kind}
		if kind == tar.TypeReg {
			h.Size = 5
		} else {
			h.Linkname = "outside"
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if kind == tar.TypeReg {
			tw.Write([]byte("DUMMY"))
		}
	}
	tw.Close()
	gz.Close()
	return b.Bytes()
}
func zipFixture(t *testing.T, name string, mode os.FileMode) []byte {
	t.Helper()
	var b bytes.Buffer
	zw := zip.NewWriter(&b)
	h := &zip.FileHeader{Name: name}
	h.SetMode(mode)
	w, err := zw.CreateHeader(h)
	if err != nil {
		t.Fatal(err)
	}
	w.Write([]byte("DUMMY"))
	zw.Close()
	return b.Bytes()
}

func TestReleaseArchivePolicy(t *testing.T) {
	for _, names := range [][]string{{"../mdserve"}, {"nested/mdserve"}, {"mdserve", "extra"}, {"mdserve", "mdserve"}} {
		t.Run(strings.Join(names, ","), func(t *testing.T) {
			if _, err := extractFromTarGz(tarFixture(t, names, tar.TypeReg), "mdserve"); err == nil {
				t.Fatal("unsafe archive accepted")
			}
		})
	}
	for _, kind := range []byte{tar.TypeSymlink, tar.TypeLink} {
		if _, err := extractFromTarGz(tarFixture(t, []string{"mdserve"}, kind), "mdserve"); err == nil {
			t.Error("link accepted")
		}
	}
	for _, name := range []string{"../mdserve.exe", "nested/mdserve.exe"} {
		if _, err := extractFromZip(zipFixture(t, name, 0755), "mdserve.exe"); err == nil {
			t.Error("zip traversal accepted")
		}
	}
	if _, err := extractFromZip(zipFixture(t, "mdserve.exe", os.ModeSymlink|0755), "mdserve.exe"); err == nil {
		t.Error("zip symlink accepted")
	}
	for _, tc := range []struct {
		data    []byte
		name    string
		extract func([]byte, string) ([]byte, error)
	}{{tarFixture(t, []string{"mdserve"}, tar.TypeReg), "mdserve", extractFromTarGz}, {zipFixture(t, "mdserve.exe", 0755), "mdserve.exe", extractFromZip}} {
		b, err := tc.extract(tc.data, tc.name)
		if err != nil || string(b) != "DUMMY" {
			t.Fatalf("valid archive: %q %v", b, err)
		}
	}
}

type fixtureTransport func(*http.Request) (*http.Response, error)

func (f fixtureTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestDownloadChecksumGate(t *testing.T) {
	archive := tarFixture(t, []string{"mdserve"}, tar.TypeReg)
	asset, _ := assetFor("v1.2.3")
	if strings.HasSuffix(asset, ".zip") {
		archive = zipFixture(t, "mdserve.exe", 0755)
	}
	good := fmt.Sprintf("%x  %s\n", sha256.Sum256(archive), asset)
	for _, checksum := range []string{good, "", strings.Repeat("0", 64) + "  " + asset + "\n", good + good, strings.Replace(good, asset, "wrong", 1)} {
		t.Run(checksum, func(t *testing.T) {
			old := http.DefaultTransport
			defer func() { http.DefaultTransport = old }()
			http.DefaultTransport = fixtureTransport(func(r *http.Request) (*http.Response, error) {
				body := archive
				if strings.HasSuffix(r.URL.Path, ".sha256") {
					body = []byte(checksum)
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(body)), Header: make(http.Header)}, nil
			})
			b, err := downloadReleaseBinary("v1.2.3")
			if checksum == good {
				if err != nil || string(b) != "DUMMY" {
					t.Fatal(err)
				}
			} else if err == nil {
				t.Fatal("unverified archive accepted")
			}
		})
	}
}

func TestInvalidReleaseTags(t *testing.T) {
	for _, tag := range []string{"../v1.2.3", "v1.2.3/other", "v1.2.3?x=1", "bad", "v1.2.3\n"} {
		if _, err := downloadReleaseBinary(tag); err == nil {
			t.Errorf("tag %q accepted", tag)
		}
	}
}

func TestArchiveDoesNotTouchExecutable(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "mdserve")
	os.WriteFile(exe, []byte("OLD"), 0755)
	_, err := extractFromTarGz(tarFixture(t, []string{"../mdserve"}, tar.TypeReg), "mdserve")
	if err == nil {
		t.Fatal("bad archive accepted")
	}
	b, _ := os.ReadFile(exe)
	if string(b) != "OLD" {
		t.Fatal("executable changed")
	}
}

func TestExecutableReplacement(t *testing.T) {
	for _, windows := range []bool{false, true} {
		for _, failMove := range []int{0, 1, 2} {
			t.Run(fmt.Sprintf("windows=%v/fail=%d", windows, failMove), func(t *testing.T) {
				dir := t.TempDir()
				exe := filepath.Join(dir, "mdserve")
				if err := os.WriteFile(exe, []byte("OLD"), 0755); err != nil {
					t.Fatal(err)
				}
				moves := 0
				rename := func(a, b string) error {
					moves++
					if moves == failMove {
						return fmt.Errorf("injected rename failure")
					}
					return os.Rename(a, b)
				}
				err := replaceExecutableAt(exe, []byte("NEW"), windows, rename)
				b, readErr := os.ReadFile(exe)
				if readErr != nil {
					t.Fatal(readErr)
				}
				shouldFail := failMove == 1 || (windows && failMove == 2)
				if shouldFail {
					if err == nil || string(b) != "OLD" {
						t.Fatalf("failed replacement lost original: %v %q", err, b)
					}
				} else if err != nil || string(b) != "NEW" {
					t.Fatalf("replacement failed: %v %q", err, b)
				}
				entries, _ := os.ReadDir(dir)
				if len(entries) != 1 {
					t.Fatal("temporary update files leaked")
				}
			})
		}
	}
}

func TestReplacementRetainsBackupOnRollbackFailure(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "mdserve")
	os.WriteFile(exe, []byte("OLD"), 0755)
	calls := 0
	err := replaceExecutableAt(exe, []byte("NEW"), true, func(a, b string) error {
		calls++
		if calls > 1 {
			return fmt.Errorf("injected failure")
		}
		return os.Rename(a, b)
	})
	if err == nil || !strings.Contains(err.Error(), "old executable retained") {
		t.Fatal("missing recovery diagnostic", err)
	}
	backups, _ := filepath.Glob(filepath.Join(dir, ".mdserve-old-*"))
	if len(backups) != 1 {
		t.Fatal("original backup lost")
	}
	b, _ := os.ReadFile(backups[0])
	if string(b) != "OLD" {
		t.Fatal("backup changed")
	}
}

func TestBoundedReleaseDownloads(t *testing.T) {
	old := http.DefaultTransport
	defer func() { http.DefaultTransport = old }()
	for _, tc := range []struct {
		status int
		body   string
		length int64
	}{{404, "", 0}, {200, strings.Repeat("x", 33), -1}, {200, "small", 33}} {
		http.DefaultTransport = fixtureTransport(func(r *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: tc.status, ContentLength: tc.length, Body: io.NopCloser(strings.NewReader(tc.body))}, nil
		})
		if _, err := fetchReleaseAsset("https://fixture.invalid/archive", 32); err == nil {
			t.Fatal("failed/oversized download accepted")
		}
	}
}

func TestArchiveSizeAndIntegrity(t *testing.T) {
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: "mdserve", Mode: 0755, Size: maxReleaseBinary + 1, Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	tw.Close()
	gz.Close()
	if _, err := extractFromTarGz(b.Bytes(), "mdserve"); err == nil {
		t.Fatal("oversized tar binary accepted")
	}
	data := tarFixture(t, []string{"mdserve"}, tar.TypeReg)
	data[len(data)-8] ^= 1
	if _, err := extractFromTarGz(data, "mdserve"); err == nil {
		t.Fatal("gzip integrity failure accepted")
	}
	var z bytes.Buffer
	zw := zip.NewWriter(&z)
	h := &zip.FileHeader{Name: "mdserve.exe", UncompressedSize64: maxReleaseBinary + 1}
	h.SetMode(0755)
	if _, err := zw.CreateRaw(h); err != nil {
		t.Fatal(err)
	}
	zw.Close()
	if _, err := extractFromZip(z.Bytes(), "mdserve.exe"); err == nil {
		t.Fatal("oversized zip binary accepted")
	}
	var extra bytes.Buffer
	writer := zip.NewWriter(&extra)
	for _, name := range []string{"mdserve.exe", "extra"} {
		w, _ := writer.Create(name)
		w.Write([]byte("DUMMY"))
	}
	writer.Close()
	if _, err := extractFromZip(extra.Bytes(), "mdserve.exe"); err == nil {
		t.Fatal("extra zip entry accepted")
	}
}

func TestSystemTarReleaseArchive(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("system tar is optional on Windows; ZIP covered separately")
	}
	tarTool, err := exec.LookPath("tar")
	if err != nil {
		t.Fatal("tar required for installer tests")
	}
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "mdserve"), []byte("DUMMY"), 0755)
	archive := filepath.Join(t.TempDir(), "release.tar.gz")
	cmd := exec.Command(tarTool, "-czf", archive, "-C", dir, "mdserve")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatal(err, string(out))
	}
	data, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	bin, err := extractFromTarGz(data, "mdserve")
	if err != nil || string(bin) != "DUMMY" {
		t.Fatal("system tar release rejected", err)
	}
}
