// SPDX-FileCopyrightText: 2026 startclean contributors <https://github.com/P4suta/startclean/graphs/contributors>
// SPDX-License-Identifier: MIT OR Apache-2.0

package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/P4suta/startclean/internal/core"
)

func TestCLIExitCodeContracts(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		t.Parallel()
		system := newFakeSystem(t)
		code, _, _, err := executeForTest([]string{"version"}, system)
		if err != nil || code != ExitSuccess {
			t.Fatalf("version exit = (%d, %v), want (%d, nil)", code, err, ExitSuccess)
		}
	})

	t.Run("operational failure", func(t *testing.T) {
		t.Parallel()
		system := newFakeSystem(t)
		system.supported = false
		code, _, _, err := executeForTest([]string{"scan", "--scope", "user"}, system)
		if err == nil || code != ExitOperational {
			t.Fatalf("unsupported exit = (%d, %v), want (%d, error)", code, err, ExitOperational)
		}
	})

	t.Run("usage failure", func(t *testing.T) {
		t.Parallel()
		code, _, _, err := executeForTest([]string{"clean", "--all"}, newFakeSystem(t))
		if err == nil || code != ExitUsage {
			t.Fatalf("incomplete confirmation exit = (%d, %v), want (%d, error)", code, err, ExitUsage)
		}
	})

	t.Run("candidates found", func(t *testing.T) {
		t.Parallel()
		system := newFakeSystem(t)
		link := writeFakeLink(t, system.roots.User, "Stale.lnk")
		system.targets[link] = filepath.Join(system.roots.User, "missing.exe")
		code, stdout, _, err := executeForTest(
			[]string{"scan", "--scope", "user", "--format", "json", "--check"}, system,
		)
		if err == nil || code != ExitCandidatesFound {
			t.Fatalf("scan --check exit = (%d, %v), want (%d, error)", code, err, ExitCandidatesFound)
		}
		if !strings.Contains(stdout, `"schema_version": 1`) || !strings.Contains(stdout, `"stale": 1`) {
			t.Fatalf("scan JSON was not written before --check exit:\n%s", stdout)
		}
	})

	t.Run("elevation required", func(t *testing.T) {
		t.Parallel()
		system := newFakeSystem(t)
		link := writeFakeLink(t, system.roots.Common, "Machine.lnk")
		system.targets[link] = filepath.Join(system.roots.Common, "missing.exe")
		code, _, stderr, err := executeForTest([]string{"clean", "--all", "--yes"}, system)
		if !errors.Is(err, core.ErrElevationRequired) || code != ExitElevationRequired {
			t.Fatalf("unelevated clean exit = (%d, %v), want (%d, ErrElevationRequired)", code, err, ExitElevationRequired)
		}
		if _, statErr := os.Stat(link); statErr != nil {
			t.Fatalf("common shortcut was mutated before elevation preflight: %v", statErr)
		}
		if !strings.Contains(stderr, "Start-Process -FilePath") || !strings.Contains(stderr, "-Verb RunAs") {
			t.Fatalf("PowerShell elevation guidance missing:\n%s", stderr)
		}
		if !strings.Contains(stderr, "-ArgumentList 'clean','--all','--yes'") {
			t.Fatalf("automation elevation guidance lost explicit cleanup flags:\n%s", stderr)
		}
	})
}

func TestNonInteractiveCleanRequiresBothExplicitFlagsBeforeScanning(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{{"clean", "--all"}, {"clean", "--yes"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			t.Parallel()
			system := newFakeSystem(t)
			system.rootsErr = errors.New("Roots must not be called")
			code, _, _, err := executeForTest(args, system)
			if err == nil || code != ExitUsage {
				t.Fatalf("exit = (%d, %v), want (%d, error)", code, err, ExitUsage)
			}
			if system.rootsCalls != 0 {
				t.Fatalf("Roots called %d time(s) before explicit confirmation", system.rootsCalls)
			}
		})
	}
}

func TestNonInteractiveCleanRefusesPartialScanWithoutMutation(t *testing.T) {
	t.Parallel()
	system := newFakeSystem(t)
	system.elevated = true
	staleLink := writeFakeLink(t, system.roots.User, "Stale.lnk")
	unreadableLink := writeFakeLink(t, system.roots.User, "Unreadable.lnk")
	system.targets[staleLink] = filepath.Join(system.roots.User, "missing.exe")

	code, _, _, err := executeForTest([]string{"clean", "--all", "--yes"}, system)
	if err == nil || code != ExitOperational || !strings.Contains(err.Error(), "refusing cleanup because the scan had 1 error(s)") {
		t.Fatalf("partial-scan clean exit = (%d, %v), want (%d, scan error)", code, err, ExitOperational)
	}
	if len(system.deleted) != 0 {
		t.Fatalf("partial-scan clean notified %d deletion(s): %v", len(system.deleted), system.deleted)
	}
	for _, path := range []string{staleLink, unreadableLink} {
		if _, statErr := os.Stat(path); statErr != nil {
			t.Fatalf("partial-scan clean mutated %s: %v", path, statErr)
		}
	}
}

func TestInteractiveInvocationRefusesNonTTYWithoutScanning(t *testing.T) {
	t.Parallel()
	system := newFakeSystem(t)
	system.rootsErr = errors.New("Roots must not be called")
	code, _, _, err := executeForTest(nil, system)
	if err == nil || code != ExitUsage || !strings.Contains(err.Error(), "requires a TTY") {
		t.Fatalf("non-TTY invocation = (%d, %v), want (%d, TTY error)", code, err, ExitUsage)
	}
	if system.rootsCalls != 0 {
		t.Fatalf("Roots called %d time(s) before TTY guard", system.rootsCalls)
	}
}

func TestScanHidesSkippedEntriesUnlessRequested(t *testing.T) {
	t.Parallel()
	system := newFakeSystem(t)
	staleLink := writeFakeLink(t, system.roots.User, "Stale.lnk")
	healthyLink := writeFakeLink(t, system.roots.User, "Healthy.LNK")
	missing := filepath.Join(system.roots.User, "missing.exe")
	existing := filepath.Join(system.roots.User, "healthy.exe")
	if err := os.WriteFile(existing, []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	system.targets[staleLink] = missing
	system.targets[healthyLink] = existing

	code, hidden, _, err := executeForTest([]string{"scan", "--scope", "user"}, system)
	if err != nil || code != ExitSuccess {
		t.Fatalf("scan exit = (%d, %v)", code, err)
	}
	if !strings.Contains(hidden, staleLink) || strings.Contains(hidden, healthyLink) {
		t.Fatalf("default scan did not filter healthy item:\n%s", hidden)
	}

	code, shown, _, err := executeForTest([]string{"scan", "--scope", "user", "--show-skipped"}, system)
	if err != nil || code != ExitSuccess {
		t.Fatalf("scan --show-skipped exit = (%d, %v)", code, err)
	}
	if !strings.Contains(shown, staleLink) || !strings.Contains(shown, healthyLink) {
		t.Fatalf("--show-skipped omitted an item:\n%s", shown)
	}
}

func TestExplicitCleanDeletesOnlyTemporaryEligibleLink(t *testing.T) {
	t.Parallel()
	system := newFakeSystem(t)
	system.elevated = true
	link := writeFakeLink(t, system.roots.User, "Stale.lnk")
	target := filepath.Join(system.roots.User, "missing.exe")
	system.targets[link] = target

	code, stdout, _, err := executeForTest([]string{"clean", "--all", "--yes"}, system)
	if err != nil || code != ExitSuccess {
		t.Fatalf("clean exit = (%d, %v), output:\n%s", code, err, stdout)
	}
	if _, statErr := os.Stat(link); !errors.Is(statErr, fs.ErrNotExist) {
		t.Fatalf("temporary stale shortcut still exists or stat failed unexpectedly: %v", statErr)
	}
	if !strings.Contains(stdout, "Deleted 1 of 1") || len(system.deleted) != 1 {
		t.Fatalf("cleanup result/notification mismatch: output=%q deleted=%v", stdout, system.deleted)
	}
}

func TestNonInteractiveCleanPropagatesSummaryWriteFailureWithoutMaskingCleanFailure(t *testing.T) {
	t.Parallel()

	t.Run("successful cleanup", func(t *testing.T) {
		t.Parallel()
		system := newFakeSystem(t)
		system.elevated = true
		link := writeFakeLink(t, system.roots.User, "Stale.lnk")
		system.targets[link] = filepath.Join(system.roots.User, "missing.exe")
		writeFailure := errors.New("stdout is unavailable")

		code, err := executeWithSystem(
			[]string{"clean", "--all", "--yes"},
			strings.NewReader(""),
			failingWriter{err: writeFailure},
			&bytes.Buffer{},
			system,
		)
		if !errors.Is(err, writeFailure) || code != ExitOperational {
			t.Fatalf("summary write failure exit = (%d, %v), want (%d, write failure)", code, err, ExitOperational)
		}
		if _, statErr := os.Stat(link); !errors.Is(statErr, fs.ErrNotExist) {
			t.Fatalf("successful cleanup did not delete its link: %v", statErr)
		}
		if len(system.deleted) != 1 {
			t.Fatalf("successful cleanup notified %d deletion(s), want 1", len(system.deleted))
		}
	})

	t.Run("cleanup failure takes precedence", func(t *testing.T) {
		t.Parallel()
		system := newFakeSystem(t)
		system.elevated = true
		link := writeFakeLink(t, system.roots.User, "Stale.lnk")
		system.targets[link] = filepath.Join(system.roots.User, "missing.exe")
		system.deleteErr = errors.New("guarded deletion failed")
		writeFailure := errors.New("stdout is unavailable")

		code, err := executeWithSystem(
			[]string{"clean", "--all", "--yes"},
			strings.NewReader(""),
			failingWriter{err: writeFailure},
			&bytes.Buffer{},
			system,
		)
		if err == nil || code != ExitOperational || errors.Is(err, writeFailure) ||
			!strings.Contains(err.Error(), "cleanup completed with operational errors") {
			t.Fatalf("cleanup/write failure exit = (%d, %v), want cleanup error precedence", code, err)
		}
		if _, statErr := os.Stat(link); statErr != nil {
			t.Fatalf("failed cleanup mutated its link: %v", statErr)
		}
		if len(system.deleted) != 0 {
			t.Fatalf("failed cleanup notified %d deletion(s): %v", len(system.deleted), system.deleted)
		}
	})
}

func TestDoctorReportsHealthyTemporaryFoldersAsVersionedJSON(t *testing.T) {
	t.Parallel()

	system := newFakeSystem(t)
	link := writeFakeLink(t, system.roots.User, "Healthy.lnk")
	target := filepath.Join(system.roots.User, "healthy.exe")
	if err := os.WriteFile(target, []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	system.targets[link] = target

	code, stdout, _, err := executeForTest([]string{"doctor", "--format", "json"}, system)
	if err != nil || code != ExitSuccess {
		t.Fatalf("doctor exit = (%d, %v), output:\n%s", code, err, stdout)
	}
	var envelope struct {
		SchemaVersion int `json:"schema_version"`
		Data          struct {
			Supported  bool         `json:"supported"`
			Elevated   bool         `json:"elevated"`
			Folders    []folderInfo `json:"folders"`
			Candidates core.Summary `json:"candidates"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &envelope); err != nil {
		t.Fatalf("decode doctor JSON: %v\n%s", err, stdout)
	}
	if envelope.SchemaVersion != 1 || !envelope.Data.Supported || envelope.Data.Elevated ||
		len(envelope.Data.Folders) != 2 || !envelope.Data.Folders[0].Accessible ||
		!envelope.Data.Folders[1].Accessible || envelope.Data.Candidates.Healthy != 1 {
		t.Fatalf("unexpected doctor JSON: %+v", envelope)
	}
}

func TestDoctorTableIncludesFolderAndCandidateDiagnostics(t *testing.T) {
	t.Parallel()

	system := newFakeSystem(t)
	link := writeFakeLink(t, system.roots.User, "Stale.lnk")
	system.targets[link] = filepath.Join(system.roots.User, "missing.exe")

	code, stdout, _, err := executeForTest([]string{"doctor", "--format", "table"}, system)
	if err != nil || code != ExitSuccess {
		t.Fatalf("doctor table exit = (%d, %v), output:\n%s", code, err, stdout)
	}
	for _, want := range []string{"windows/amd64", "Supported", "User Programs", "Common Programs", "1 total (1 user, 0 common)"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("doctor table is missing %q:\n%s", want, stdout)
		}
	}
}

func TestDoctorEmitsDiagnosticsBeforeOperationalExit(t *testing.T) {
	t.Parallel()

	system := newFakeSystem(t)
	system.roots.Common = filepath.Join(t.TempDir(), "missing-common")
	code, stdout, _, err := executeForTest([]string{"doctor", "--format", "json"}, system)
	if err == nil || code != ExitOperational {
		t.Fatalf("doctor exit = (%d, %v), want (%d, error)", code, err, ExitOperational)
	}
	if !strings.Contains(stdout, `"schema_version": 1`) ||
		!strings.Contains(stdout, `"accessible": false`) ||
		!strings.Contains(stdout, `"errors": 1`) {
		t.Fatalf("doctor omitted partial diagnostics before failing:\n%s", stdout)
	}
}

type fakeSystem struct {
	roots       core.Roots
	targets     map[string]string
	supported   bool
	elevated    bool
	rootsErr    error
	rootsCalls  int
	deleteErr   error
	deleted     []string
	directories []string
}

func newFakeSystem(t *testing.T) *fakeSystem {
	t.Helper()
	base := t.TempDir()
	user := filepath.Join(base, "User Programs")
	common := filepath.Join(base, "Common Programs")
	for _, root := range []string{user, common} {
		if err := os.MkdirAll(root, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	return &fakeSystem{
		roots: core.Roots{User: user, Common: common}, targets: make(map[string]string), supported: true,
	}
}

func (s *fakeSystem) Supported() bool { return s.supported }
func (s *fakeSystem) Roots() (core.Roots, error) {
	s.rootsCalls++
	return s.roots, s.rootsErr
}
func (s *fakeSystem) Elevated() bool { return s.elevated }
func (s *fakeSystem) Target(path string) (string, error) {
	target, ok := s.targets[path]
	if !ok {
		return "", fs.ErrNotExist
	}
	return target, nil
}
func (s *fakeSystem) ExpandEnvironment(value string) (string, error) { return value, nil }
func (s *fakeSystem) DriveKind(string) (core.DriveKind, error)       { return core.DriveFixed, nil }
func (s *fakeSystem) Deleted(path string)                            { s.deleted = append(s.deleted, path) }
func (s *fakeSystem) DirectoryRemoved(path string) {
	s.directories = append(s.directories, path)
}
func (s *fakeSystem) DeleteValidated(_ string, path string, validate func() error) error {
	if err := validate(); err != nil {
		return err
	}
	if s.deleteErr != nil {
		return s.deleteErr
	}
	return os.Remove(path)
}
func (s *fakeSystem) RemoveEmptyDirectory(_ string, path string) (bool, error) {
	entries, err := os.ReadDir(path)
	if err != nil {
		return false, err
	}
	if len(entries) != 0 {
		return false, nil
	}
	if err := os.Remove(path); err != nil {
		return false, err
	}
	return true, nil
}

func writeFakeLink(t *testing.T, root, name string) string {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.WriteFile(path, []byte("test shortcut"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func executeForTest(args []string, system systemAdapter) (int, string, string, error) {
	var stdout, stderr bytes.Buffer
	code, err := executeWithSystem(args, strings.NewReader(""), &stdout, &stderr, system)
	return code, stdout.String(), stderr.String(), err
}

type failingWriter struct{ err error }

func (w failingWriter) Write([]byte) (int, error) { return 0, w.err }
