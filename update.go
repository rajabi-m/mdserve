package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

// runUpdate replaces the running binary with the latest released build for this
// OS/arch from GitHub Releases. `mdserve update --check` only reports whether a
// newer release exists.
func runUpdate(args []string) {
	check := false
	for _, a := range args {
		if a == "--check" || a == "-check" {
			check = true
		}
	}
	cur := versionString()
	latest, err := latestTag()
	if err != nil {
		fatal(fmt.Errorf("checking latest release: %w", err))
	}
	fmt.Printf("mdserve: current %s\n", cur)
	fmt.Printf("mdserve: latest  %s\n", latest)
	if !isNewer(latest, cur) {
		fmt.Println("mdserve: already up to date")
		return
	}
	if check {
		fmt.Printf("mdserve: update available — run `mdserve update`\n")
		return
	}
	bin, err := downloadReleaseBinary(latest)
	if err != nil {
		fatal(fmt.Errorf("downloading %s: %w", latest, err))
	}
	if err := replaceExecutable(bin); err != nil {
		fatal(fmt.Errorf("installing update: %w", err))
	}
	fmt.Printf("mdserve: updated %s → %s\n", cur, latest)
}

const updateUA = "mdserve-selfupdate"

func httpGet(url string) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", updateUA)
	return (&http.Client{Timeout: 60 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if req.URL.Scheme != "https" {
			return fmt.Errorf("release redirect must use HTTPS")
		}
		if len(via) >= 10 {
			return fmt.Errorf("too many release redirects")
		}
		return nil
	}}).Do(req)
}

// latestTag returns the tag_name of the latest GitHub release (60s timeout).
func latestTag() (string, error) {
	return latestTagCtx(60 * time.Second)
}

// latestTagCtx is latestTag with a caller-chosen timeout. The launch-time update
// check (updatecheck.go) uses a short one so a slow network can't stall the page.
func latestTagCtx(timeout time.Duration) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://api.github.com/repos/"+repoSlug+"/releases/latest", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", updateUA)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("github api: %s", resp.Status)
	}
	var r struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&r); err != nil {
		return "", err
	}
	if !validReleaseTag(r.TagName) {
		return "", fmt.Errorf("invalid release tag")
	}
	return r.TagName, nil
}

// assetFor returns the release asset filename and the binary name inside it for
// this OS/arch — matching the names produced by .github/workflows/release.yml.
func assetFor(tag string) (asset, binName string) {
	binName = "mdserve"
	ext := "tar.gz"
	if runtime.GOOS == "windows" {
		binName = "mdserve.exe"
		ext = "zip"
	}
	return fmt.Sprintf("mdserve_%s_%s_%s.%s", tag, runtime.GOOS, runtime.GOARCH, ext), binName
}

// Release payload limits cover downloads and decompression separately.
const maxReleaseArchive = 64 << 20
const maxReleaseBinary = 128 << 20

var releaseTagPattern = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+(?:-[A-Za-z0-9]+(?:[.-][A-Za-z0-9]+)*)?$`)

func validReleaseTag(tag string) bool { return len(tag) <= 128 && releaseTagPattern.MatchString(tag) }

func readBounded(r io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("release payload exceeds size limit")
	}
	return data, nil
}

func fetchReleaseAsset(url string, limit int64) ([]byte, error) {
	resp, err := httpGet(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("release download: HTTP %d", resp.StatusCode)
	}
	if resp.ContentLength > limit {
		return nil, fmt.Errorf("release payload exceeds size limit")
	}
	return readBounded(resp.Body, limit)
}

// Per-asset checksum files use the exact sha256sum format from release.yml.
func verifyReleaseChecksum(data, checksum []byte, asset string) error {
	line := strings.TrimSuffix(string(checksum), "\n")
	if len(line) != 66+len(asset) || (line[64:66] != "  " && line[64:66] != " *") || line[66:] != asset {
		return fmt.Errorf("invalid release checksum record")
	}
	expected, err := hex.DecodeString(line[:64])
	if err != nil {
		return fmt.Errorf("invalid release checksum digest")
	}
	actual := sha256.Sum256(data)
	if !bytes.Equal(actual[:], expected) {
		return fmt.Errorf("release checksum mismatch")
	}
	return nil
}

// downloadReleaseBinary verifies the matching published checksum before extraction.
func downloadReleaseBinary(tag string) ([]byte, error) {
	if !validReleaseTag(tag) {
		return nil, fmt.Errorf("invalid release tag")
	}
	asset, binName := assetFor(tag)
	base := fmt.Sprintf("https://github.com/%s/releases/download/%s/%s", repoSlug, tag, asset)
	checksum, err := fetchReleaseAsset(base+".sha256", 4096)
	if err != nil {
		return nil, err
	}
	data, err := fetchReleaseAsset(base, maxReleaseArchive)
	if err != nil {
		return nil, err
	}
	if err := verifyReleaseChecksum(data, checksum, asset); err != nil {
		return nil, err
	}
	if strings.HasSuffix(asset, ".zip") {
		return extractFromZip(data, binName)
	}
	return extractFromTarGz(data, binName)
}

func extractFromTarGz(data []byte, name string) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer gz.Close()
	// Include a small allowance for tar metadata and padding, but bound all
	// decompression, including data after tar's end marker and concatenated gzip.
	expanded, err := readBounded(gz, maxReleaseBinary+(1<<20))
	if err != nil {
		return nil, err
	}
	tr := tar.NewReader(bytes.NewReader(expanded))
	h, err := tr.Next()
	if err != nil {
		return nil, err
	}
	if h.Name != name || h.Typeflag != tar.TypeReg || h.Size <= 0 || h.Size > maxReleaseBinary || h.Linkname != "" {
		return nil, fmt.Errorf("unexpected release binary entry")
	}
	bin, err := readBounded(tr, maxReleaseBinary)
	if err != nil {
		return nil, err
	}
	if _, err := tr.Next(); err != io.EOF {
		return nil, fmt.Errorf("release archive must contain only %s", name)
	}
	// The expected release archive has just the header, binary and zero padding.
	// Reject hidden trailing nonzero records after the end marker.
	offset := 512 + ((h.Size+511)/512)*512
	if offset > int64(len(expanded)) {
		return nil, fmt.Errorf("truncated release archive")
	}
	for _, b := range expanded[offset:] {
		if b != 0 {
			return nil, fmt.Errorf("unexpected trailing archive data")
		}
	}
	return bin, nil
}

func extractFromZip(data []byte, name string) ([]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}
	if len(zr.File) != 1 {
		return nil, fmt.Errorf("release archive must contain only %s", name)
	}
	f := zr.File[0]
	if f.Name != name || !f.Mode().IsRegular() || f.UncompressedSize64 == 0 || f.UncompressedSize64 > maxReleaseBinary {
		return nil, fmt.Errorf("unexpected release binary entry")
	}
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return readBounded(rc, maxReleaseBinary)
}

// replaceExecutable swaps the running binary for newBin. It writes a temp file in
// the same directory (so the rename is atomic on the same filesystem) and renames
// it over the current executable. On Windows the running file can't be
// overwritten, so it is moved to a unique backup first and restored on failure.
func replaceExecutable(newBin []byte) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return err
	}
	return replaceExecutableAt(exe, newBin, runtime.GOOS == "windows", os.Rename)
}

// rename is injectable to exercise both Windows moves and rollback failures
// without replacing the executable running the test suite.
func replaceExecutableAt(exe string, newBin []byte, windows bool, rename func(string, string) error) error {
	if len(newBin) == 0 || len(newBin) > maxReleaseBinary {
		return fmt.Errorf("invalid update binary size")
	}
	dir := filepath.Dir(exe)
	tmp, err := os.CreateTemp(dir, ".mdserve-update-*")
	if err != nil {
		return fmt.Errorf("stage update (directory must be writable): %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(newBin); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0755); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	backup := ""
	if windows {
		old, err := os.CreateTemp(dir, ".mdserve-old-*")
		if err != nil {
			return err
		}
		backup = old.Name()
		if err := old.Close(); err != nil {
			os.Remove(backup)
			return err
		}
		if err := os.Remove(backup); err != nil {
			return err
		}
		if err := rename(exe, backup); err != nil {
			return err
		}
	}
	if err := rename(tmpName, exe); err != nil {
		if backup != "" {
			if restoreErr := rename(backup, exe); restoreErr != nil {
				return fmt.Errorf("update failed: %w; old executable retained at %s (rollback: %v)", err, backup, restoreErr)
			}
		}
		return fmt.Errorf("replace executable: %w", err)
	}
	if backup != "" {
		_ = os.Remove(backup)
	} // a running Windows binary may remain locked
	return nil
}
