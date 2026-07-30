// SPDX-FileCopyrightText: 2026 startclean contributors <https://github.com/P4suta/startclean/graphs/contributors>
// SPDX-License-Identifier: MIT OR Apache-2.0

// Command devtool provides the repository's portable development tasks without
// relying on a shell.
package main

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"debug/pe"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"go/format"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
)

var generatedCompletionFiles = []string{
	"completions/_startclean",
	"completions/startclean.bash",
	"completions/startclean.fish",
	"completions/startclean.ps1",
}

const (
	projectModulePath    = "github.com/P4suta/startclean"
	maxReleaseBinarySize = 64 << 20

	golangciLintInstall = "github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.12.2"
	govulncheckInstall  = "golang.org/x/vuln/cmd/govulncheck@v1.6.0"
	actionlintInstall   = "github.com/rhysd/actionlint/cmd/actionlint@v1.7.12"
	goreleaserInstall   = "github.com/goreleaser/goreleaser/v2@v2.17.1"
	gitleaksInstall     = "github.com/zricethezav/gitleaks/v8@v8.30.1"
	osvScannerInstall   = "github.com/google/osv-scanner/v2/cmd/osv-scanner@v2.3.8"
	syftInstall         = "github.com/anchore/syft/cmd/syft@v1.50.0"
	coverageInstall     = "github.com/vladopajic/go-test-coverage/v2@v2.19.0"
	typosVersion        = "1.48.0"
	taploVersion        = "0.10.0"
	uvInstall           = "uv==0.12.0"
	reuseInstall        = "reuse[charset-normalizer]==6.2.0"
)

var requiredReleaseArchiveFiles = []string{
	"startclean.exe",
	"README.md",
	"LICENSES/Apache-2.0.txt",
	"LICENSES/MIT.txt",
	"completions/_startclean",
	"completions/startclean.bash",
	"completions/startclean.fish",
	"completions/startclean.ps1",
}

var releaseArchiveSuffixes = []string{
	"_Windows_x86_64.zip",
	"_Windows_arm64.zip",
}

var releaseArchiveMachines = map[string]uint16{
	"_Windows_x86_64.zip": pe.IMAGE_FILE_MACHINE_AMD64,
	"_Windows_arm64.zip":  pe.IMAGE_FILE_MACHINE_ARM64,
}

var windowsRaceCompilerCandidates = []string{
	`C:\ProgramData\chocolatey\lib\mingw\tools\install\mingw64\bin\gcc.exe`,
	`C:\mingw64\bin\gcc.exe`,
	`C:\msys64\mingw64\bin\gcc.exe`,
}

var ciSteps = [][]string{
	{"format", "--check"},
	{"module-verify"},
	{"generate", "--check"},
	{"lint"},
	{"vet"},
	{"test"},
	{"integration"},
	{"stress"},
	{"coverage"},
	{"security"},
	{"reuse"},
	{"build"},
	{"release-source"},
	{"release-check"},
}

var conventionalCommitPattern = regexp.MustCompile(
	`^(feat|fix|docs|style|refactor|perf|test|build|ci|chore|revert)(\([a-z0-9._-]+\))?!?: .+`,
)

var releaseTagPattern = regexp.MustCompile(
	`^v(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)(?:-(?:0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*)(?:\.(?:0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*))*)?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$`,
)

var releaseRepositoryPattern = regexp.MustCompile(
	`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`,
)

var releaseCommitPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

type environmentSetting struct {
	name  string
	value string
}

type invocation struct {
	name string
	args []string
	env  []environmentSetting
}

type commandExecutor func(
	directory string,
	command invocation,
	stdin io.Reader,
	stdout io.Writer,
	stderr io.Writer,
) error

type application struct {
	root     string
	stdin    io.Reader
	stdout   io.Writer
	stderr   io.Writer
	execute  commandExecutor
	lookPath func(string) (string, error)
	stat     func(string) (fs.FileInfo, error)
	getenv   func(string) string
	http     *http.Client
}

type usageError struct {
	message string
}

func (e *usageError) Error() string {
	return e.message
}

type fileState struct {
	exists bool
	digest [sha256.Size]byte
}

func main() {
	root, err := findRepositoryRoot()
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "devtool:", err)
		os.Exit(1)
	}

	app := application{
		root:     root,
		stdin:    os.Stdin,
		stdout:   os.Stdout,
		stderr:   os.Stderr,
		execute:  executeCommand,
		lookPath: exec.LookPath,
		stat:     os.Stat,
		getenv:   os.Getenv,
		http:     &http.Client{Timeout: 30 * time.Second},
	}
	if err := app.run(os.Args[1:]); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "devtool:", err)
		os.Exit(errorExitCode(err))
	}
}

func (a *application) run(args []string) error {
	if len(args) == 0 {
		a.printUsage(a.stdout)
		return &usageError{message: "a command is required"}
	}

	switch args[0] {
	case "help", "-h", "--help":
		a.printUsage(a.stdout)
		return nil
	case "format":
		return a.runFormat(args[1:])
	case "generate":
		return a.runGenerate(args[1:])
	case "test":
		return a.runNoArgumentTask("test", args[1:], a.runTest)
	case "integration":
		return a.runNoArgumentTask("integration", args[1:], a.runIntegration)
	case "stress":
		return a.runNoArgumentTask("stress", args[1:], a.runStress)
	case "coverage":
		return a.runNoArgumentTask("coverage", args[1:], a.runCoverage)
	case "module-verify":
		return a.runNoArgumentTask("module-verify", args[1:], a.runModuleVerify)
	case "pr-title":
		return a.runNoArgumentTask("pr-title", args[1:], a.runPRTitle)
	case "race":
		return a.runNoArgumentTask("race", args[1:], a.runRace)
	case "fuzz":
		return a.runNoArgumentTask("fuzz", args[1:], a.runFuzz)
	case "vet":
		return a.runNoArgumentTask("vet", args[1:], a.runVet)
	case "build":
		return a.runNoArgumentTask("build", args[1:], a.runBuild)
	case "lint":
		return a.runNoArgumentTask("lint", args[1:], a.runLint)
	case "security":
		return a.runNoArgumentTask("security", args[1:], a.runSecurity)
	case "secrets":
		return a.runNoArgumentTask("secrets", args[1:], a.runSecrets)
	case "reuse":
		return a.runNoArgumentTask("reuse", args[1:], a.runReuse)
	case "release-check":
		return a.runNoArgumentTask("release-check", args[1:], a.runReleaseCheck)
	case "release-preflight":
		return a.runNoArgumentTask("release-preflight", args[1:], a.runReleasePreflight)
	case "release-publish":
		return a.runNoArgumentTask("release-publish", args[1:], a.runReleasePublish)
	case "release-source":
		return a.runNoArgumentTask("release-source", args[1:], a.runReleaseSource)
	case "release-verify":
		return a.runNoArgumentTask("release-verify", args[1:], a.runReleaseVerify)
	case "release-smoke":
		return a.runNoArgumentTask("release-smoke", args[1:], a.runReleaseSmoke)
	case "commit-msg":
		return a.runCommitMessage(args[1:])
	case "tools":
		return a.runTools(args[1:])
	case "ci":
		return a.runNoArgumentTask("ci", args[1:], a.runCI)
	default:
		a.printUsage(a.stderr)
		return &usageError{message: fmt.Sprintf("unknown command %q", args[0])}
	}
}

func (a *application) runFormat(args []string) error {
	check, help, err := parseCheckFlag("format", args)
	if err != nil {
		return err
	}
	if help {
		_, _ = fmt.Fprintln(a.stdout, "Usage: devtool format [--check]")
		return nil
	}

	if check {
		_, _ = fmt.Fprintln(a.stdout, "+ check Go source formatting")
	} else {
		_, _ = fmt.Fprintln(a.stdout, "+ format Go sources")
	}
	changed, err := formatGoFiles(a.root, check)
	if err != nil {
		return fmt.Errorf("format Go sources: %w", err)
	}
	for _, path := range changed {
		_, _ = fmt.Fprintln(a.stdout, path)
	}
	if check && len(changed) != 0 {
		return fmt.Errorf("%d Go file(s) require formatting", len(changed))
	}
	return nil
}

func (a *application) runGenerate(args []string) error {
	check, help, err := parseCheckFlag("generate", args)
	if err != nil {
		return err
	}
	if help {
		_, _ = fmt.Fprintln(a.stdout, "Usage: devtool generate [--check]")
		return nil
	}

	if !check {
		return a.runExternal(invocation{name: "go", args: []string{"generate", "./..."}})
	}

	temporaryRoot, err := os.MkdirTemp("", "startclean-generated-check-")
	if err != nil {
		return fmt.Errorf("create temporary generation directory: %w", err)
	}
	defer func() {
		if removeErr := os.RemoveAll(temporaryRoot); removeErr != nil {
			_, _ = fmt.Fprintf(a.stderr, "warning: remove temporary generation directory: %v\n", removeErr)
		}
	}()

	if err := a.runExternal(invocation{
		name: "go",
		args: []string{
			"run",
			"-buildvcs=false",
			"./cmd/gencompletion",
			"--output",
			filepath.Join(temporaryRoot, "completions"),
		},
	}); err != nil {
		return err
	}

	tracked, err := snapshotFiles(a.root, generatedCompletionFiles)
	if err != nil {
		return fmt.Errorf("read tracked generated completions: %w", err)
	}
	generated, err := snapshotFiles(temporaryRoot, generatedCompletionFiles)
	if err != nil {
		return fmt.Errorf("read temporary generated completions: %w", err)
	}
	for _, path := range generatedCompletionFiles {
		if !generated[path].exists {
			return fmt.Errorf("generator did not produce %s", path)
		}
	}
	changed := changedSnapshots(tracked, generated)
	for _, path := range changed {
		_, _ = fmt.Fprintf(a.stdout, "generated file changed: %s\n", path)
	}
	if len(changed) != 0 {
		return fmt.Errorf("%d generated completion file(s) are out of date", len(changed))
	}
	return nil
}

func (a *application) runTest() error {
	return a.runExternal(invocation{
		name: "go",
		args: []string{
			"test",
			"-buildvcs=false",
			"-shuffle=on",
			"-count=1",
			"./...",
		},
	})
}

func (a *application) runIntegration() error {
	return a.runExternal(invocation{
		name: "go",
		args: []string{
			"test",
			"-buildvcs=false",
			"-count=1",
			"-tags=integration",
			"./internal/platform",
		},
	})
}

func (a *application) runStress() error {
	return a.runExternal(invocation{
		name: "go",
		args: []string{
			"test",
			"-buildvcs=false",
			"-count=10",
			"-tags=integration",
			"./internal/platform",
		},
	})
}

func (a *application) runCoverage() error {
	coverageRuns := []invocation{
		{
			name: "go",
			args: []string{
				"test", "-buildvcs=false", "-count=1", "-covermode", "atomic",
				"-coverprofile", "coverage.out", "./...",
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
	}
	if err := a.runExternalSequence(coverageRuns); err != nil {
		return err
	}
	return a.runExternalSequence([]invocation{
		{name: "go", args: []string{"tool", "cover", "-func", "coverage.out"}},
		{name: "go-test-coverage", args: []string{"--config", ".testcoverage.yml"}},
		{name: "go", args: []string{"tool", "cover", "-func", "coverage-core.out"}},
		{
			name: "go-test-coverage",
			args: []string{"--profile", "coverage-core.out", "--threshold-total", "80"},
		},
		{name: "go", args: []string{"tool", "cover", "-func", "coverage-platform.out"}},
		{
			name: "go-test-coverage",
			args: []string{"--profile", "coverage-platform.out", "--threshold-total", "65"},
		},
	})
}

func (a *application) runModuleVerify() error {
	return a.runExternalSequence([]invocation{
		{name: "go", args: []string{"mod", "verify"}},
		{name: "go", args: []string{"mod", "tidy", "-diff"}},
	})
}

func (a *application) runPRTitle() error {
	title := a.environment("STARTCLEAN_PR_TITLE")
	if title == "" || title != strings.TrimSpace(title) || strings.ContainsAny(title, "\r\n") ||
		!conventionalCommitPattern.MatchString(title) {
		return errors.New("pull request title must use a single-line Conventional Commit subject")
	}
	return nil
}

func (a *application) runRace() error {
	environment := []environmentSetting{{name: "CGO_ENABLED", value: "1"}}
	if runtime.GOOS == "windows" && a.lookPath != nil && a.stat != nil {
		if _, err := a.lookPath("gcc"); err != nil {
			for _, compiler := range windowsRaceCompilerCandidates {
				info, statErr := a.stat(compiler)
				if statErr != nil || !info.Mode().IsRegular() {
					continue
				}
				_, _ = fmt.Fprintf(a.stdout, "+ use Windows race compiler %s\n", compiler)
				environment = append(environment,
					environmentSetting{name: "CC", value: compiler},
					environmentSetting{
						name:  "PATH",
						value: filepath.Dir(compiler) + string(os.PathListSeparator) + a.environment("PATH"),
					},
				)
				break
			}
		}
	}
	return a.runExternal(invocation{
		name: "go",
		args: []string{
			"test",
			"-buildvcs=false",
			"-race",
			"-shuffle=on",
			"-count=1",
			"./...",
		},
		env: environment,
	})
}

func (a *application) runFuzz() error {
	commands := make([]invocation, 0, 2)
	for _, target := range []string{
		"FuzzInsideRootContainment",
		"FuzzClassifierOnlyMarksDefinitivelyMissingFixedTargetsStale",
	} {
		commands = append(commands, invocation{
			name: "go",
			args: []string{
				"test",
				"-buildvcs=false",
				"-run=^$",
				"-fuzz=^" + target + "$",
				"-fuzztime=10s",
				"./internal/core",
			},
		})
	}
	return a.runExternalSequence(commands)
}

func (a *application) runVet() error {
	return a.runExternal(invocation{
		name: "go",
		args: []string{"vet", "-buildvcs=false", "./..."},
	})
}

func (a *application) runBuild() error {
	outputDirectory := filepath.Join(a.root, "dist")
	if err := os.MkdirAll(outputDirectory, 0o750); err != nil {
		return fmt.Errorf("create build output directory: %w", err)
	}

	builds := []struct {
		architecture string
		filename     string
	}{
		{architecture: "amd64", filename: "startclean-windows-x64.exe"},
		{architecture: "arm64", filename: "startclean-windows-arm64.exe"},
	}
	for _, build := range builds {
		if err := a.runExternal(invocation{
			name: "go",
			args: []string{
				"build",
				"-buildvcs=false",
				"-trimpath",
				"-o",
				filepath.Join("dist", build.filename),
				"./cmd/startclean",
			},
			env: []environmentSetting{
				{name: "CGO_ENABLED", value: "0"},
				{name: "GOOS", value: "windows"},
				{name: "GOARCH", value: build.architecture},
			},
		}); err != nil {
			return err
		}
	}
	return nil
}

func (a *application) runLint() error {
	commands := []invocation{
		{name: "golangci-lint", args: []string{"run", "./..."}},
		{name: "actionlint"},
		{name: "typos"},
		{name: "taplo", args: []string{"format", "--check"}},
	}
	return a.runExternalSequence(commands)
}

func (a *application) runSecurity() error {
	commands := []invocation{
		{name: "govulncheck", args: []string{"./..."}},
		{name: "osv-scanner", args: []string{"scan", "source", "--recursive", "."}},
		{name: "gitleaks", args: []string{"git", ".", "--redact"}},
	}
	return a.runExternalSequence(commands)
}

func (a *application) runSecrets() error {
	return a.runExternal(invocation{
		name: "gitleaks",
		args: []string{"git", ".", "--pre-commit", "--staged", "--redact"},
	})
}

func (a *application) runReuse() error {
	if a.lookPath != nil {
		if _, err := a.lookPath("reuse"); err == nil {
			return a.runExternal(invocation{name: "reuse", args: []string{"lint"}})
		}
	}
	return a.runExternal(invocation{
		name: "uvx",
		args: []string{"--from", reuseInstall, "reuse", "lint"},
	})
}

func (a *application) runReleaseCheck() error {
	return a.runExternal(invocation{name: "goreleaser", args: []string{"check"}})
}

func (a *application) runReleasePreflight() error {
	tag, err := a.releaseTag()
	if err != nil {
		return err
	}
	tagCommit := "refs/tags/" + tag + "^{commit}"
	if err := a.runExternal(invocation{
		name: "git",
		args: []string{"merge-base", "--is-ancestor", tagCommit, "origin/main"},
	}); err != nil {
		return fmt.Errorf("release tag %s must point to a commit on origin/main: %w", tag, err)
	}
	return nil
}

func (a *application) runReleasePublish() error {
	tag, err := a.releaseTag()
	if err != nil {
		return err
	}
	repository := a.environment("GITHUB_REPOSITORY")
	if !releaseRepositoryPattern.MatchString(repository) {
		return fmt.Errorf("release repository %q is not a valid owner/name", repository)
	}
	owner, name, _ := strings.Cut(repository, "/")
	if owner == "." || owner == ".." || name == "." || name == ".." {
		return fmt.Errorf("release repository %q is not a valid owner/name", repository)
	}
	token := a.environment("GITHUB_TOKEN")
	if token == "" {
		return errors.New("release publish requires GITHUB_TOKEN")
	}
	apiBase, err := releaseAPIBase(a.environment("GITHUB_API_URL"))
	if err != nil {
		return err
	}
	repositoryEndpoint := apiBase + "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(name)
	lookupEndpoint := repositoryEndpoint + "/releases/tags/" + url.PathEscape(tag)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var release gitHubRelease
	if err := a.githubJSON(ctx, http.MethodGet, lookupEndpoint, token, nil, &release); err != nil {
		return fmt.Errorf("find draft release for %s: %w", tag, err)
	}
	if release.ID <= 0 || release.TagName != tag {
		return fmt.Errorf("GitHub returned an invalid release for tag %s", tag)
	}
	if !release.Draft {
		_, _ = fmt.Fprintf(a.stdout, "Release %s is already published.\n", tag)
		return nil
	}
	expectedAssets, err := loadLocalReleaseAssets(filepath.Join(a.root, "dist"), tag)
	if err != nil {
		return fmt.Errorf("load local release assets: %w", err)
	}
	if err := validateDraftReleaseAssets(release.Assets, expectedAssets); err != nil {
		return fmt.Errorf("validate GitHub draft release assets: %w", err)
	}

	publishEndpoint := repositoryEndpoint + "/releases/" + strconv.FormatInt(release.ID, 10)
	var published gitHubRelease
	if err := a.githubJSON(
		ctx,
		http.MethodPatch,
		publishEndpoint,
		token,
		map[string]bool{"draft": false},
		&published,
	); err != nil {
		return fmt.Errorf("publish draft release %s: %w", tag, err)
	}
	if published.ID != release.ID || published.TagName != tag || published.Draft {
		return fmt.Errorf("GitHub did not confirm publication of release %s", tag)
	}
	return nil
}

type gitHubRelease struct {
	ID      int64                `json:"id"`
	TagName string               `json:"tag_name"`
	Draft   bool                 `json:"draft"`
	Assets  []gitHubReleaseAsset `json:"assets"`
}

type gitHubReleaseAsset struct {
	Name   string `json:"name"`
	State  string `json:"state"`
	Size   int64  `json:"size"`
	Digest string `json:"digest"`
}

type localReleaseAsset struct {
	Name   string
	Size   int64
	Digest string
}

func loadLocalReleaseAssets(distDirectory, tag string) ([]localReleaseAsset, error) {
	version := strings.TrimPrefix(tag, "v")
	names := make([]string, 0, 1+3*len(releaseArchiveSuffixes))
	for _, suffix := range releaseArchiveSuffixes {
		archiveName := "startclean_" + version + suffix
		names = append(names, archiveName, archiveName+".spdx.json", archiveName+".cdx.json")
	}
	names = append(names, "checksums.txt")

	assets := make([]localReleaseAsset, 0, len(names))
	for _, name := range names {
		path := filepath.Join(distDirectory, name)
		info, err := os.Stat(path)
		if err != nil {
			return nil, fmt.Errorf("stat %s: %w", name, err)
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("%s is not a regular file", name)
		}
		digest, err := fileSHA256(path)
		if err != nil {
			return nil, err
		}
		assets = append(assets, localReleaseAsset{
			Name:   name,
			Size:   info.Size(),
			Digest: "sha256:" + digest,
		})
	}
	return assets, nil
}

func validateDraftReleaseAssets(actual []gitHubReleaseAsset, expected []localReleaseAsset) error {
	expectedByName := make(map[string]localReleaseAsset, len(expected))
	for _, asset := range expected {
		expectedByName[asset.Name] = asset
	}

	seen := make(map[string]struct{}, len(actual))
	for _, asset := range actual {
		if _, duplicate := seen[asset.Name]; duplicate {
			return fmt.Errorf("draft release contains duplicate asset %s", asset.Name)
		}
		seen[asset.Name] = struct{}{}

		want, ok := expectedByName[asset.Name]
		if !ok {
			return fmt.Errorf("draft release contains unexpected asset %s", asset.Name)
		}
		if asset.State != "uploaded" {
			return fmt.Errorf("draft release asset %s has state %q, want %q", asset.Name, asset.State, "uploaded")
		}
		if asset.Size != want.Size {
			return fmt.Errorf(
				"draft release asset %s has size %d, want %d",
				asset.Name,
				asset.Size,
				want.Size,
			)
		}
		if !strings.EqualFold(asset.Digest, want.Digest) {
			return fmt.Errorf(
				"draft release asset %s has digest %q, want %q",
				asset.Name,
				asset.Digest,
				want.Digest,
			)
		}
	}

	for _, asset := range expected {
		if _, ok := seen[asset.Name]; !ok {
			return fmt.Errorf("draft release is missing asset %s", asset.Name)
		}
	}
	return nil
}

func (a *application) githubJSON(
	ctx context.Context,
	method, endpoint, token string,
	requestValue, responseValue any,
) error {
	var requestBody io.Reader
	if requestValue != nil {
		encoded, err := json.Marshal(requestValue)
		if err != nil {
			return fmt.Errorf("encode GitHub API request: %w", err)
		}
		requestBody = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint, requestBody)
	if err != nil {
		return fmt.Errorf("create GitHub API request: %w", err)
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("User-Agent", "startclean-devtool")
	request.Header.Set("X-GitHub-Api-Version", "2026-03-10")
	if requestValue != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	client := a.http
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("call GitHub API: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	const responseLimit = 1 << 20
	contents, err := io.ReadAll(io.LimitReader(response.Body, responseLimit+1))
	if err != nil {
		return fmt.Errorf("read GitHub API response: %w", err)
	}
	if len(contents) > responseLimit {
		return errors.New("GitHub API response exceeds 1 MiB")
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf(
			"GitHub API returned %s: %s",
			response.Status,
			strings.TrimSpace(string(contents)),
		)
	}
	if responseValue != nil {
		if err := json.Unmarshal(contents, responseValue); err != nil {
			return fmt.Errorf("decode GitHub API response: %w", err)
		}
	}
	return nil
}

func releaseAPIBase(value string) (string, error) {
	if value == "" {
		value = "https://api.github.com"
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("invalid GITHUB_API_URL %q", value)
	}
	localHTTP := parsed.Scheme == "http" &&
		(parsed.Hostname() == "127.0.0.1" || parsed.Hostname() == "localhost")
	if parsed.Scheme != "https" && !localHTTP {
		return "", fmt.Errorf("GITHUB_API_URL must use HTTPS, got %q", value)
	}
	return strings.TrimRight(parsed.String(), "/"), nil
}

func (a *application) releaseTag() (string, error) {
	if refType := a.environment("GITHUB_REF_TYPE"); refType != "tag" {
		return "", fmt.Errorf("release requires GITHUB_REF_TYPE=tag, got %q", refType)
	}
	tag := a.environment("GITHUB_REF_NAME")
	if !releaseTagPattern.MatchString(tag) {
		return "", fmt.Errorf("release tag %q is not a supported vMAJOR.MINOR.PATCH tag", tag)
	}
	return tag, nil
}

func (a *application) environment(name string) string {
	if a.getenv == nil {
		return os.Getenv(name)
	}
	return a.getenv(name)
}

func (a *application) runReleaseSource() error {
	if err := a.runModuleVerify(); err != nil {
		return err
	}
	return a.runGenerate([]string{"--check"})
}

func (a *application) runReleaseSmoke() error {
	if err := a.runExternal(invocation{
		name: "goreleaser",
		args: []string{"release", "--snapshot", "--clean"},
	}); err != nil {
		return err
	}
	return a.runReleaseVerify()
}

func (a *application) runReleaseVerify() error {
	distDirectory := filepath.Join(a.root, "dist")
	if err := verifyReleaseArtifacts(distDirectory); err != nil {
		return fmt.Errorf("verify release artifacts: %w", err)
	}
	if err := a.validateReleaseSBOMs(distDirectory); err != nil {
		return fmt.Errorf("validate release SBOMs with Syft: %w", err)
	}
	if err := a.verifyReleaseRuntime(distDirectory); err != nil {
		return fmt.Errorf("verify release runtime metadata: %w", err)
	}
	return nil
}

func (a *application) verifyReleaseRuntime(distDirectory string) error {
	metadataPath := filepath.Join(distDirectory, "metadata.json")
	contents, err := os.ReadFile(metadataPath) //nolint:gosec // Fixed GoReleaser metadata path.
	if err != nil {
		return fmt.Errorf("read metadata.json: %w", err)
	}
	var metadata struct {
		ProjectName string `json:"project_name"`
		Version     string `json:"version"`
		Commit      string `json:"commit"`
		Date        string `json:"date"`
	}
	if err := json.Unmarshal(contents, &metadata); err != nil {
		return fmt.Errorf("parse metadata.json: %w", err)
	}
	if metadata.ProjectName != "startclean" || metadata.Version == "" ||
		metadata.Version == "dev" || metadata.Version == "unknown" ||
		!releaseCommitPattern.MatchString(metadata.Commit) {
		return errors.New("metadata.json does not contain canonical release identity")
	}
	builtAt, err := time.Parse(time.RFC3339Nano, metadata.Date)
	if err != nil {
		return fmt.Errorf("metadata.json has invalid build date %q: %w", metadata.Date, err)
	}
	expected := fmt.Sprintf(
		"startclean %s (commit %s, built %s)",
		metadata.Version,
		metadata.Commit,
		builtAt.UTC().Format(time.RFC3339),
	)

	matches, err := filepath.Glob(filepath.Join(distDirectory, "*"+releaseArchiveSuffixes[0]))
	if err != nil {
		return fmt.Errorf("find x64 release archive: %w", err)
	}
	if len(matches) != 1 {
		return fmt.Errorf("x64 release archive count is %d, want 1", len(matches))
	}
	temporaryDirectory, err := os.MkdirTemp(a.root, ".release-runtime-")
	if err != nil {
		return fmt.Errorf("create runtime verification directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(temporaryDirectory) }()
	executablePath := filepath.Join(temporaryDirectory, "startclean.exe")
	if err := extractReleaseExecutable(matches[0], executablePath); err != nil {
		return err
	}

	var stdout, stderr bytes.Buffer
	_, _ = fmt.Fprintln(a.stdout, "+ execute x64 release startclean.exe version")
	if err := a.execute(
		a.root,
		invocation{name: executablePath, args: []string{"version"}},
		strings.NewReader(""),
		&stdout,
		&stderr,
	); err != nil {
		return fmt.Errorf("execute x64 release binary: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	if actual := strings.TrimSpace(stdout.String()); actual != expected {
		return fmt.Errorf("x64 release version output is %q, want %q", actual, expected)
	}
	return nil
}

func extractReleaseExecutable(archivePath, destination string) error {
	archive, err := zip.OpenReader(archivePath)
	if err != nil {
		return fmt.Errorf("open x64 release archive: %w", err)
	}
	defer func() { _ = archive.Close() }()

	for _, file := range archive.File {
		if filepath.ToSlash(file.Name) != "startclean.exe" {
			continue
		}
		stream, err := file.Open()
		if err != nil {
			return fmt.Errorf("open x64 release executable: %w", err)
		}
		output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o700) //nolint:gosec // Destination is a fresh private temporary directory.
		if err != nil {
			_ = stream.Close()
			return fmt.Errorf("create extracted x64 executable: %w", err)
		}
		written, copyErr := io.Copy(output, io.LimitReader(stream, maxReleaseBinarySize+1))
		outputCloseErr := output.Close()
		streamCloseErr := stream.Close()
		if copyErr != nil {
			return fmt.Errorf("extract x64 release executable: %w", copyErr)
		}
		if outputCloseErr != nil || streamCloseErr != nil {
			return errors.New("close extracted x64 release executable")
		}
		if written == 0 || written > maxReleaseBinarySize {
			return fmt.Errorf("extracted x64 release executable has invalid size %d", written)
		}
		return nil
	}
	return errors.New("x64 release archive has no startclean.exe")
}

func (a *application) validateReleaseSBOMs(distDirectory string) error {
	var documents []string
	for _, pattern := range []string{"*.spdx.json", "*.cdx.json"} {
		matches, err := filepath.Glob(filepath.Join(distDirectory, pattern))
		if err != nil {
			return fmt.Errorf("find %s documents: %w", pattern, err)
		}
		documents = append(documents, matches...)
	}
	sort.Strings(documents)
	if len(documents) != 2*len(releaseArchiveSuffixes) {
		return fmt.Errorf("SBOM document count is %d, want %d", len(documents), 2*len(releaseArchiveSuffixes))
	}
	validationDirectory, err := os.MkdirTemp(a.root, ".sbom-validation-")
	if err != nil {
		return fmt.Errorf("create temporary validation directory: %w", err)
	}
	defer func() {
		_ = os.RemoveAll(validationDirectory)
	}()
	for index, document := range documents {
		converted := filepath.Join(validationDirectory, fmt.Sprintf("%d.syft.json", index))
		if err := a.runExternal(invocation{
			name: "syft",
			args: []string{
				"convert",
				document,
				"--output",
				"syft-json=" + converted,
				"--quiet",
			},
		}); err != nil {
			return fmt.Errorf("parse %s: %w", filepath.Base(document), err)
		}
	}
	return nil
}

func (a *application) runCommitMessage(args []string) error {
	if isSingleHelpArgument(args) {
		_, _ = fmt.Fprintln(a.stdout, "Usage: devtool commit-msg <path>")
		return nil
	}
	if len(args) != 1 {
		return &usageError{message: "commit-msg requires exactly one message file path"}
	}

	path := args[0]
	if !filepath.IsAbs(path) {
		path = filepath.Join(a.root, path)
	}
	// The hook intentionally reads the exact commit-message path supplied by Git.
	message, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		return fmt.Errorf("read commit message: %w", err)
	}
	if !conventionalCommitPattern.Match(message) {
		return errors.New("commit message must use Conventional Commits")
	}
	return nil
}

func (a *application) runTools(args []string) error {
	if len(args) == 0 || isSingleHelpArgument(args) {
		_, _ = fmt.Fprintln(
			a.stdout,
			"Usage: devtool tools coverage|policy|policy-go|policy-rust|policy-python|release",
		)
		if len(args) == 0 {
			return &usageError{
				message: "tools requires coverage, policy, policy-go, policy-rust, policy-python, or release",
			}
		}
		return nil
	}
	if len(args) != 1 {
		return &usageError{
			message: "tools accepts exactly one of coverage, policy, policy-go, policy-rust, policy-python, or release",
		}
	}

	switch args[0] {
	case "coverage":
		return a.installCoverageTools()
	case "policy":
		return a.installPolicyTools()
	case "policy-go":
		return a.installPolicyGoTools()
	case "policy-rust":
		return a.installPolicyRustTools()
	case "policy-python":
		return a.installPolicyPythonTools()
	case "release":
		return a.installReleaseTools()
	default:
		return &usageError{message: fmt.Sprintf("unknown tools set %q", args[0])}
	}
}

func (a *application) installPolicyTools() error {
	if err := a.installPolicyGoTools(); err != nil {
		return err
	}
	if err := a.installPolicyRustTools(); err != nil {
		return err
	}
	return a.installPolicyPythonTools()
}

func (a *application) installPolicyGoTools() error {
	return a.runExternalSequence([]invocation{
		{name: "go", args: []string{"install", golangciLintInstall}},
		{name: "go", args: []string{"install", govulncheckInstall}},
		{name: "go", args: []string{"install", actionlintInstall}},
		{name: "go", args: []string{"install", goreleaserInstall}},
		{name: "go", args: []string{"install", gitleaksInstall}},
		{name: "go", args: []string{"install", osvScannerInstall}},
	})
}

func (a *application) installPolicyRustTools() error {
	return a.runExternalSequence([]invocation{
		{
			name: "cargo",
			args: []string{
				"install",
				"typos-cli",
				"--version",
				typosVersion,
				"--locked",
			},
		},
		{
			name: "cargo",
			args: []string{
				"install",
				"taplo-cli",
				"--version",
				taploVersion,
				"--locked",
			},
		},
	})
}

func (a *application) installPolicyPythonTools() error {
	return a.runExternal(invocation{
		name: "python",
		args: []string{"-m", "pip", "install", uvInstall},
	})
}

func (a *application) installCoverageTools() error {
	return a.runExternal(invocation{
		name: "go",
		args: []string{"install", coverageInstall},
	})
}

func (a *application) installReleaseTools() error {
	commands := []invocation{
		{name: "go", args: []string{"install", goreleaserInstall}},
		{name: "go", args: []string{"install", syftInstall}},
	}
	return a.runExternalSequence(commands)
}

func (a *application) runCI() error {
	for _, step := range ciSteps {
		_, _ = fmt.Fprintf(a.stdout, "\n==> devtool %s\n", strings.Join(step, " "))
		if err := a.run(step); err != nil {
			return fmt.Errorf("CI step %q failed: %w", strings.Join(step, " "), err)
		}
	}
	return nil
}

func (a *application) runNoArgumentTask(
	name string,
	args []string,
	task func() error,
) error {
	if isSingleHelpArgument(args) {
		_, _ = fmt.Fprintf(a.stdout, "Usage: devtool %s\n", name)
		return nil
	}
	if len(args) != 0 {
		return &usageError{message: fmt.Sprintf("%s does not accept arguments", name)}
	}
	return task()
}

func isSingleHelpArgument(args []string) bool {
	if len(args) != 1 {
		return false
	}
	argument := args[0]
	return argument == "-h" || argument == "--help"
}

func (a *application) runExternalSequence(commands []invocation) error {
	for _, command := range commands {
		if err := a.runExternal(command); err != nil {
			return err
		}
	}
	return nil
}

func (a *application) runExternal(command invocation) error {
	_, _ = fmt.Fprintln(a.stdout, "+ "+renderInvocation(command))
	if err := a.execute(
		a.root,
		command,
		a.stdin,
		a.stdout,
		a.stderr,
	); err != nil {
		return fmt.Errorf("%s failed: %w", command.name, err)
	}
	return nil
}

func (a *application) printUsage(writer io.Writer) {
	_, _ = fmt.Fprintln(writer, `Usage: devtool <command> [options]

Commands:
  format [--check]    Format Go files or verify their formatting
  generate [--check]  Generate files or verify generated completions
  test                Run unit tests
  integration         Run Windows Shell Link integration tests
  stress              Repeat Windows COM and guarded-deletion tests ten times
  coverage            Run tests with coverage and print the function report
  module-verify       Verify downloaded modules and a tidy module graph
  pr-title            Validate STARTCLEAN_PR_TITLE as a Conventional Commit subject
  race                Run shuffled tests with the race detector
  fuzz                Fuzz path-containment and conservative-classification invariants
  vet                 Run go vet
  build               Build Windows x64 and arm64 executables
  lint                Run golangci-lint, actionlint, typos, and Taplo
  security            Run govulncheck, OSV-Scanner, and gitleaks
  secrets             Check staged changes with gitleaks
  reuse               Check REUSE compliance
  release-check       Validate the GoReleaser configuration
  release-preflight   Require a SemVer tag whose commit is on origin/main
  release-publish     Publish the fully attested draft GitHub release
  release-source      Verify tidy modules and generated release inputs
  release-verify      Verify built ZIPs, PE architectures, SBOMs, and checksums
  release-smoke       Build and inspect a clean snapshot release
  commit-msg <path>   Validate a Conventional Commit message file
  tools coverage      Install the pinned coverage threshold tool
  tools policy        Install pinned policy and CI tools
  tools policy-go     Install pinned Go policy and CI tools
  tools policy-rust   Install pinned Rust policy and CI tools
  tools policy-python Install the pinned Python policy bootstrap tool
  tools release       Install pinned release tools
  ci                  Run the standard local quality gate`)
}

func parseCheckFlag(command string, args []string) (check bool, help bool, err error) {
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.BoolVar(&check, "check", false, "check without accepting changes")
	if parseErr := flags.Parse(args); parseErr != nil {
		if errors.Is(parseErr, flag.ErrHelp) {
			return false, true, nil
		}
		return false, false, &usageError{message: parseErr.Error()}
	}
	if flags.NArg() != 0 {
		return false, false, &usageError{
			message: fmt.Sprintf("%s does not accept positional arguments", command),
		}
	}
	return check, false, nil
}

func formatGoFiles(root string, check bool) ([]string, error) {
	var changed []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if path != root && (entry.Name() == ".git" || entry.Name() == "dist") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.EqualFold(filepath.Ext(entry.Name()), ".go") {
			return nil
		}

		// WalkDir guarantees that path is an entry beneath the repository root.
		source, err := os.ReadFile(path) //nolint:gosec
		if err != nil {
			return err
		}
		formatted, err := format.Source(source)
		if err != nil {
			relative, relativeErr := filepath.Rel(root, path)
			if relativeErr != nil {
				relative = path
			}
			return fmt.Errorf("%s: %w", filepath.ToSlash(relative), err)
		}
		if bytes.Equal(source, formatted) {
			return nil
		}

		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		changed = append(changed, filepath.ToSlash(relative))
		if check {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		// WalkDir guarantees that path is an entry beneath the repository root.
		return os.WriteFile(path, formatted, info.Mode().Perm()) //nolint:gosec
	})
	return changed, err
}

func snapshotFiles(root string, paths []string) (map[string]fileState, error) {
	states := make(map[string]fileState, len(paths))
	for _, path := range paths {
		// paths is the fixed generatedCompletionFiles allowlist at runtime.
		contents, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path))) //nolint:gosec
		if errors.Is(err, fs.ErrNotExist) {
			states[path] = fileState{}
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		states[path] = fileState{
			exists: true,
			digest: sha256.Sum256(contents),
		}
	}
	return states, nil
}

func changedSnapshots(before, after map[string]fileState) []string {
	pathSet := make(map[string]struct{}, len(before)+len(after))
	for path := range before {
		pathSet[path] = struct{}{}
	}
	for path := range after {
		pathSet[path] = struct{}{}
	}

	changed := make([]string, 0, len(pathSet))
	for path := range pathSet {
		if before[path] != after[path] {
			changed = append(changed, path)
		}
	}
	sort.Strings(changed)
	return changed
}

func verifyReleaseArtifacts(distDirectory string) error {
	entries, err := os.ReadDir(distDirectory)
	if err != nil {
		return fmt.Errorf("read dist directory: %w", err)
	}

	archives := make(map[string]string, len(releaseArchiveSuffixes))
	archiveCount := 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".zip") {
			continue
		}
		archiveCount++
		for _, suffix := range releaseArchiveSuffixes {
			if !strings.HasSuffix(entry.Name(), suffix) {
				continue
			}
			if previous := archives[suffix]; previous != "" {
				return fmt.Errorf(
					"multiple release archives end in %s: %s and %s",
					suffix,
					previous,
					entry.Name(),
				)
			}
			archives[suffix] = entry.Name()
		}
	}
	if archiveCount != len(releaseArchiveSuffixes) {
		return fmt.Errorf(
			"release archive count is %d, want %d",
			archiveCount,
			len(releaseArchiveSuffixes),
		)
	}
	for _, suffix := range releaseArchiveSuffixes {
		if archives[suffix] == "" {
			return fmt.Errorf("release archive ending in %s is missing", suffix)
		}
	}

	checksums, err := readChecksums(filepath.Join(distDirectory, "checksums.txt"))
	if err != nil {
		return err
	}
	if expected := 3 * len(releaseArchiveSuffixes); len(checksums) != expected {
		return fmt.Errorf("checksums.txt contains %d entries, want exactly %d", len(checksums), expected)
	}
	for _, suffix := range releaseArchiveSuffixes {
		archivePath := filepath.Join(distDirectory, archives[suffix])
		if err := verifyArchive(archivePath, releaseArchiveMachines[suffix]); err != nil {
			return err
		}
		archiveName := filepath.Base(archivePath)
		archiveDigest, ok := checksums[archiveName]
		if !ok {
			return fmt.Errorf("checksums.txt has no entry for %s", archiveName)
		}
		if err := verifyFileChecksum(archivePath, archiveDigest); err != nil {
			return err
		}
		spdxPath := archivePath + ".spdx.json"
		spdxProjectVersion, err := verifySPDXSBOM(spdxPath, archiveName)
		if err != nil {
			return err
		}
		cycloneDXPath := archivePath + ".cdx.json"
		cycloneDXProjectVersion, err := verifyCycloneDXSBOM(
			cycloneDXPath,
			archiveName,
			"sha256:"+archiveDigest,
		)
		if err != nil {
			return err
		}
		if spdxProjectVersion != cycloneDXProjectVersion {
			return fmt.Errorf(
				"SBOM project version mismatch for %s: SPDX has %q, CycloneDX has %q",
				archiveName,
				spdxProjectVersion,
				cycloneDXProjectVersion,
			)
		}
		for _, path := range []string{spdxPath, cycloneDXPath} {
			name := filepath.Base(path)
			expected, ok := checksums[name]
			if !ok {
				return fmt.Errorf("checksums.txt has no entry for %s", name)
			}
			if err := verifyFileChecksum(path, expected); err != nil {
				return err
			}
		}
	}
	return nil
}

func readChecksums(path string) (map[string]string, error) {
	contents, err := os.ReadFile(path) //nolint:gosec // The path is the fixed dist/checksums.txt manifest.
	if err != nil {
		return nil, fmt.Errorf("read checksums: %w", err)
	}

	checksums := make(map[string]string)
	for lineNumber, line := range strings.Split(string(contents), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 2 {
			return nil, fmt.Errorf("checksums.txt line %d is malformed", lineNumber+1)
		}
		digest := strings.ToLower(fields[0])
		decoded, decodeErr := hex.DecodeString(digest)
		if decodeErr != nil || len(decoded) != sha256.Size {
			return nil, fmt.Errorf("checksums.txt line %d has an invalid SHA-256", lineNumber+1)
		}
		name := strings.TrimPrefix(fields[1], "*")
		if name == "" {
			return nil, fmt.Errorf("checksums.txt line %d has no filename", lineNumber+1)
		}
		if _, exists := checksums[name]; exists {
			return nil, fmt.Errorf("checksums.txt contains duplicate entry for %s", name)
		}
		checksums[name] = digest
	}
	if len(checksums) == 0 {
		return nil, errors.New("checksums.txt contains no entries")
	}
	return checksums, nil
}

func verifyFileChecksum(path, expected string) error {
	actual, err := fileSHA256(path)
	if err != nil {
		return err
	}
	if !strings.EqualFold(actual, expected) {
		return fmt.Errorf(
			"SHA-256 mismatch for %s: got %s, want %s",
			filepath.Base(path),
			actual,
			expected,
		)
	}
	return nil
}

func fileSHA256(path string) (string, error) {
	file, err := os.Open(path) //nolint:gosec // Callers provide fixed or ReadDir-discovered dist paths.
	if err != nil {
		return "", fmt.Errorf("open %s: %w", filepath.Base(path), err)
	}

	hasher := sha256.New()
	_, copyErr := io.Copy(hasher, file)
	closeErr := file.Close()
	if copyErr != nil {
		return "", fmt.Errorf("hash %s: %w", filepath.Base(path), copyErr)
	}
	if closeErr != nil {
		return "", fmt.Errorf("close %s after hashing: %w", filepath.Base(path), closeErr)
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

func verifyArchive(path string, expectedMachine uint16) error {
	archive, err := zip.OpenReader(path)
	if err != nil {
		return fmt.Errorf("open release archive %s: %w", filepath.Base(path), err)
	}
	defer func() {
		_ = archive.Close()
	}()

	allowed := make(map[string]struct{}, len(requiredReleaseArchiveFiles))
	for _, name := range requiredReleaseArchiveFiles {
		allowed[name] = struct{}{}
	}
	found := make(map[string]bool, len(requiredReleaseArchiveFiles))
	var executable *zip.File
	for _, file := range archive.File {
		name := filepath.ToSlash(file.Name)
		if _, ok := allowed[name]; !ok {
			return fmt.Errorf("release archive %s contains unexpected member %s", filepath.Base(path), name)
		}
		if found[name] {
			return fmt.Errorf("release archive %s contains duplicate member %s", filepath.Base(path), name)
		}
		found[name] = true
		if name == "startclean.exe" {
			executable = file
		}
	}
	for _, required := range requiredReleaseArchiveFiles {
		if !found[required] {
			return fmt.Errorf(
				"release archive %s is missing %s",
				filepath.Base(path),
				required,
			)
		}
	}
	return verifyReleaseExecutable(filepath.Base(path), executable, expectedMachine)
}

func verifyReleaseExecutable(archiveName string, file *zip.File, expectedMachine uint16) error {
	if file == nil {
		return fmt.Errorf("release archive %s has no startclean.exe", archiveName)
	}
	if file.UncompressedSize64 == 0 || file.UncompressedSize64 > maxReleaseBinarySize {
		return fmt.Errorf(
			"release archive %s has invalid executable size %d",
			archiveName,
			file.UncompressedSize64,
		)
	}
	stream, err := file.Open()
	if err != nil {
		return fmt.Errorf("open startclean.exe in %s: %w", archiveName, err)
	}
	contents, readErr := io.ReadAll(io.LimitReader(stream, maxReleaseBinarySize+1))
	closeErr := stream.Close()
	if readErr != nil {
		return fmt.Errorf("read startclean.exe in %s: %w", archiveName, readErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close startclean.exe in %s: %w", archiveName, closeErr)
	}
	if len(contents) > maxReleaseBinarySize {
		return fmt.Errorf("startclean.exe in %s exceeds the verification size limit", archiveName)
	}
	executable, err := pe.NewFile(bytes.NewReader(contents))
	if err != nil {
		return fmt.Errorf("parse startclean.exe in %s as PE: %w", archiveName, err)
	}
	defer func() { _ = executable.Close() }()
	if executable.Machine != expectedMachine {
		return fmt.Errorf(
			"startclean.exe in %s has PE machine 0x%04X, want 0x%04X",
			archiveName,
			executable.Machine,
			expectedMachine,
		)
	}
	if executable.Characteristics&pe.IMAGE_FILE_EXECUTABLE_IMAGE == 0 ||
		executable.Characteristics&pe.IMAGE_FILE_DLL != 0 {
		return fmt.Errorf("startclean.exe in %s is not a Windows executable image", archiveName)
	}
	return nil
}

func verifySPDXSBOM(path, archiveName string) (string, error) {
	contents, err := os.ReadFile(path) //nolint:gosec // The path is derived from a ReadDir-discovered archive.
	if err != nil {
		return "", fmt.Errorf("read SBOM %s: %w", filepath.Base(path), err)
	}
	if len(bytes.TrimSpace(contents)) == 0 {
		return "", fmt.Errorf("SBOM %s is empty", filepath.Base(path))
	}
	var document struct {
		SPDXVersion string `json:"spdxVersion"`
		DataLicense string `json:"dataLicense"`
		SPDXID      string `json:"SPDXID"`
		Name        string `json:"name"`
		Packages    []struct {
			Name        string `json:"name"`
			SPDXID      string `json:"SPDXID"`
			VersionInfo string `json:"versionInfo"`
		} `json:"packages"`
	}
	if err := json.Unmarshal(contents, &document); err != nil {
		return "", fmt.Errorf("parse SBOM %s: %w", filepath.Base(path), err)
	}
	if document.SPDXVersion != "SPDX-2.3" {
		return "", fmt.Errorf("SBOM %s has unsupported spdxVersion %q", filepath.Base(path), document.SPDXVersion)
	}
	if document.DataLicense != "CC0-1.0" || document.SPDXID != "SPDXRef-DOCUMENT" {
		return "", fmt.Errorf("SBOM %s has invalid SPDX document metadata", filepath.Base(path))
	}
	if document.Name != archiveName {
		return "", fmt.Errorf("SBOM %s identifies archive %q, want %q", filepath.Base(path), document.Name, archiveName)
	}
	if len(document.Packages) == 0 {
		return "", fmt.Errorf("SBOM %s has no identified SPDX packages", filepath.Base(path))
	}
	projectVersion := ""
	for _, pkg := range document.Packages {
		if pkg.Name == "" || pkg.SPDXID == "" {
			return "", fmt.Errorf("SBOM %s contains an unidentified SPDX package", filepath.Base(path))
		}
		if pkg.Name != projectModulePath {
			continue
		}
		if pkg.VersionInfo == "" {
			return "", fmt.Errorf("SBOM %s has no version for %s", filepath.Base(path), projectModulePath)
		}
		if projectVersion != "" && projectVersion != pkg.VersionInfo {
			return "", fmt.Errorf("SBOM %s has conflicting versions for %s", filepath.Base(path), projectModulePath)
		}
		projectVersion = pkg.VersionInfo
	}
	if projectVersion == "" {
		return "", fmt.Errorf("SBOM %s does not identify %s", filepath.Base(path), projectModulePath)
	}
	return projectVersion, nil
}

func verifyCycloneDXSBOM(path, archiveName, archiveDigest string) (string, error) {
	contents, err := os.ReadFile(path) //nolint:gosec // The path is derived from a ReadDir-discovered archive.
	if err != nil {
		return "", fmt.Errorf("read SBOM %s: %w", filepath.Base(path), err)
	}
	if len(bytes.TrimSpace(contents)) == 0 {
		return "", fmt.Errorf("SBOM %s is empty", filepath.Base(path))
	}
	var document struct {
		BOMFormat   string `json:"bomFormat"`
		SpecVersion string `json:"specVersion"`
		Version     int    `json:"version"`
		Metadata    struct {
			Component struct {
				Name    string `json:"name"`
				Type    string `json:"type"`
				Version string `json:"version"`
			} `json:"component"`
		} `json:"metadata"`
		Components []struct {
			Name    string `json:"name"`
			Type    string `json:"type"`
			Version string `json:"version"`
		} `json:"components"`
	}
	if err := json.Unmarshal(contents, &document); err != nil {
		return "", fmt.Errorf("parse SBOM %s: %w", filepath.Base(path), err)
	}
	if document.BOMFormat != "CycloneDX" {
		return "", fmt.Errorf("SBOM %s has invalid bomFormat %q", filepath.Base(path), document.BOMFormat)
	}
	if document.SpecVersion != "1.7" || document.Version < 1 {
		return "", fmt.Errorf("SBOM %s has invalid CycloneDX document metadata", filepath.Base(path))
	}
	if document.Metadata.Component.Type != "file" || document.Metadata.Component.Name != archiveName ||
		!strings.EqualFold(document.Metadata.Component.Version, archiveDigest) {
		return "", fmt.Errorf("SBOM %s is not bound to archive %s at %s", filepath.Base(path), archiveName, archiveDigest)
	}
	if len(document.Components) == 0 {
		return "", fmt.Errorf("SBOM %s has no identified CycloneDX components", filepath.Base(path))
	}
	projectVersion := ""
	for _, component := range document.Components {
		if component.Name == "" || component.Type == "" {
			return "", fmt.Errorf("SBOM %s contains an unidentified CycloneDX component", filepath.Base(path))
		}
		if component.Name != projectModulePath {
			continue
		}
		if component.Version == "" {
			return "", fmt.Errorf("SBOM %s has no version for %s", filepath.Base(path), projectModulePath)
		}
		if projectVersion != "" && projectVersion != component.Version {
			return "", fmt.Errorf("SBOM %s has conflicting versions for %s", filepath.Base(path), projectModulePath)
		}
		projectVersion = component.Version
	}
	if projectVersion == "" {
		return "", fmt.Errorf("SBOM %s does not identify %s", filepath.Base(path), projectModulePath)
	}
	return projectVersion, nil
}

func executeCommand(
	directory string,
	command invocation,
	stdin io.Reader,
	stdout io.Writer,
	stderr io.Writer,
) error {
	// Every invocation is assembled from the fixed task definitions above.
	process := exec.CommandContext(context.Background(), command.name, command.args...) //nolint:gosec
	process.Dir = directory
	process.Stdin = stdin
	process.Stdout = stdout
	process.Stderr = stderr
	if len(command.env) != 0 {
		process.Env = mergeEnvironment(os.Environ(), command.env)
	}
	return process.Run()
}

func mergeEnvironment(
	environment []string,
	settings []environmentSetting,
) []string {
	merged := append([]string(nil), environment...)
	for _, setting := range settings {
		keyPrefix := setting.name + "="
		found := false
		for index, variable := range merged {
			if strings.EqualFold(variableName(variable), setting.name) {
				merged[index] = keyPrefix + setting.value
				found = true
				break
			}
		}
		if !found {
			merged = append(merged, keyPrefix+setting.value)
		}
	}
	return merged
}

func variableName(variable string) string {
	if name, _, found := strings.Cut(variable, "="); found {
		return name
	}
	return variable
}

func renderInvocation(command invocation) string {
	parts := make([]string, 0, len(command.env)+len(command.args)+1)
	for _, setting := range command.env {
		parts = append(parts, renderArgument(setting.name+"="+setting.value))
	}
	parts = append(parts, renderArgument(command.name))
	for _, argument := range command.args {
		parts = append(parts, renderArgument(argument))
	}
	return strings.Join(parts, " ")
}

func renderArgument(argument string) string {
	if argument == "" || strings.IndexFunc(argument, unicode.IsSpace) >= 0 {
		return strconv.Quote(argument)
	}
	return argument
}

func findRepositoryRoot() (string, error) {
	directory, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("get working directory: %w", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(directory, "go.mod")); err == nil {
			return directory, nil
		} else if !errors.Is(err, fs.ErrNotExist) {
			return "", fmt.Errorf("inspect go.mod: %w", err)
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			return "", errors.New("could not find repository root containing go.mod")
		}
		directory = parent
	}
}

func errorExitCode(err error) int {
	var usage *usageError
	if errors.As(err, &usage) {
		return 2
	}
	var processError *exec.ExitError
	if errors.As(err, &processError) {
		if code := processError.ExitCode(); code >= 0 {
			return code
		}
	}
	return 1
}
