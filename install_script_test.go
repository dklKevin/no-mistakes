package main

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestInstallScriptInstallsUserOwnedBinaryAndPathSymlink(t *testing.T) {
	skipInstallScriptTestsOnWindows(t)

	home := t.TempDir()
	archivePath := filepath.Join(t.TempDir(), "no-mistakes-v1.2.3-darwin-arm64.tar.gz")
	binaryScript := "#!/bin/sh\nexit 0\n"
	makeInstallArchive(t, archivePath, binaryScript)
	fakeBin := makeFakeInstallCommands(t)
	localBin := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(localBin, 0o755); err != nil {
		t.Fatal(err)
	}

	runInstallScript(t, home, fakeBin, map[string]string{
		"FAKE_RELEASE_ARCHIVE": archivePath,
	})

	realBin := filepath.Join(home, ".no-mistakes", "bin", "no-mistakes")
	assertFileContent(t, realBin, binaryScript)
	assertSymlinkTarget(t, filepath.Join(localBin, "no-mistakes"), realBin)
}

func TestInstallScriptReplacesExistingPathEntryWithSymlink(t *testing.T) {
	skipInstallScriptTestsOnWindows(t)

	home := t.TempDir()
	archivePath := filepath.Join(t.TempDir(), "no-mistakes-v1.2.3-darwin-arm64.tar.gz")
	binaryScript := "#!/bin/sh\nexit 0\n"
	makeInstallArchive(t, archivePath, binaryScript)
	fakeBin := makeFakeInstallCommands(t)
	linkDir := filepath.Join(t.TempDir(), "link-bin")
	if err := os.MkdirAll(linkDir, 0o755); err != nil {
		t.Fatal(err)
	}
	oldPath := filepath.Join(linkDir, "no-mistakes")
	if err := os.WriteFile(oldPath, []byte("old-binary"), 0o755); err != nil {
		t.Fatal(err)
	}

	runInstallScript(t, home, fakeBin, map[string]string{
		"FAKE_RELEASE_ARCHIVE": archivePath,
		"NO_MISTAKES_LINK_DIR": linkDir,
	})

	realBin := filepath.Join(home, ".no-mistakes", "bin", "no-mistakes")
	assertFileContent(t, realBin, binaryScript)
	assertSymlinkTarget(t, oldPath, realBin)
}

func TestInstallScriptRestartsDaemonAfterInstall(t *testing.T) {
	skipInstallScriptTestsOnWindows(t)

	home := t.TempDir()
	archivePath := filepath.Join(t.TempDir(), "no-mistakes-v1.2.3-darwin-arm64.tar.gz")
	callLog := filepath.Join(t.TempDir(), "calls.log")
	makeInstallArchive(t, archivePath, "#!/bin/sh\nprintf '%s\n' \"$*\" >> \"$NO_MISTAKES_CALL_LOG\"\n")
	fakeBin := makeFakeInstallCommands(t)
	localBin := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(localBin, 0o755); err != nil {
		t.Fatal(err)
	}

	runInstallScript(t, home, fakeBin, map[string]string{
		"FAKE_RELEASE_ARCHIVE": archivePath,
		"NO_MISTAKES_CALL_LOG": callLog,
	})

	data, err := os.ReadFile(callLog)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "daemon restart") {
		t.Fatalf("install.sh should restart the daemon after install, got calls %q", string(data))
	}
}

func TestInstallScriptFailsWhenDaemonRestartFails(t *testing.T) {
	skipInstallScriptTestsOnWindows(t)

	home := t.TempDir()
	archivePath := filepath.Join(t.TempDir(), "no-mistakes-v1.2.3-darwin-arm64.tar.gz")
	callLog := filepath.Join(t.TempDir(), "calls.log")
	makeInstallArchive(t, archivePath, "#!/bin/sh\nprintf '%s\n' \"$*\" >> \"$NO_MISTAKES_CALL_LOG\"\nif [ \"$1\" = \"daemon\" ] && [ \"$2\" = \"restart\" ]; then\n  exit 23\nfi\n")
	fakeBin := makeFakeInstallCommands(t)
	localBin := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(localBin, 0o755); err != nil {
		t.Fatal(err)
	}

	output, err := runInstallScriptCommand(t, home, fakeBin, map[string]string{
		"FAKE_RELEASE_ARCHIVE": archivePath,
		"NO_MISTAKES_CALL_LOG": callLog,
	})
	if err == nil {
		t.Fatalf("install.sh should fail when daemon restart fails\n%s", output)
	}

	data, err := os.ReadFile(callLog)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "daemon restart") {
		t.Fatalf("install.sh should still attempt daemon restart, got calls %q", string(data))
	}
}

func TestPowerShellInstallScriptChecksDaemonRestartFailure(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("docs", "install.ps1"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, "$restart = Start-Process -FilePath \"$installDir\\no-mistakes.exe\" -ArgumentList @(") {
		t.Fatal("install.ps1 should run daemon restart in a way that exposes the exit code")
	}
	if !strings.Contains(text, "-Wait -PassThru") {
		t.Fatal("install.ps1 should wait for daemon restart to finish and inspect the process result")
	}
	if !strings.Contains(text, "if ($restart.ExitCode -ne 0)") {
		t.Fatal("install.ps1 should fail the install when daemon restart returns a non-zero exit code")
	}
	if !strings.Contains(text, "throw \"Failed to restart daemon (exit code $($restart.ExitCode))\"") {
		t.Fatal("install.ps1 should surface the daemon restart exit code")
	}
}

func TestInstallScriptsPinReleaseAndRequireChecksums(t *testing.T) {
	pinned := "v1.55.0"
	scripts := []struct {
		path          string
		latestScrapes []string
		pinnedURL     string
		checksumsURL  string
		hashAPI       string
		mismatchFail  string
		missingFail   string
	}{
		{
			path:          filepath.Join("docs", "install.sh"),
			latestScrapes: []string{"/releases/latest", "api.github.com"},
			pinnedURL:     "https://github.com/${REPO}/releases/download/${VERSION}/",
			checksumsURL:  "checksums.txt",
			hashAPI:       "sha256",
			mismatchFail:  "checksum mismatch",
			missingFail:   "checksums.txt",
		},
		{
			path:          filepath.Join("docs", "install.ps1"),
			latestScrapes: []string{"/releases/latest", "api.github.com"},
			pinnedURL:     "https://github.com/$repo/releases/download/$version/",
			checksumsURL:  "checksums.txt",
			hashAPI:       "Get-FileHash",
			mismatchFail:  "checksum mismatch",
			missingFail:   "checksums.txt",
		},
	}
	for _, tc := range scripts {
		data, err := os.ReadFile(tc.path)
		if err != nil {
			t.Fatal(err)
		}
		text := string(data)
		for _, scrape := range tc.latestScrapes {
			if strings.Contains(text, scrape) {
				t.Errorf("%s must not scrape an unpinned latest release via %q", tc.path, scrape)
			}
		}
		if !strings.Contains(text, pinned) {
			t.Errorf("%s must pin the current release tag %s", tc.path, pinned)
		}
		if !strings.Contains(text, tc.pinnedURL) {
			t.Errorf("%s must download from the pinned release URL %q", tc.path, tc.pinnedURL)
		}
		if !strings.Contains(text, tc.checksumsURL) {
			t.Errorf("%s must download checksums.txt from the same pinned release", tc.path)
		}
		if !strings.Contains(strings.ToLower(text), strings.ToLower(tc.hashAPI)) {
			t.Errorf("%s must verify the archive with %s", tc.path, tc.hashAPI)
		}
		if !strings.Contains(text, tc.mismatchFail) {
			t.Errorf("%s must fail closed on a checksum mismatch", tc.path)
		}
		if !strings.Contains(text, tc.missingFail) {
			t.Errorf("%s must mention checksums.txt when verification cannot proceed", tc.path)
		}
	}
}

func TestInstallScriptFailsWhenChecksumsMissing(t *testing.T) {
	skipInstallScriptTestsOnWindows(t)

	home := t.TempDir()
	archivePath := filepath.Join(t.TempDir(), "no-mistakes-v1.2.3-darwin-arm64.tar.gz")
	makeInstallArchive(t, archivePath, "#!/bin/sh\nexit 0\n")
	fakeBin := makeFakeInstallCommands(t)

	output, err := runInstallScriptCommand(t, home, fakeBin, map[string]string{
		"FAKE_RELEASE_ARCHIVE":   archivePath,
		"FAKE_CHECKSUMS_MISSING": "1",
	})
	if err == nil {
		t.Fatalf("install.sh should fail when checksums.txt cannot be downloaded\n%s", output)
	}
	if !strings.Contains(string(output), "checksums") {
		t.Fatalf("install.sh should name checksums.txt in the failure, got:\n%s", output)
	}
	assertNotInstalled(t, home)
}

func TestInstallScriptFailsOnChecksumMismatch(t *testing.T) {
	skipInstallScriptTestsOnWindows(t)

	home := t.TempDir()
	archivePath := filepath.Join(t.TempDir(), "no-mistakes-v1.2.3-darwin-arm64.tar.gz")
	makeInstallArchive(t, archivePath, "#!/bin/sh\nexit 0\n")
	checksumsPath := filepath.Join(t.TempDir(), "checksums.txt")
	if err := os.WriteFile(checksumsPath, []byte("deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef  no-mistakes-v1.2.3-darwin-arm64.tar.gz\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fakeBin := makeFakeInstallCommands(t)

	output, err := runInstallScriptCommand(t, home, fakeBin, map[string]string{
		"FAKE_RELEASE_ARCHIVE": archivePath,
		"FAKE_CHECKSUMS":       checksumsPath,
	})
	if err == nil {
		t.Fatalf("install.sh should fail when checksums.txt does not match\n%s", output)
	}
	if !strings.Contains(string(output), "checksum mismatch") {
		t.Fatalf("install.sh should report a checksum mismatch, got:\n%s", output)
	}
	assertNotInstalled(t, home)
}

func TestInstallScriptFailsWhenChecksumEntryMissing(t *testing.T) {
	skipInstallScriptTestsOnWindows(t)

	home := t.TempDir()
	archivePath := filepath.Join(t.TempDir(), "no-mistakes-v1.2.3-darwin-arm64.tar.gz")
	makeInstallArchive(t, archivePath, "#!/bin/sh\nexit 0\n")
	checksumsPath := filepath.Join(t.TempDir(), "checksums.txt")
	if err := os.WriteFile(checksumsPath, []byte("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa  other-file.tar.gz\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fakeBin := makeFakeInstallCommands(t)

	output, err := runInstallScriptCommand(t, home, fakeBin, map[string]string{
		"FAKE_RELEASE_ARCHIVE": archivePath,
		"FAKE_CHECKSUMS":       checksumsPath,
	})
	if err == nil {
		t.Fatalf("install.sh should fail when checksums.txt has no entry for the archive\n%s", output)
	}
	assertNotInstalled(t, home)
}

func TestInstallScriptAcceptsMatchingChecksums(t *testing.T) {
	skipInstallScriptTestsOnWindows(t)

	home := t.TempDir()
	archivePath := filepath.Join(t.TempDir(), "no-mistakes-v1.2.3-darwin-arm64.tar.gz")
	binaryScript := "#!/bin/sh\nexit 0\n"
	makeInstallArchive(t, archivePath, binaryScript)
	checksumsPath := writeMatchingChecksums(t, archivePath)
	fakeBin := makeFakeInstallCommands(t)
	localBin := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(localBin, 0o755); err != nil {
		t.Fatal(err)
	}

	runInstallScript(t, home, fakeBin, map[string]string{
		"FAKE_RELEASE_ARCHIVE": archivePath,
		"FAKE_CHECKSUMS":       checksumsPath,
	})

	realBin := filepath.Join(home, ".no-mistakes", "bin", "no-mistakes")
	assertFileContent(t, realBin, binaryScript)
}

func skipInstallScriptTestsOnWindows(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("install.sh is a POSIX installer; Windows uses install.ps1")
	}
}

func runInstallScript(t *testing.T, home, fakeBin string, extraEnv map[string]string) {
	t.Helper()
	output, err := runInstallScriptCommand(t, home, fakeBin, extraEnv)
	if err != nil {
		t.Fatalf("install.sh failed: %v\n%s", err, output)
	}
}

func runInstallScriptCommand(t *testing.T, home, fakeBin string, extraEnv map[string]string) ([]byte, error) {
	t.Helper()

	if extraEnv == nil {
		extraEnv = map[string]string{}
	}
	if extraEnv["NO_MISTAKES_VERSION"] == "" {
		// Tests ship v1.2.3 archives; the installer pins a different default.
		extraEnv["NO_MISTAKES_VERSION"] = "v1.2.3"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sh", "docs/install.sh")
	pathValue := strings.Join([]string{fakeBin, filepath.Join(home, ".local", "bin"), os.Getenv("PATH")}, string(os.PathListSeparator))
	cmd.Env = append(filteredEnv(os.Environ(), "HOME", "PATH"), []string{
		"HOME=" + home,
		"PATH=" + pathValue,
	}...)
	for key, value := range extraEnv {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	return cmd.CombinedOutput()
}

func filteredEnv(env []string, excluded ...string) []string {
	blocked := make(map[string]struct{}, len(excluded))
	for _, key := range excluded {
		blocked[key] = struct{}{}
	}
	filtered := make([]string, 0, len(env))
	for _, entry := range env {
		key, _, found := strings.Cut(entry, "=")
		if !found {
			filtered = append(filtered, entry)
			continue
		}
		if _, skip := blocked[key]; skip {
			continue
		}
		filtered = append(filtered, entry)
	}
	return filtered
}

func makeInstallArchive(t *testing.T, archivePath, binaryContent string) {
	t.Helper()

	file, err := os.Create(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	gz := gzip.NewWriter(file)
	tw := tar.NewWriter(gz)
	data := []byte(binaryContent)
	hdr := &tar.Header{Name: "no-mistakes", Mode: 0o755, Size: int64(len(data))}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
}

func makeFakeInstallCommands(t *testing.T) string {
	t.Helper()

	binDir := t.TempDir()
	writeExecutable(t, filepath.Join(binDir, "uname"), `#!/bin/sh
case "$1" in
  -s) printf 'Darwin\n' ;;
  -m) printf 'arm64\n' ;;
  *) command uname "$@" ;;
esac
`)
	writeExecutable(t, filepath.Join(binDir, "curl"), `#!/bin/sh
out=""
url=""
while [ "$#" -gt 0 ]; do
  case "$1" in
    -o) out="$2"; shift 2 ;;
    -*) shift ;;
    *) url="$1"; shift ;;
  esac
done
case "$url" in
  */releases/latest*|*/repos/*/releases/latest*)
    echo "curl: unpinned latest scrape is forbidden" >&2
    exit 1
    ;;
  *checksums.txt*)
    if [ "${FAKE_CHECKSUMS_MISSING-}" = "1" ]; then
      echo "curl: failed to download checksums.txt" >&2
      exit 22
    fi
    if [ -n "${FAKE_CHECKSUMS-}" ]; then
      if [ -n "$out" ]; then
        cp "$FAKE_CHECKSUMS" "$out"
      else
        cat "$FAKE_CHECKSUMS"
      fi
      exit 0
    fi
    hash=$(sha256sum "$FAKE_RELEASE_ARCHIVE" | awk '{print $1}')
    name=$(basename "$FAKE_RELEASE_ARCHIVE")
    if [ -n "$out" ]; then
      printf '%s  %s\n' "$hash" "$name" > "$out"
    else
      printf '%s  %s\n' "$hash" "$name"
    fi
    exit 0
    ;;
esac
if [ -n "$out" ]; then
  cp "$FAKE_RELEASE_ARCHIVE" "$out"
  exit 0
fi
echo "curl: unexpected unpinned request: $url" >&2
exit 1
`)
	writeExecutable(t, filepath.Join(binDir, "sudo"), "#!/bin/sh\nexec \"$@\"\n")
	return binDir
}

func writeExecutable(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
}

func assertFileContent(t *testing.T, path, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != want {
		t.Fatalf("file %s = %q, want %q", path, string(data), want)
	}
}

func writeMatchingChecksums(t *testing.T, archivePath string) string {
	t.Helper()
	data, err := os.ReadFile(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	path := filepath.Join(t.TempDir(), "checksums.txt")
	line := hex.EncodeToString(sum[:]) + "  " + filepath.Base(archivePath) + "\n"
	if err := os.WriteFile(path, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func assertNotInstalled(t *testing.T, home string) {
	t.Helper()
	realBin := filepath.Join(home, ".no-mistakes", "bin", "no-mistakes")
	if _, err := os.Stat(realBin); err == nil {
		t.Fatalf("install.sh must not install a binary when checksum verification fails: %s exists", realBin)
	}
}

func assertSymlinkTarget(t *testing.T, path, wantTarget string) {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("%s is not a symlink", path)
	}
	target, err := os.Readlink(path)
	if err != nil {
		t.Fatal(err)
	}
	if target != wantTarget {
		t.Fatalf("symlink %s -> %s, want %s", path, target, wantTarget)
	}
}
