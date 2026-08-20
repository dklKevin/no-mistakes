package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestReleaseWorkflowUsesScopedConcurrencyGroup(t *testing.T) {
	data, err := os.ReadFile(".github/workflows/release.yml")
	if err != nil {
		t.Fatalf("read workflow: %v", err)
	}

	content := string(data)
	if !strings.Contains(content, "group: release-${{ github.ref }}") {
		t.Fatalf("release workflow must scope concurrency by ref")
	}
	if strings.Contains(content, "group: release\n") {
		t.Fatalf("release workflow must not use a global concurrency group")
	}
}

func TestReleaseWorkflowDoesNotDefineValidationJobs(t *testing.T) {
	data, err := os.ReadFile(".github/workflows/release.yml")
	if err != nil {
		t.Fatalf("read workflow: %v", err)
	}

	content := string(data)
	for _, job := range []string{"check", "test"} {
		if strings.Contains(content, "\n  "+job+":\n") {
			t.Fatalf("release workflow must not define %q; CI owns validation now", job)
		}
	}
}

func TestReleaseWorkflowRunsReleasePleaseWithoutValidationGuards(t *testing.T) {
	data, err := os.ReadFile(".github/workflows/release.yml")
	if err != nil {
		t.Fatalf("read workflow: %v", err)
	}

	block := extractJobBlock(t, string(data), "release-please")
	if strings.Contains(block, "needs:") {
		t.Fatalf("release-please must not depend on in-workflow validation jobs")
	}
	guard := "!startsWith(github.event.head_commit.message, 'chore(main): release')"
	if strings.Contains(block, guard) {
		t.Fatalf("release-please must not carry the old release-commit skip guard")
	}
}

func TestReleaseWorkflowBuildStartsOnlyWhenReleaseIsCreated(t *testing.T) {
	data, err := os.ReadFile(".github/workflows/release.yml")
	if err != nil {
		t.Fatalf("read workflow: %v", err)
	}

	block := extractJobBlock(t, string(data), "build-and-upload")
	if !strings.Contains(block, "if: needs.release-please.outputs.release_created == 'true'") {
		t.Fatalf("build-and-upload must run only when release-please created a release")
	}
	for _, unexpected := range []string{"!cancelled()", "needs.release-please.result == 'success'"} {
		if strings.Contains(block, unexpected) {
			t.Fatalf("build-and-upload must not keep the old skipped-validation guard %q", unexpected)
		}
	}
}

func TestReleaseWorkflowEmbedsSelfHostedTelemetryConfig(t *testing.T) {
	data, err := os.ReadFile(".github/workflows/release.yml")
	if err != nil {
		t.Fatalf("read workflow: %v", err)
	}

	block := extractJobBlock(t, string(data), "build-and-upload")
	for _, want := range []string{
		"UMAMI_HOST: https://a.kunchenguid.com",
		"UMAMI_WEBSITE_ID: f959e889-92f5-4121-8a1f-571b10861198",
		"TelemetryHost=${UMAMI_HOST}",
		"TelemetryWebsiteID=${UMAMI_WEBSITE_ID}",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("build-and-upload must contain %q", want)
		}
	}
}

// Partial-release protection: release-please must create drafts so that a
// release is never marked "latest" until all binaries and checksums are
// uploaded. A separate finalize job gates the promotion on every asset job
// succeeding.
func TestReleasePleaseConfigCreatesDrafts(t *testing.T) {
	data, err := os.ReadFile("release-please-config.json")
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var cfg struct {
		Packages map[string]struct {
			Draft bool `json:"draft"`
		} `json:"packages"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("parse config: %v", err)
	}
	pkg, ok := cfg.Packages["."]
	if !ok {
		t.Fatalf("release-please config missing '.' package")
	}
	if !pkg.Draft {
		t.Fatalf("release-please must create releases as drafts; partial releases would otherwise be marked latest before binaries are uploaded")
	}
}

func TestReleasePleaseConfigForcesTagCreation(t *testing.T) {
	data, err := os.ReadFile("release-please-config.json")
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var cfg struct {
		Packages map[string]struct {
			ForceTagCreation bool `json:"force-tag-creation"`
		} `json:"packages"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("parse config: %v", err)
	}
	pkg, ok := cfg.Packages["."]
	if !ok {
		t.Fatalf("release-please config missing '.' package")
	}
	if !pkg.ForceTagCreation {
		t.Fatalf("release-please config must force tag creation so an existing GitHub release cannot silently prevent the tag from being recreated")
	}
}

func TestReleaseWorkflowDoesNotOverrideReleaseType(t *testing.T) {
	data, err := os.ReadFile(".github/workflows/release.yml")
	if err != nil {
		t.Fatalf("read workflow: %v", err)
	}
	content := string(data)

	block := extractJobBlock(t, content, "release-please")
	if strings.Contains(block, "release-type:") {
		t.Fatalf("release workflow must not override release-type; release-please should read it from release-please-config.json")
	}
	if !strings.Contains(block, "config-file: release-please-config.json") {
		t.Fatalf("release workflow must point release-please at release-please-config.json")
	}
}

func TestReleaseWorkflowPublishesDraftOnlyAfterAssetsComplete(t *testing.T) {
	wf := loadReleaseWorkflowDoc(t)
	finalize := wf.Jobs["finalize"]
	if finalize == nil {
		t.Fatal("release workflow has no finalize job")
	}
	for _, want := range []string{
		"!cancelled()",
		"needs.release-please.result == 'success'",
		"needs.build-darwin.result == 'success'",
		"needs.build-and-upload.result == 'success'",
		"needs.checksums.result == 'success'",
		"needs.release-please.outputs.release_created == 'true'",
	} {
		if !strings.Contains(finalize.If, want) {
			t.Fatalf("finalize job condition missing %q: %q", want, finalize.If)
		}
	}
	for _, dep := range []string{"release-please", "build-darwin", "build-and-upload", "checksums"} {
		found := false
		for _, actual := range finalize.needs() {
			if actual == dep {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("finalize job must declare %q in needs so its gate sees all upstream results: %v", dep, finalize.needs())
		}
	}

	cases := []struct {
		tag  string
		want string
	}{
		{tag: "v1.2.3", want: "release edit v1.2.3 --draft=false --prerelease=false --latest=true"},
		{tag: "v1.2.3-beta.1", want: "release edit v1.2.3-beta.1 --draft=false --prerelease=true --latest=false"},
	}
	for _, tc := range cases {
		if got := runReleaseFinalizeScript(t, finalize, tc.tag); got != tc.want {
			t.Errorf("finalize for %s invoked gh as %q, want %q", tc.tag, got, tc.want)
		}
	}
}

func runReleaseFinalizeScript(t *testing.T, job *wfJob, tag string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("release workflow finalize scripts run under bash on GitHub's Linux runner")
	}
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skipf("bash is required to execute the release finalize script: %v", err)
	}

	var script string
	for _, step := range job.Steps {
		if step.Name == "Publish draft release" {
			script = step.Run
			break
		}
	}
	if script == "" {
		t.Fatal("finalize job has no publish step")
	}

	fakeBin := t.TempDir()
	writeExecutable(t, filepath.Join(fakeBin, "gh"), `#!/bin/sh
printf '%s\n' "$*" > "$GH_LOG"
`)
	logPath := filepath.Join(t.TempDir(), "gh.log")
	cmd := exec.Command("bash", "-c", script)
	cmd.Env = append(os.Environ(),
		"TAG="+tag,
		"GH_LOG="+logPath,
		"PATH="+fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"),
	)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("finalize script for %s failed: %v\n%s", tag, err, output)
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(data))
}

func TestExtractJobBlockHandlesCRLF(t *testing.T) {
	lf := "jobs:\n  foo:\n    runs-on: ubuntu-latest\n    steps:\n      - run: echo foo\n  bar:\n    runs-on: ubuntu-latest\n"
	crlf := strings.ReplaceAll(lf, "\n", "\r\n")

	block := extractJobBlock(t, crlf, "foo")
	if !strings.Contains(block, "echo foo") {
		t.Fatalf("CRLF block missing foo body: %q", block)
	}
	if strings.Contains(block, "bar:") {
		t.Fatalf("CRLF block must stop before next job: %q", block)
	}
}

func extractJobBlock(t *testing.T, content, name string) string {
	t.Helper()
	content = strings.ReplaceAll(content, "\r\n", "\n")
	header := "\n  " + name + ":\n"
	start := strings.Index(content, header)
	if start < 0 {
		t.Fatalf("could not locate %s job in workflow", name)
	}
	rest := content[start+len(header):]
	idx := 0
	for {
		next := strings.Index(rest[idx:], "\n  ")
		if next < 0 {
			return rest
		}
		pos := idx + next + 1
		if pos+2 >= len(rest) {
			return rest
		}
		ch := rest[pos+2]
		if ch != ' ' && ch != '#' && ch != '\n' {
			return rest[:pos]
		}
		idx = pos + 1
	}
}
