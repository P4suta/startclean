// SPDX-FileCopyrightText: 2026 startclean contributors <https://github.com/P4suta/startclean/graphs/contributors>
// SPDX-License-Identifier: MIT OR Apache-2.0

package main

import (
	"archive/zip"
	"bytes"
	"debug/pe"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func TestFormatGoFilesChecksWritesAndSkipsOutputDirectories(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	unformatted := []byte("package sample\nfunc answer( )int{return 42}\n")
	formatted := []byte("package sample\n\nfunc answer() int { return 42 }\n")
	writeTestFile(t, filepath.Join(root, "source.go"), unformatted)
	writeTestFile(t, filepath.Join(root, ".git", "ignored.go"), unformatted)
	writeTestFile(t, filepath.Join(root, "dist", "ignored.go"), unformatted)
	writeTestFile(t, filepath.Join(root, "notes.txt"), unformatted)

	changed, err := formatGoFiles(root, true)
	if err != nil {
		t.Fatalf("check formatting: %v", err)
	}
	if want := []string{"source.go"}; !reflect.DeepEqual(changed, want) {
		t.Fatalf("changed files = %#v, want %#v", changed, want)
	}
	if got := readTestFile(t, filepath.Join(root, "source.go")); !bytes.Equal(got, unformatted) {
		t.Fatalf("check mode rewrote source.go:\n%s", got)
	}

	changed, err = formatGoFiles(root, false)
	if err != nil {
		t.Fatalf("format files: %v", err)
	}
	if want := []string{"source.go"}; !reflect.DeepEqual(changed, want) {
		t.Fatalf("formatted files = %#v, want %#v", changed, want)
	}
	if got := readTestFile(t, filepath.Join(root, "source.go")); !bytes.Equal(got, formatted) {
		t.Fatalf("formatted source.go =\n%s\nwant:\n%s", got, formatted)
	}
	for _, path := range []string{
		filepath.Join(root, ".git", "ignored.go"),
		filepath.Join(root, "dist", "ignored.go"),
	} {
		if got := readTestFile(t, path); !bytes.Equal(got, unformatted) {
			t.Fatalf("skipped file %s was rewritten", path)
		}
	}
}

func TestChangedSnapshotsDetectsContentCreationAndRemoval(t *testing.T) {
	t.Parallel()

	unchanged := fileState{exists: true, digest: [32]byte{1}}
	before := map[string]fileState{
		"unchanged": unchanged,
		"modified":  {exists: true, digest: [32]byte{2}},
		"removed":   {exists: true, digest: [32]byte{3}},
	}
	after := map[string]fileState{
		"unchanged": unchanged,
		"modified":  {exists: true, digest: [32]byte{4}},
		"created":   {exists: true, digest: [32]byte{5}},
	}

	got := changedSnapshots(before, after)
	want := []string{"created", "modified", "removed"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("changed snapshots = %#v, want %#v", got, want)
	}
}

func TestGenerateCheckUsesTemporaryOutputWithoutRewritingTrackedFiles(t *testing.T) {
	t.Parallel()

	trackedContents := []byte("tracked completion\n")
	generatedContents := []byte("new generated completion\n")
	var temporaryOutput string
	app := testApplication(t, func(
		_ string,
		command invocation,
		_ io.Reader,
		_ io.Writer,
		_ io.Writer,
	) error {
		for index, argument := range command.args {
			if argument == "--output" && index+1 < len(command.args) {
				temporaryOutput = command.args[index+1]
			}
		}
		if temporaryOutput == "" {
			return errors.New("missing --output argument")
		}
		for _, path := range generatedCompletionFiles {
			writeTestFile(t, filepath.Join(temporaryOutput, filepath.Base(path)), generatedContents)
		}
		return nil
	})
	for _, path := range generatedCompletionFiles {
		writeTestFile(t, filepath.Join(app.root, filepath.FromSlash(path)), trackedContents)
	}

	err := app.run([]string{"generate", "--check"})
	if err == nil || !strings.Contains(err.Error(), "out of date") {
		t.Fatalf("generate check error = %v, want out-of-date error", err)
	}
	for _, path := range generatedCompletionFiles {
		got := readTestFile(t, filepath.Join(app.root, filepath.FromSlash(path)))
		if !bytes.Equal(got, trackedContents) {
			t.Fatalf("tracked file %s was rewritten:\n%s", path, got)
		}
	}
	if _, statErr := os.Stat(filepath.Dir(temporaryOutput)); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("temporary generation root was not removed: %v", statErr)
	}
}

func TestTestCommandsDisableCachingAndShuffleUnitTests(t *testing.T) {
	t.Parallel()

	var calls []invocation
	app := testApplication(t, func(
		_ string,
		command invocation,
		_ io.Reader,
		_ io.Writer,
		_ io.Writer,
	) error {
		calls = append(calls, command)
		return nil
	})

	if err := app.run([]string{"test"}); err != nil {
		t.Fatalf("run test: %v", err)
	}
	if err := app.run([]string{"integration"}); err != nil {
		t.Fatalf("run integration: %v", err)
	}
	if err := app.run([]string{"stress"}); err != nil {
		t.Fatalf("run stress: %v", err)
	}

	want := []invocation{
		{
			name: "go",
			args: []string{
				"test", "-buildvcs=false", "-shuffle=on", "-count=1", "./...",
			},
		},
		{
			name: "go",
			args: []string{
				"test", "-buildvcs=false", "-count=1", "-tags=integration",
				"./internal/platform",
			},
		},
		{
			name: "go",
			args: []string{
				"test", "-buildvcs=false", "-count=10", "-tags=integration",
				"./internal/platform",
			},
		},
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("test commands = %#v, want %#v", calls, want)
	}
}

func TestCoveragePassesEveryGoFlagAsASeparateArgument(t *testing.T) {
	t.Parallel()

	var calls []invocation
	app := testApplication(t, func(
		_ string,
		command invocation,
		_ io.Reader,
		_ io.Writer,
		_ io.Writer,
	) error {
		calls = append(calls, command)
		return nil
	})

	if err := app.run([]string{"coverage"}); err != nil {
		t.Fatalf("run coverage: %v", err)
	}
	want := []invocation{
		{
			name: "go",
			args: []string{
				"test",
				"-buildvcs=false",
				"-count=1",
				"-covermode",
				"atomic",
				"-coverprofile",
				"coverage.out",
				"./...",
			},
		},
		{
			name: "go",
			args: []string{
				"test", "-buildvcs=false", "-count=1", "-covermode", "atomic",
				"-coverprofile", "coverage-core.out", "./internal/core",
			},
		},
		{
			name: "go",
			args: []string{
				"test", "-buildvcs=false", "-count=1", "-tags=integration",
				"-covermode", "atomic", "-coverprofile", "coverage-platform.out",
				"./internal/platform",
			},
		},
		{
			name: "go",
			args: []string{"tool", "cover", "-func", "coverage.out"},
		},
		{
			name: "go-test-coverage",
			args: []string{"--config", ".testcoverage.yml"},
		},
		{
			name: "go",
			args: []string{"tool", "cover", "-func", "coverage-core.out"},
		},
		{
			name: "go-test-coverage",
			args: []string{"--profile", "coverage-core.out", "--threshold-total", "80"},
		},
		{
			name: "go",
			args: []string{"tool", "cover", "-func", "coverage-platform.out"},
		},
		{
			name: "go-test-coverage",
			args: []string{"--profile", "coverage-platform.out", "--threshold-total", "65"},
		},
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("coverage commands = %#v, want %#v", calls, want)
	}
}

func TestModuleVerifyChecksDownloadsAndTidyDiff(t *testing.T) {
	t.Parallel()

	var calls []invocation
	app := testApplication(t, func(
		_ string,
		command invocation,
		_ io.Reader,
		_ io.Writer,
		_ io.Writer,
	) error {
		calls = append(calls, command)
		return nil
	})

	if err := app.run([]string{"module-verify"}); err != nil {
		t.Fatalf("run module-verify: %v", err)
	}
	want := []invocation{
		{name: "go", args: []string{"mod", "verify"}},
		{name: "go", args: []string{"mod", "tidy", "-diff"}},
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("module-verify commands = %#v, want %#v", calls, want)
	}
}

func TestPullRequestTitleRequiresConventionalCommitSubject(t *testing.T) {
	t.Parallel()

	app := testApplication(t, nil)
	var title string
	app.getenv = func(name string) string {
		if name == "STARTCLEAN_PR_TITLE" {
			return title
		}
		return ""
	}
	for _, valid := range []string{
		"feat: add safe cleanup",
		"fix(windows)!: harden handle validation",
	} {
		title = valid
		if err := app.run([]string{"pr-title"}); err != nil {
			t.Errorf("valid title %q rejected: %v", valid, err)
		}
	}
	for _, invalid := range []string{
		"",
		"Add safe cleanup",
		"feat: valid first line\nunsafe second line",
		" feat: leading whitespace",
	} {
		title = invalid
		if err := app.run([]string{"pr-title"}); err == nil {
			t.Errorf("invalid title %q accepted", invalid)
		}
	}
}

func TestRaceUsesCGOAndNonCachedShuffledTests(t *testing.T) {
	t.Parallel()

	var calls []invocation
	app := testApplication(t, func(
		_ string,
		command invocation,
		_ io.Reader,
		_ io.Writer,
		_ io.Writer,
	) error {
		calls = append(calls, command)
		return nil
	})

	if err := app.run([]string{"race"}); err != nil {
		t.Fatalf("run race: %v", err)
	}
	want := []invocation{{
		name: "go",
		args: []string{
			"test",
			"-buildvcs=false",
			"-race",
			"-shuffle=on",
			"-count=1",
			"./...",
		},
		env: []environmentSetting{{name: "CGO_ENABLED", value: "1"}},
	}}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("race commands = %#v, want %#v", calls, want)
	}
}

func TestFuzzRunsEachSafetyInvariantWithABoundedBudget(t *testing.T) {
	t.Parallel()

	var calls []invocation
	app := testApplication(t, func(
		_ string,
		command invocation,
		_ io.Reader,
		_ io.Writer,
		_ io.Writer,
	) error {
		calls = append(calls, command)
		return nil
	})

	if err := app.run([]string{"fuzz"}); err != nil {
		t.Fatalf("run fuzz: %v", err)
	}
	want := []invocation{
		{
			name: "go",
			args: []string{
				"test", "-buildvcs=false", "-run=^$",
				"-fuzz=^FuzzInsideRootContainment$", "-fuzztime=10s", "./internal/core",
			},
		},
		{
			name: "go",
			args: []string{
				"test", "-buildvcs=false", "-run=^$",
				"-fuzz=^FuzzClassifierOnlyMarksDefinitivelyMissingFixedTargetsStale$",
				"-fuzztime=10s", "./internal/core",
			},
		},
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("fuzz commands = %#v, want %#v", calls, want)
	}
}

func TestBuildTargetsBothWindowsArchitectures(t *testing.T) {
	t.Parallel()

	var calls []invocation
	app := testApplication(t, func(
		_ string,
		command invocation,
		_ io.Reader,
		_ io.Writer,
		_ io.Writer,
	) error {
		calls = append(calls, command)
		return nil
	})

	if err := app.run([]string{"build"}); err != nil {
		t.Fatalf("run build: %v", err)
	}
	if len(calls) != 2 {
		t.Fatalf("build command count = %d, want 2", len(calls))
	}
	for index, architecture := range []string{"amd64", "arm64"} {
		command := calls[index]
		if command.name != "go" {
			t.Errorf("build %d executable = %q, want go", index, command.name)
		}
		if got := environmentValue(command.env, "CGO_ENABLED"); got != "0" {
			t.Errorf("build %d CGO_ENABLED = %q, want 0", index, got)
		}
		if got := environmentValue(command.env, "GOOS"); got != "windows" {
			t.Errorf("build %d GOOS = %q, want windows", index, got)
		}
		if got := environmentValue(command.env, "GOARCH"); got != architecture {
			t.Errorf("build %d GOARCH = %q, want %q", index, got, architecture)
		}
	}
}

func TestExternalFailureStopsASequence(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("failed")
	var calls []string
	app := testApplication(t, func(
		_ string,
		command invocation,
		_ io.Reader,
		_ io.Writer,
		_ io.Writer,
	) error {
		calls = append(calls, command.name)
		if command.name == "actionlint" {
			return sentinel
		}
		return nil
	})

	err := app.run([]string{"lint"})
	if !errors.Is(err, sentinel) {
		t.Fatalf("lint error = %v, want wrapped sentinel", err)
	}
	if want := []string{"golangci-lint", "actionlint"}; !reflect.DeepEqual(calls, want) {
		t.Fatalf("commands = %#v, want %#v", calls, want)
	}
}

func TestLintIncludesTaploFormatCheck(t *testing.T) {
	t.Parallel()

	var calls []invocation
	app := testApplication(t, func(
		_ string,
		command invocation,
		_ io.Reader,
		_ io.Writer,
		_ io.Writer,
	) error {
		calls = append(calls, command)
		return nil
	})

	if err := app.run([]string{"lint"}); err != nil {
		t.Fatalf("run lint: %v", err)
	}
	want := []invocation{
		{name: "golangci-lint", args: []string{"run", "./..."}},
		{name: "actionlint"},
		{name: "typos"},
		{name: "taplo", args: []string{"format", "--check"}},
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("lint commands = %#v, want %#v", calls, want)
	}
}

func TestSecurityTasksUseCurrentScanningCommands(t *testing.T) {
	t.Parallel()

	var calls []invocation
	app := testApplication(t, func(
		_ string,
		command invocation,
		_ io.Reader,
		_ io.Writer,
		_ io.Writer,
	) error {
		calls = append(calls, command)
		return nil
	})

	if err := app.run([]string{"security"}); err != nil {
		t.Fatalf("run security: %v", err)
	}
	if err := app.run([]string{"secrets"}); err != nil {
		t.Fatalf("run secrets: %v", err)
	}
	want := []invocation{
		{name: "govulncheck", args: []string{"./..."}},
		{name: "osv-scanner", args: []string{"scan", "source", "--recursive", "."}},
		{name: "gitleaks", args: []string{"git", ".", "--redact"}},
		{
			name: "gitleaks",
			args: []string{"git", ".", "--pre-commit", "--staged", "--redact"},
		},
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("security commands = %#v, want %#v", calls, want)
	}
}

func TestReleaseSourceChecksTidyAndGeneratedFiles(t *testing.T) {
	t.Parallel()

	generatedContents := []byte("generated completion\n")
	var calls []invocation
	app := testApplication(t, func(
		_ string,
		command invocation,
		_ io.Reader,
		_ io.Writer,
		_ io.Writer,
	) error {
		calls = append(calls, command)
		for index, argument := range command.args {
			if argument != "--output" || index+1 >= len(command.args) {
				continue
			}
			output := command.args[index+1]
			for _, path := range generatedCompletionFiles {
				writeTestFile(t, filepath.Join(output, filepath.Base(path)), generatedContents)
			}
		}
		return nil
	})
	for _, path := range generatedCompletionFiles {
		writeTestFile(t, filepath.Join(app.root, filepath.FromSlash(path)), generatedContents)
	}

	if err := app.run([]string{"release-source"}); err != nil {
		t.Fatalf("run release-source: %v", err)
	}
	if len(calls) != 3 {
		t.Fatalf("release-source command count = %d, want 3", len(calls))
	}
	if want := (invocation{name: "go", args: []string{"mod", "verify"}}); !reflect.DeepEqual(calls[0], want) {
		t.Errorf("verify command = %#v, want %#v", calls[0], want)
	}
	if want := (invocation{name: "go", args: []string{"mod", "tidy", "-diff"}}); !reflect.DeepEqual(calls[1], want) {
		t.Errorf("tidy command = %#v, want %#v", calls[1], want)
	}
	if got := calls[2]; got.name != "go" || len(got.args) < 3 || got.args[0] != "run" {
		t.Errorf("generation command = %#v", got)
	}
}

func TestReleasePreflightUsesValidatedTag(t *testing.T) {
	t.Parallel()

	var calls []invocation
	app := testApplication(t, func(
		_ string,
		command invocation,
		_ io.Reader,
		_ io.Writer,
		_ io.Writer,
	) error {
		calls = append(calls, command)
		return nil
	})
	values := map[string]string{
		"GITHUB_REF_TYPE": "tag",
		"GITHUB_REF_NAME": "v1.2.3-rc.1",
	}
	app.getenv = func(name string) string { return values[name] }

	if err := app.run([]string{"release-preflight"}); err != nil {
		t.Fatalf("release preflight: %v", err)
	}
	want := []invocation{{
		name: "git",
		args: []string{
			"merge-base", "--is-ancestor", "refs/tags/v1.2.3-rc.1^{commit}", "origin/main",
		},
	}}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("release commands = %#v, want %#v", calls, want)
	}
}

func TestReleasePublishUsesVersionedGitHubRESTContract(t *testing.T) {
	t.Parallel()
	const tag = "v1.2.3-rc.1"

	type requestRecord struct {
		method     string
		path       string
		authorize  string
		accept     string
		apiVersion string
		userAgent  string
		body       string
	}
	app := testApplication(t, nil)
	remoteAssets := createReleasePublishFixture(t, app.root, tag)
	var mutex sync.Mutex
	var requests []requestRecord
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		mutex.Lock()
		requests = append(requests, requestRecord{
			method:     request.Method,
			path:       request.URL.Path,
			authorize:  request.Header.Get("Authorization"),
			accept:     request.Header.Get("Accept"),
			apiVersion: request.Header.Get("X-GitHub-Api-Version"),
			userAgent:  request.Header.Get("User-Agent"),
			body:       string(body),
		})
		mutex.Unlock()
		writer.Header().Set("Content-Type", "application/json")
		switch request.Method {
		case http.MethodGet:
			_ = json.NewEncoder(writer).Encode(gitHubRelease{
				ID:      42,
				TagName: tag,
				Draft:   true,
				Assets:  remoteAssets,
			})
		case http.MethodPatch:
			_, _ = io.WriteString(writer, `{"id":42,"tag_name":"v1.2.3-rc.1","draft":false}`)
		default:
			writer.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	defer server.Close()

	values := map[string]string{
		"GITHUB_REF_TYPE":   "tag",
		"GITHUB_REF_NAME":   tag,
		"GITHUB_REPOSITORY": "P4suta/startclean",
		"GITHUB_TOKEN":      "test-token",
		"GITHUB_API_URL":    server.URL,
	}
	app.getenv = func(name string) string { return values[name] }
	app.http = server.Client()

	if err := app.run([]string{"release-publish"}); err != nil {
		t.Fatalf("release publish: %v", err)
	}
	mutex.Lock()
	got := append([]requestRecord(nil), requests...)
	mutex.Unlock()
	if len(got) != 2 {
		t.Fatalf("GitHub API request count = %d, want 2: %#v", len(got), got)
	}
	wantPaths := []string{
		"/repos/P4suta/startclean/releases/tags/v1.2.3-rc.1",
		"/repos/P4suta/startclean/releases/42",
	}
	for index, request := range got {
		if request.path != wantPaths[index] ||
			request.authorize != "Bearer test-token" ||
			request.accept != "application/vnd.github+json" ||
			request.apiVersion != "2026-03-10" ||
			request.userAgent != "startclean-devtool" {
			t.Fatalf("GitHub API request %d = %#v", index, request)
		}
	}
	if got[0].method != http.MethodGet || got[0].body != "" {
		t.Fatalf("lookup request = %#v", got[0])
	}
	if got[1].method != http.MethodPatch || got[1].body != `{"draft":false}` {
		t.Fatalf("publish request = %#v", got[1])
	}
}

func TestReleasePublishRejectsInvalidDraftAssetsBeforePatch(t *testing.T) {
	t.Parallel()
	const tag = "v1.2.3"

	tests := []struct {
		name   string
		mutate func([]gitHubReleaseAsset) []gitHubReleaseAsset
		want   string
	}{
		{
			name: "missing",
			mutate: func(assets []gitHubReleaseAsset) []gitHubReleaseAsset {
				return assets[:len(assets)-1]
			},
			want: "missing asset checksums.txt",
		},
		{
			name: "extra",
			mutate: func(assets []gitHubReleaseAsset) []gitHubReleaseAsset {
				return append(assets, gitHubReleaseAsset{
					Name:   "unexpected.txt",
					State:  "uploaded",
					Size:   1,
					Digest: "sha256:" + strings.Repeat("0", 64),
				})
			},
			want: "unexpected asset unexpected.txt",
		},
		{
			name: "duplicate",
			mutate: func(assets []gitHubReleaseAsset) []gitHubReleaseAsset {
				return append(assets, assets[0])
			},
			want: "duplicate asset",
		},
		{
			name: "state",
			mutate: func(assets []gitHubReleaseAsset) []gitHubReleaseAsset {
				assets[0].State = "new"
				return assets
			},
			want: `has state "new", want "uploaded"`,
		},
		{
			name: "size",
			mutate: func(assets []gitHubReleaseAsset) []gitHubReleaseAsset {
				assets[0].Size++
				return assets
			},
			want: "has size",
		},
		{
			name: "digest",
			mutate: func(assets []gitHubReleaseAsset) []gitHubReleaseAsset {
				assets[0].Digest = "sha256:" + strings.Repeat("0", 64)
				return assets
			},
			want: "has digest",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			app := testApplication(t, nil)
			remoteAssets := test.mutate(createReleasePublishFixture(t, app.root, tag))
			var mutex sync.Mutex
			patchCalls := 0
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				writer.Header().Set("Content-Type", "application/json")
				switch request.Method {
				case http.MethodGet:
					_ = json.NewEncoder(writer).Encode(gitHubRelease{
						ID:      42,
						TagName: tag,
						Draft:   true,
						Assets:  remoteAssets,
					})
				case http.MethodPatch:
					mutex.Lock()
					patchCalls++
					mutex.Unlock()
					_, _ = io.WriteString(writer, `{"id":42,"tag_name":"v1.2.3","draft":false}`)
				default:
					writer.WriteHeader(http.StatusMethodNotAllowed)
				}
			}))
			defer server.Close()

			values := map[string]string{
				"GITHUB_REF_TYPE":   "tag",
				"GITHUB_REF_NAME":   tag,
				"GITHUB_REPOSITORY": "P4suta/startclean",
				"GITHUB_TOKEN":      "test-token",
				"GITHUB_API_URL":    server.URL,
			}
			app.getenv = func(name string) string { return values[name] }
			app.http = server.Client()

			err := app.run([]string{"release-publish"})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("release-publish error = %v, want substring %q", err, test.want)
			}
			mutex.Lock()
			gotPatchCalls := patchCalls
			mutex.Unlock()
			if gotPatchCalls != 0 {
				t.Fatalf("release-publish made %d PATCH requests after invalid draft assets", gotPatchCalls)
			}
		})
	}
}

func TestReleaseCommandsRejectUntrustedRefBeforeExternalCommands(t *testing.T) {
	t.Parallel()

	app := testApplication(t, nil)
	values := map[string]string{
		"GITHUB_REF_TYPE": "branch",
		"GITHUB_REF_NAME": "v1.2.3;danger",
	}
	app.getenv = func(name string) string { return values[name] }

	for _, command := range []string{"release-preflight", "release-publish"} {
		if err := app.run([]string{command}); err == nil {
			t.Fatalf("%s accepted an untrusted ref", command)
		}
	}
}

func TestReleaseCommandsRejectNonCanonicalSemVerTags(t *testing.T) {
	t.Parallel()

	invalidTags := []string{
		"v01.2.3",
		"v1.02.3",
		"v1.2.03",
		"v1.2.3-01",
		"v1.2.3-",
		"v1.2.3+",
		"v1.2.3-rc..1",
	}
	for _, tag := range invalidTags {
		t.Run(tag, func(t *testing.T) {
			t.Parallel()
			app := testApplication(t, nil)
			app.getenv = func(name string) string {
				values := map[string]string{
					"GITHUB_REF_TYPE": "tag",
					"GITHUB_REF_NAME": tag,
				}
				return values[name]
			}
			if err := app.run([]string{"release-preflight"}); err == nil {
				t.Fatalf("release-preflight accepted non-canonical SemVer tag %q", tag)
			}
		})
	}
}

func TestReleasePublishRejectsAmbiguousRepositorySegments(t *testing.T) {
	t.Parallel()

	for _, repository := range []string{"../startclean", "P4suta/..", "./startclean", "P4suta/."} {
		t.Run(repository, func(t *testing.T) {
			t.Parallel()
			app := testApplication(t, nil)
			app.getenv = func(name string) string {
				values := map[string]string{
					"GITHUB_REF_TYPE":   "tag",
					"GITHUB_REF_NAME":   "v1.2.3",
					"GITHUB_REPOSITORY": repository,
					"GITHUB_TOKEN":      "test-token",
				}
				return values[name]
			}
			if err := app.run([]string{"release-publish"}); err == nil {
				t.Fatalf("release-publish accepted ambiguous repository %q", repository)
			}
		})
	}
}

func TestReleaseSmokeRunsGoreleaserAndVerifiesArtifacts(t *testing.T) {
	t.Parallel()

	var calls []invocation
	app := testApplication(t, func(
		_ string,
		command invocation,
		_ io.Reader,
		stdout io.Writer,
		_ io.Writer,
	) error {
		calls = append(calls, command)
		if strings.EqualFold(filepath.Ext(command.name), ".exe") {
			_, _ = fmt.Fprintln(stdout, testReleaseVersionOutput)
		}
		return nil
	})
	createReleaseFixture(t, filepath.Join(app.root, "dist"))

	if err := app.run([]string{"release-smoke"}); err != nil {
		t.Fatalf("run release-smoke: %v", err)
	}
	if len(calls) != 6 {
		t.Fatalf("release-smoke command count = %d, want 6: %#v", len(calls), calls)
	}
	wantRelease := invocation{name: "goreleaser", args: []string{"release", "--snapshot", "--clean"}}
	if !reflect.DeepEqual(calls[0], wantRelease) {
		t.Fatalf("release command = %#v, want %#v", calls[0], wantRelease)
	}
	for _, command := range calls[1:5] {
		if command.name != "syft" || len(command.args) != 5 || command.args[0] != "convert" || command.args[2] != "--output" || !strings.HasPrefix(command.args[3], "syft-json=") || command.args[4] != "--quiet" {
			t.Fatalf("unexpected SBOM validation command: %#v", command)
		}
	}
	if runtimeCall := calls[5]; !strings.EqualFold(filepath.Base(runtimeCall.name), "startclean.exe") || !reflect.DeepEqual(runtimeCall.args, []string{"version"}) {
		t.Fatalf("unexpected runtime verification command: %#v", runtimeCall)
	}
}

func TestReleaseVerifyChecksExistingArtifactsWithoutRebuilding(t *testing.T) {
	t.Parallel()

	var calls []invocation
	app := testApplication(t, func(
		_ string,
		command invocation,
		_ io.Reader,
		stdout io.Writer,
		_ io.Writer,
	) error {
		calls = append(calls, command)
		if strings.EqualFold(filepath.Ext(command.name), ".exe") {
			_, _ = fmt.Fprintln(stdout, testReleaseVersionOutput)
		}
		return nil
	})
	createReleaseFixture(t, filepath.Join(app.root, "dist"))

	if err := app.run([]string{"release-verify"}); err != nil {
		t.Fatalf("run release-verify: %v", err)
	}
	if len(calls) != 5 {
		t.Fatalf("release-verify command count = %d, want 5: %#v", len(calls), calls)
	}
	for _, command := range calls[:4] {
		if command.name != "syft" || command.args[0] != "convert" {
			t.Fatalf("release-verify invoked an unexpected command: %#v", command)
		}
	}
	if !strings.EqualFold(filepath.Base(calls[4].name), "startclean.exe") {
		t.Fatalf("release-verify did not execute the packaged x64 binary: %#v", calls[4])
	}
}

func TestReleaseVerifyRejectsIncorrectRuntimeVersionMetadata(t *testing.T) {
	t.Parallel()

	app := testApplication(t, func(
		_ string,
		command invocation,
		_ io.Reader,
		stdout io.Writer,
		_ io.Writer,
	) error {
		if strings.EqualFold(filepath.Ext(command.name), ".exe") {
			_, _ = fmt.Fprintln(stdout, "startclean dev (commit unknown, built unknown)")
		}
		return nil
	})
	createReleaseFixture(t, filepath.Join(app.root, "dist"))

	err := app.run([]string{"release-verify"})
	if err == nil || !strings.Contains(err.Error(), "version output") {
		t.Fatalf("release-verify accepted incorrect runtime metadata: %v", err)
	}
}

func TestVerifyReleaseArtifactsRejectsIncompleteOrCorruptArtifacts(t *testing.T) {
	t.Parallel()

	t.Run("missing archive member", func(t *testing.T) {
		t.Parallel()

		dist := t.TempDir()
		archives := createReleaseFixture(t, dist)
		writeTestArchive(t, archives[0], requiredReleaseArchiveFiles[1:])

		err := verifyReleaseArtifacts(dist)
		if err == nil || !strings.Contains(err.Error(), "missing startclean.exe") {
			t.Fatalf("verify incomplete archive error = %v", err)
		}
	})

	t.Run("checksum mismatch", func(t *testing.T) {
		t.Parallel()

		dist := t.TempDir()
		archives := createReleaseFixture(t, dist)
		spdxPath := archives[0] + ".spdx.json"
		contents, readErr := os.ReadFile(spdxPath) //nolint:gosec // Fixture path is beneath t.TempDir.
		if readErr != nil {
			t.Fatal(readErr)
		}
		writeTestFile(t, spdxPath, append(contents, '\n'))

		err := verifyReleaseArtifacts(dist)
		if err == nil || !strings.Contains(err.Error(), "SHA-256 mismatch") {
			t.Fatalf("verify corrupt artifact error = %v", err)
		}
	})

	t.Run("unexpected archive member", func(t *testing.T) {
		t.Parallel()

		dist := t.TempDir()
		archives := createReleaseFixture(t, dist)
		files := append([]string(nil), requiredReleaseArchiveFiles...)
		files = append(files, "debug.txt")
		writeTestArchive(t, archives[0], files)

		err := verifyReleaseArtifacts(dist)
		if err == nil || !strings.Contains(err.Error(), "unexpected member debug.txt") {
			t.Fatalf("verify unexpected archive member error = %v", err)
		}
	})

	t.Run("wrong PE architecture", func(t *testing.T) {
		t.Parallel()

		dist := t.TempDir()
		archives := createReleaseFixture(t, dist)
		writeTestArchiveMachine(t, archives[0], requiredReleaseArchiveFiles, pe.IMAGE_FILE_MACHINE_ARM64)

		err := verifyReleaseArtifacts(dist)
		if err == nil || !strings.Contains(err.Error(), "PE machine") {
			t.Fatalf("verify wrong PE architecture error = %v", err)
		}
	})

	t.Run("invalid CycloneDX format", func(t *testing.T) {
		t.Parallel()

		dist := t.TempDir()
		archives := createReleaseFixture(t, dist)
		writeTestFile(t, archives[0]+".cdx.json", []byte("{\"bomFormat\":\"not-cyclonedx\"}"))

		err := verifyReleaseArtifacts(dist)
		if err == nil || !strings.Contains(err.Error(), "invalid bomFormat") {
			t.Fatalf("verify invalid CycloneDX error = %v", err)
		}
	})

	t.Run("empty SPDX packages", func(t *testing.T) {
		t.Parallel()

		dist := t.TempDir()
		archives := createReleaseFixture(t, dist)
		writeTestFile(t, archives[0]+".spdx.json", fmt.Appendf(nil, `{"spdxVersion":"SPDX-2.3","dataLicense":"CC0-1.0","SPDXID":"SPDXRef-DOCUMENT","name":%q,"packages":[]}`, filepath.Base(archives[0])))

		err := verifyReleaseArtifacts(dist)
		if err == nil || !strings.Contains(err.Error(), "no identified SPDX packages") {
			t.Fatalf("verify empty SPDX packages error = %v", err)
		}
	})

	t.Run("empty CycloneDX components", func(t *testing.T) {
		t.Parallel()

		dist := t.TempDir()
		archives := createReleaseFixture(t, dist)
		digest, hashErr := fileSHA256(archives[0])
		if hashErr != nil {
			t.Fatal(hashErr)
		}
		writeTestFile(t, archives[0]+".cdx.json", fmt.Appendf(nil, `{"bomFormat":"CycloneDX","specVersion":"1.7","version":1,"metadata":{"component":{"type":"file","name":%q,"version":%q}},"components":[]}`, filepath.Base(archives[0]), "sha256:"+digest))

		err := verifyReleaseArtifacts(dist)
		if err == nil || !strings.Contains(err.Error(), "no identified CycloneDX components") {
			t.Fatalf("verify empty CycloneDX components error = %v", err)
		}
	})

	t.Run("SPDX names a different archive", func(t *testing.T) {
		t.Parallel()

		dist := t.TempDir()
		archives := createReleaseFixture(t, dist)
		writeTestFile(t, archives[0]+".spdx.json", []byte(`{"spdxVersion":"SPDX-2.3","dataLicense":"CC0-1.0","SPDXID":"SPDXRef-DOCUMENT","name":"other.zip","packages":[{"name":"github.com/P4suta/startclean","SPDXID":"SPDXRef-Package-startclean","versionInfo":"v1.0.0"}]}`))

		err := verifyReleaseArtifacts(dist)
		if err == nil || !strings.Contains(err.Error(), "identifies archive") {
			t.Fatalf("verify mismatched SPDX archive error = %v", err)
		}
	})

	t.Run("CycloneDX has stale archive digest", func(t *testing.T) {
		t.Parallel()

		dist := t.TempDir()
		archives := createReleaseFixture(t, dist)
		writeTestFile(t, archives[0]+".cdx.json", fmt.Appendf(nil, `{"bomFormat":"CycloneDX","specVersion":"1.7","version":1,"metadata":{"component":{"type":"file","name":%q,"version":"sha256:stale"}},"components":[{"name":"github.com/P4suta/startclean","type":"application","version":"v0.0.0-SNAPSHOT-test"}]}`, filepath.Base(archives[0])))

		err := verifyReleaseArtifacts(dist)
		if err == nil || !strings.Contains(err.Error(), "is not bound to archive") {
			t.Fatalf("verify stale CycloneDX archive digest error = %v", err)
		}
	})

	t.Run("SBOM project versions disagree", func(t *testing.T) {
		t.Parallel()

		dist := t.TempDir()
		archives := createReleaseFixture(t, dist)
		digest, hashErr := fileSHA256(archives[0])
		if hashErr != nil {
			t.Fatal(hashErr)
		}
		writeTestFile(t, archives[0]+".cdx.json", fmt.Appendf(nil, `{"bomFormat":"CycloneDX","specVersion":"1.7","version":1,"metadata":{"component":{"type":"file","name":%q,"version":%q}},"components":[{"name":"github.com/P4suta/startclean","type":"application","version":"v9.9.9"}]}`, filepath.Base(archives[0]), "sha256:"+digest))

		err := verifyReleaseArtifacts(dist)
		if err == nil || !strings.Contains(err.Error(), "project version mismatch") {
			t.Fatalf("verify mismatched SBOM project versions error = %v", err)
		}
	})
}

func TestCommitMessageValidatesConventionalCommits(t *testing.T) {
	t.Parallel()

	app := testApplication(t, nil)
	for _, message := range []string{
		"feat: add cleaner",
		"fix(cli)!: change flags\n\nBREAKING CHANGE: flags changed",
		"chore(build-system): update tools",
	} {
		path := filepath.Join(app.root, "message.txt")
		writeTestFile(t, path, []byte(message))
		if err := app.run([]string{"commit-msg", path}); err != nil {
			t.Errorf("message %q rejected: %v", message, err)
		}
	}

	path := filepath.Join(app.root, "message.txt")
	writeTestFile(t, path, []byte("Added cleaner"))
	err := app.run([]string{"commit-msg", path})
	if err == nil || !strings.Contains(err.Error(), "Conventional Commits") {
		t.Fatalf("invalid message error = %v", err)
	}
}

func TestReuseFallsBackToPinnedUVXInvocation(t *testing.T) {
	t.Parallel()

	var calls []invocation
	app := testApplication(t, func(
		_ string,
		command invocation,
		_ io.Reader,
		_ io.Writer,
		_ io.Writer,
	) error {
		calls = append(calls, command)
		return nil
	})
	app.lookPath = func(string) (string, error) {
		return "", errors.New("not found")
	}

	if err := app.run([]string{"reuse"}); err != nil {
		t.Fatalf("run reuse: %v", err)
	}
	want := []invocation{{
		name: "uvx",
		args: []string{
			"--from",
			"reuse[charset-normalizer]==6.2.0",
			"reuse",
			"lint",
		},
	}}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("reuse commands = %#v, want %#v", calls, want)
	}
}

func TestPolicyToolsAreExactPinnedInstalls(t *testing.T) {
	t.Parallel()

	var calls []invocation
	app := testApplication(t, func(
		_ string,
		command invocation,
		_ io.Reader,
		_ io.Writer,
		_ io.Writer,
	) error {
		calls = append(calls, command)
		return nil
	})

	if err := app.run([]string{"tools", "policy"}); err != nil {
		t.Fatalf("install policy tools: %v", err)
	}
	want := []invocation{
		{name: "go", args: []string{"install", "github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.12.2"}},
		{name: "go", args: []string{"install", "golang.org/x/vuln/cmd/govulncheck@v1.6.0"}},
		{name: "go", args: []string{"install", "github.com/rhysd/actionlint/cmd/actionlint@v1.7.12"}},
		{name: "go", args: []string{"install", "github.com/goreleaser/goreleaser/v2@v2.17.1"}},
		{name: "go", args: []string{"install", "github.com/zricethezav/gitleaks/v8@v8.30.1"}},
		{name: "go", args: []string{"install", "github.com/google/osv-scanner/v2/cmd/osv-scanner@v2.3.8"}},
		{
			name: "cargo",
			args: []string{
				"install",
				"typos-cli",
				"--version",
				"1.48.0",
				"--locked",
			},
		},
		{
			name: "cargo",
			args: []string{
				"install",
				"taplo-cli",
				"--version",
				"0.10.0",
				"--locked",
			},
		},
		{name: "python", args: []string{"-m", "pip", "install", "uv==0.12.0"}},
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("policy install commands = %#v, want %#v", calls, want)
	}
}

func TestCoverageAndReleaseToolsAreExactPinnedInstalls(t *testing.T) {
	t.Parallel()

	var calls []invocation
	app := testApplication(t, func(
		_ string,
		command invocation,
		_ io.Reader,
		_ io.Writer,
		_ io.Writer,
	) error {
		calls = append(calls, command)
		return nil
	})

	if err := app.run([]string{"tools", "coverage"}); err != nil {
		t.Fatalf("install coverage tools: %v", err)
	}
	if err := app.run([]string{"tools", "release"}); err != nil {
		t.Fatalf("install release tools: %v", err)
	}
	want := []invocation{
		{name: "go", args: []string{"install", "github.com/vladopajic/go-test-coverage/v2@v2.19.0"}},
		{name: "go", args: []string{"install", "github.com/goreleaser/goreleaser/v2@v2.17.1"}},
		{name: "go", args: []string{"install", "github.com/anchore/syft/cmd/syft@v1.50.0"}},
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("coverage/release install commands = %#v, want %#v", calls, want)
	}
}

func TestPinnedToolchainFilesStaySynchronized(t *testing.T) {
	t.Parallel()

	root, err := findRepositoryRoot()
	if err != nil {
		t.Fatal(err)
	}
	configured := readMiseConfigVersions(t, filepath.Join(root, "mise.toml"))
	locked := readMiseLockVersions(t, filepath.Join(root, "mise.lock"))
	if !reflect.DeepEqual(configured, locked) {
		t.Fatalf("mise.toml tools and mise.lock tools differ:\nconfigured=%#v\nlocked=%#v", configured, locked)
	}
	if len(configured) != 15 {
		t.Fatalf("pinned tool count = %d, want 15", len(configured))
	}

	devtoolPins := map[string]string{
		"golangci-lint":                        installVersion(t, golangciLintInstall, "@v"),
		"go:golang.org/x/vuln/cmd/govulncheck": installVersion(t, govulncheckInstall, "@v"),
		"actionlint":                           installVersion(t, actionlintInstall, "@v"),
		"goreleaser":                           installVersion(t, goreleaserInstall, "@v"),
		"gitleaks":                             installVersion(t, gitleaksInstall, "@v"),
		"go:github.com/google/osv-scanner/v2/cmd/osv-scanner": installVersion(t, osvScannerInstall, "@v"),
		"syft": installVersion(t, syftInstall, "@v"),
		"go:github.com/vladopajic/go-test-coverage/v2": installVersion(t, coverageInstall, "@v"),
		"typos":      typosVersion,
		"taplo":      taploVersion,
		"uv":         installVersion(t, uvInstall, "=="),
		"pipx:reuse": installVersion(t, reuseInstall, "=="),
	}
	for tool, version := range devtoolPins {
		if configured[tool] != version {
			t.Errorf("devtool pins %s %s, mise.toml pins %q", tool, version, configured[tool])
		}
	}

	goVersion := configured["go"]
	goMod := string(readTestFile(t, filepath.Join(root, "go.mod")))
	if !strings.Contains(goMod, "\ngo "+goVersion+"\n") {
		t.Errorf("go.mod does not pin mise Go version %s", goVersion)
	}
	workflowEntries, err := os.ReadDir(filepath.Join(root, ".github", "workflows"))
	if err != nil {
		t.Fatal(err)
	}
	goVersionPins := 0
	for _, entry := range workflowEntries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".yml" {
			continue
		}
		contents := string(readTestFile(t, filepath.Join(root, ".github", "workflows", entry.Name())))
		for line := range strings.SplitSeq(contents, "\n") {
			line = strings.TrimSpace(line)
			if !strings.HasPrefix(line, "go-version:") {
				continue
			}
			goVersionPins++
			version := strings.Trim(strings.TrimSpace(strings.TrimPrefix(line, "go-version:")), `"'`)
			if version != goVersion {
				t.Errorf("workflow %s pins Go %s, want %s", entry.Name(), version, goVersion)
			}
		}
	}
	if goVersionPins == 0 {
		t.Fatal("no workflow go-version pins found")
	}
}

func TestCIStepsIncludeModuleVerificationButExcludeHeavyTasks(t *testing.T) {
	t.Parallel()

	seen := make(map[string]bool, len(ciSteps))
	for _, step := range ciSteps {
		seen[strings.Join(step, " ")] = true
	}
	if !seen["module-verify"] {
		t.Error("CI steps do not include module-verify")
	}
	for _, heavy := range []string{"race", "release-smoke"} {
		if seen[heavy] {
			t.Errorf("CI steps unexpectedly include %s", heavy)
		}
	}
}

func TestMergeEnvironmentReplacesNamesCaseInsensitively(t *testing.T) {
	t.Parallel()

	got := mergeEnvironment(
		[]string{"Path=old", "KEEP=value"},
		[]environmentSetting{
			{name: "PATH", value: "new"},
			{name: "GOARCH", value: "arm64"},
		},
	)
	want := []string{"PATH=new", "KEEP=value", "GOARCH=arm64"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("merged environment = %#v, want %#v", got, want)
	}
}

func TestUnknownCommandIsUsageError(t *testing.T) {
	t.Parallel()

	app := testApplication(t, nil)
	err := app.run([]string{"unknown"})
	if got := errorExitCode(err); got != 2 {
		t.Fatalf("exit code = %d, want 2 (error: %v)", got, err)
	}
	if !strings.Contains(err.Error(), "unknown command") {
		t.Fatalf("error = %q, want unknown command message", err)
	}
}

func createReleaseFixture(t *testing.T, dist string) []string {
	t.Helper()
	return createReleaseFixtureForVersion(t, dist, "0.0.0-SNAPSHOT-test")
}

const (
	testReleaseCommit        = "0123456789abcdef0123456789abcdef01234567"
	testReleaseDate          = "2026-07-30T16:54:03.4833897+09:00"
	testReleaseVersionOutput = "startclean 0.0.0-SNAPSHOT-test (commit 0123456789abcdef0123456789abcdef01234567, built 2026-07-30T07:54:03Z)"
)

func createReleaseFixtureForVersion(t *testing.T, dist, version string) []string {
	t.Helper()

	if err := os.MkdirAll(dist, 0o750); err != nil {
		t.Fatalf("create release fixture directory: %v", err)
	}
	archives := []string{
		filepath.Join(dist, "startclean_"+version+"_Windows_x86_64.zip"),
		filepath.Join(dist, "startclean_"+version+"_Windows_arm64.zip"),
	}
	var manifest strings.Builder
	for _, archive := range archives {
		writeTestArchive(t, archive, requiredReleaseArchiveFiles)
		archiveDigest, err := fileSHA256(archive)
		if err != nil {
			t.Fatalf("hash release fixture archive: %v", err)
		}
		archiveName := filepath.Base(archive)
		projectVersion := "v" + version
		spdx := archive + ".spdx.json"
		writeTestFile(t, spdx, fmt.Appendf(nil, `{"spdxVersion":"SPDX-2.3","dataLicense":"CC0-1.0","SPDXID":"SPDXRef-DOCUMENT","name":%q,"packages":[{"name":"github.com/P4suta/startclean","SPDXID":"SPDXRef-Package-startclean","versionInfo":%q}]}`, archiveName, projectVersion))
		cycloneDX := archive + ".cdx.json"
		writeTestFile(t, cycloneDX, fmt.Appendf(nil, `{"bomFormat":"CycloneDX","specVersion":"1.7","version":1,"metadata":{"component":{"type":"file","name":%q,"version":%q}},"components":[{"name":"github.com/P4suta/startclean","type":"application","version":%q}]}`, archiveName, "sha256:"+archiveDigest, projectVersion))
		for _, path := range []string{archive, spdx, cycloneDX} {
			digest, err := fileSHA256(path)
			if err != nil {
				t.Fatalf("hash release fixture: %v", err)
			}
			manifest.WriteString(digest)
			manifest.WriteString("  ")
			manifest.WriteString(filepath.Base(path))
			manifest.WriteByte('\n')
		}
	}
	writeTestFile(t, filepath.Join(dist, "checksums.txt"), []byte(manifest.String()))
	writeTestFile(t, filepath.Join(dist, "metadata.json"), fmt.Appendf(nil,
		`{"project_name":"startclean","version":%q,"commit":%q,"date":%q}`,
		version,
		testReleaseCommit,
		testReleaseDate,
	))
	return archives
}

func createReleasePublishFixture(t *testing.T, root, tag string) []gitHubReleaseAsset {
	t.Helper()
	dist := filepath.Join(root, "dist")
	createReleaseFixtureForVersion(t, dist, strings.TrimPrefix(tag, "v"))
	localAssets, err := loadLocalReleaseAssets(dist, tag)
	if err != nil {
		t.Fatalf("load release publish fixture: %v", err)
	}
	if len(localAssets) != 7 {
		t.Fatalf("release publish fixture asset count = %d, want 7", len(localAssets))
	}
	remoteAssets := make([]gitHubReleaseAsset, 0, len(localAssets))
	for _, asset := range localAssets {
		remoteAssets = append(remoteAssets, gitHubReleaseAsset{
			Name:   asset.Name,
			State:  "uploaded",
			Size:   asset.Size,
			Digest: asset.Digest,
		})
	}
	return remoteAssets
}

func writeTestArchive(t *testing.T, path string, files []string) {
	t.Helper()
	machine := uint16(pe.IMAGE_FILE_MACHINE_AMD64)
	if strings.HasSuffix(path, "_Windows_arm64.zip") {
		machine = pe.IMAGE_FILE_MACHINE_ARM64
	}
	writeTestArchiveMachine(t, path, files, machine)
}

func writeTestArchiveMachine(t *testing.T, path string, files []string, machine uint16) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("create test archive directory: %v", err)
	}
	archiveFile, err := os.Create(path) //nolint:gosec // Tests create their explicit temporary fixture.
	if err != nil {
		t.Fatalf("create test archive: %v", err)
	}
	archive := zip.NewWriter(archiveFile)
	defer func() {
		_ = archive.Close()
		_ = archiveFile.Close()
	}()
	for _, name := range files {
		entry, createErr := archive.Create(name)
		if createErr != nil {
			t.Fatalf("create archive member %s: %v", name, createErr)
		}
		contents := []byte("fixture\n")
		if name == "startclean.exe" {
			contents = minimalTestPE(machine)
		}
		if _, writeErr := entry.Write(contents); writeErr != nil {
			t.Fatalf("write archive member %s: %v", name, writeErr)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatalf("close test archive: %v", err)
	}
	if err := archiveFile.Close(); err != nil {
		t.Fatalf("close test archive file: %v", err)
	}
}

func minimalTestPE(machine uint16) []byte {
	const peOffset = 0x40
	// debug/pe reads the complete 96-byte DOS header before following e_lfanew.
	contents := make([]byte, 96)
	copy(contents, "MZ")
	binary.LittleEndian.PutUint32(contents[0x3c:], peOffset)
	copy(contents[peOffset:], "PE\x00\x00")
	fileHeader := contents[peOffset+4:]
	binary.LittleEndian.PutUint16(fileHeader, machine)
	binary.LittleEndian.PutUint16(fileHeader[18:], pe.IMAGE_FILE_EXECUTABLE_IMAGE)
	return contents
}

func testApplication(t *testing.T, executor commandExecutor) *application {
	t.Helper()

	if executor == nil {
		executor = func(
			_ string,
			_ invocation,
			_ io.Reader,
			_ io.Writer,
			_ io.Writer,
		) error {
			t.Fatal("unexpected external command")
			return nil
		}
	}
	return &application{
		root:    t.TempDir(),
		stdin:   strings.NewReader(""),
		stdout:  io.Discard,
		stderr:  io.Discard,
		execute: executor,
		lookPath: func(string) (string, error) {
			return "", errors.New("not found")
		},
		getenv: func(string) string { return "" },
	}
}

func environmentValue(settings []environmentSetting, name string) string {
	for _, setting := range settings {
		if setting.name == name {
			return setting.value
		}
	}
	return ""
}

func writeTestFile(t *testing.T, path string, contents []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("create test directory: %v", err)
	}
	if err := os.WriteFile(path, contents, 0o600); err != nil { //nolint:gosec // Tests write only explicit fixture paths.
		t.Fatalf("write test file: %v", err)
	}
}

func readMiseConfigVersions(t *testing.T, path string) map[string]string {
	t.Helper()
	versions := make(map[string]string)
	inTools := false
	for raw := range strings.SplitSeq(string(readTestFile(t, path)), "\n") {
		line := strings.TrimSpace(raw)
		if line == "[tools]" {
			inTools = true
			continue
		}
		if strings.HasPrefix(line, "[") {
			inTools = false
		}
		if !inTools || line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			t.Fatalf("invalid mise.toml tool line %q", line)
		}
		key = strings.Trim(strings.TrimSpace(key), `"`)
		value = strings.TrimSpace(value)
		version := ""
		if strings.HasPrefix(value, `"`) {
			version = strings.Trim(value, `"`)
		} else if _, remainder, found := strings.Cut(value, `version = "`); found {
			version, _, _ = strings.Cut(remainder, `"`)
		}
		if key == "" || version == "" {
			t.Fatalf("mise.toml tool line has no exact key/version: %q", line)
		}
		versions[key] = version
	}
	return versions
}

func readMiseLockVersions(t *testing.T, path string) map[string]string {
	t.Helper()
	versions := make(map[string]string)
	current := ""
	for raw := range strings.SplitSeq(string(readTestFile(t, path)), "\n") {
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(line, "[[tools.") && strings.HasSuffix(line, "]]") {
			current = strings.TrimSuffix(strings.TrimPrefix(line, "[[tools."), "]]")
			current = strings.Trim(current, `"`)
			continue
		}
		if current == "" || !strings.HasPrefix(line, "version =") {
			continue
		}
		version := strings.Trim(strings.TrimSpace(strings.TrimPrefix(line, "version =")), `"`)
		if version == "" {
			t.Fatalf("mise.lock has empty version for %s", current)
		}
		versions[current] = version
		current = ""
	}
	return versions
}

func installVersion(t *testing.T, value, separator string) string {
	t.Helper()
	_, version, ok := strings.Cut(value, separator)
	if !ok || version == "" {
		t.Fatalf("install pin %q has no separator %q", value, separator)
	}
	return version
}

func readTestFile(t *testing.T, path string) []byte {
	t.Helper()
	contents, err := os.ReadFile(path) //nolint:gosec // Tests read their explicit temporary file.
	if err != nil {
		t.Fatalf("read test file: %v", err)
	}
	return contents
}
