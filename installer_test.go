package main

import (
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func installerFixture(t *testing.T, archive []byte, checksum string, osName, shell, bindirSuffix string, noProfile bool, overrides ...string) (error, string, string) {
	t.Helper()
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	bindir := filepath.Join(dir, bindirSuffix)
	tools := filepath.Join(dir, "tools")
	for _, p := range []string{home, bindir, tools} {
		if err := os.MkdirAll(p, 0755); err != nil {
			t.Fatal(err)
		}
	}
	os.WriteFile(filepath.Join(bindir, "mdserve"), []byte("OLD"), 0755)
	os.WriteFile(filepath.Join(dir, "archive"), archive, 0644)
	os.WriteFile(filepath.Join(dir, "checksum"), []byte(checksum), 0644)
	curl := `#!/bin/sh
out=
url=
while [ "$#" -gt 0 ]; do
 case "$1" in -o) shift;out=$1;; https://*) url=$1;; esac
 shift
done
case "$url" in https://api.github.com/*) printf '{"tag_name":"v1.2.3"}\n' > "$out";; *.sha256) [ -s "$FIXTURE_DIR/checksum" ] || exit 22;cp "$FIXTURE_DIR/checksum" "$out";; *) cp "$FIXTURE_DIR/archive" "$out";; esac
`
	uname := `#!/bin/sh
case "$1" in -s) printf '%s\n' "$MOCK_OS";; -m) echo arm64;; esac
`
	os.WriteFile(filepath.Join(tools, "curl"), []byte(curl), 0755)
	os.WriteFile(filepath.Join(tools, "uname"), []byte(uname), 0755)
	cmd := exec.Command("sh", "scripts/install.sh")
	cmd.Env = append([]string{}, "HOME="+home, "PATH="+tools+":"+os.Getenv("PATH"), "MDSERVE_VERSION=v1.2.3", "MDSERVE_BINDIR="+bindir, "SHELL="+shell, "FIXTURE_DIR="+dir, "MOCK_OS="+osName, "MDSERVE_NO_MODIFY_PATH=")
	if noProfile {
		cmd.Env = append(cmd.Env, "MDSERVE_NO_MODIFY_PATH=1")
	}
	cmd.Env = append(cmd.Env, overrides...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Logf("installer rejected fixture: %s", output)
	}
	return err, home, bindir
}

func TestInstallerVerification(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX installer; Windows uses updater")
	}
	valid := tarFixture(t, []string{"mdserve"}, '0')
	for _, osName := range []string{"Darwin", "Linux"} {
		for _, scenario := range []string{"valid", "missing", "mismatch", "malformed", "traversal", "symlink", "hardlink", "extra", "duplicate", "wrong", "wrong-checksum-name", "corrupt"} {
			t.Run(osName+"/"+scenario, func(t *testing.T) {
				archive := valid
				switch scenario {
				case "traversal":
					archive = tarFixture(t, []string{"../mdserve"}, '0')
				case "symlink":
					archive = tarFixture(t, []string{"mdserve"}, '2')
				case "hardlink":
					archive = tarFixture(t, []string{"mdserve"}, '1')
				case "duplicate":
					archive = tarFixture(t, []string{"mdserve", "mdserve"}, '0')
				case "wrong":
					archive = tarFixture(t, []string{"other"}, '0')
				case "extra":
					archive = tarFixture(t, []string{"mdserve", "extra"}, '0')
				case "corrupt":
					archive = []byte("not an archive")
				}
				asset := "mdserve_v1.2.3_" + strings.ToLower(osName) + "_arm64.tar.gz"
				checksum := fmt.Sprintf("%x  %s\n", sha256.Sum256(archive), asset)
				switch scenario {
				case "missing":
					checksum = ""
				case "mismatch":
					checksum = strings.Repeat("0", 64) + "  " + asset + "\n"
				case "malformed":
					checksum += checksum
				case "wrong-checksum-name":
					checksum = strings.Replace(checksum, asset, "wrong.tar.gz", 1)
				}
				err, home, bindir := installerFixture(t, archive, checksum, osName, "/bin/sh", "bin", true)
				b, _ := os.ReadFile(filepath.Join(bindir, "mdserve"))
				if scenario == "valid" {
					if err != nil || string(b) != "DUMMY" {
						t.Fatalf("valid installation: %v %q", err, b)
					}
				} else if err == nil || string(b) != "OLD" {
					t.Fatalf("unsafe installation: %v %q", err, b)
				}
				if _, err := os.Stat(filepath.Join(home, ".profile")); !os.IsNotExist(err) {
					t.Fatal("no-profile option modified profile")
				}
			})
		}
	}
}

func TestInstallerProfileQuoting(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX installer")
	}
	archive := tarFixture(t, []string{"mdserve"}, '0')
	checksum := fmt.Sprintf("%x  mdserve_v1.2.3_linux_arm64.tar.gz\n", sha256.Sum256(archive))
	suffix := "space ' quote \" $() `literal` bin"
	for _, shell := range []string{"/bin/sh", "/bin/bash", "/bin/zsh"} {
		t.Run(shell, func(t *testing.T) {
			if _, err := os.Stat(shell); err != nil {
				t.Skip("optional shell unavailable")
			}
			err, home, bindir := installerFixture(t, archive, checksum, "Linux", shell, suffix, false)
			if err != nil {
				t.Fatal(err)
			}
			rc := ".profile"
			if strings.HasSuffix(shell, "bash") {
				rc = ".bashrc"
			}
			if strings.HasSuffix(shell, "zsh") {
				rc = ".zshrc"
			}
			cmd := exec.Command(shell, "-c", `. "$1"; printf '%s' "$PATH"`, "test", filepath.Join(home, rc))
			cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + home}
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatal(err, string(output))
			}
			if string(output) != bindir+":/usr/bin:/bin" {
				t.Fatalf("path corrupted: %q", output)
			}
		})
	}
}

func TestInstallerInvalidVersions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX installer")
	}
	archive := tarFixture(t, []string{"mdserve"}, '0')
	for _, ver := range []string{"../v1.2.3", "v1.2.3?x=1", "v1.2.3\nv1.2.4", "v1.2.3\r", "not-a-tag"} {
		t.Run(ver, func(t *testing.T) {
			err, _, bindir := installerFixture(t, archive, "unused", "Linux", "/bin/sh", "bin", true, "MDSERVE_VERSION="+ver)
			b, _ := os.ReadFile(filepath.Join(bindir, "mdserve"))
			if err == nil || string(b) != "OLD" {
				t.Fatal("invalid version changed executable")
			}
		})
	}
}

func TestInstallerFishQuoting(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX installer")
	}
	archive := tarFixture(t, []string{"mdserve"}, '0')
	checksum := fmt.Sprintf("%x  mdserve_v1.2.3_linux_arm64.tar.gz\n", sha256.Sum256(archive))
	err, home, bindir := installerFixture(t, archive, checksum, "Linux", "fish", `space ' \ $() bin`, false)
	if err != nil {
		t.Fatal(err)
	}
	rc := filepath.Join(home, ".config/fish/config.fish")
	config, err := os.ReadFile(rc)
	if err != nil {
		t.Fatal(err)
	}
	quoted := "'" + strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(bindir) + "'"
	if !strings.Contains(string(config), "fish_add_path "+quoted+"\n") {
		t.Fatalf("unsafe fish config: %q", config)
	}
	if fish, err := exec.LookPath("fish"); err == nil {
		cmd := exec.Command(fish, "--no-config", "-c", `source $argv[1]; printf '%s' $PATH[1]`, rc)
		cmd.Env = []string{"HOME=" + home, "PATH=/usr/bin:/bin"}
		output, err := cmd.CombinedOutput()
		if err != nil || string(output) != bindir {
			t.Fatalf("fish profile: %q %v", output, err)
		}
	} else {
		t.Log("fish unavailable: checked generated escaping, no fish runtime claim")
	}
}

func TestInstallerLatestRelease(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX installer")
	}
	archive := tarFixture(t, []string{"mdserve"}, '0')
	checksum := fmt.Sprintf("%x  mdserve_v1.2.3_darwin_arm64.tar.gz\n", sha256.Sum256(archive))
	err, _, bindir := installerFixture(t, archive, checksum, "Darwin", "sh", "bin", true, "MDSERVE_VERSION=")
	b, _ := os.ReadFile(filepath.Join(bindir, "mdserve"))
	if err != nil || string(b) != "DUMMY" {
		t.Fatal("latest release failed", err)
	}
}
