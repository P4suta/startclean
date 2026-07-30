// SPDX-FileCopyrightText: 2026 startclean contributors <https://github.com/P4suta/startclean/graphs/contributors>
// SPDX-License-Identifier: MIT OR Apache-2.0

package core

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func TestCleanerDeletesOnlyRevalidatedLinkAndPrunesEmptyParents(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	group := filepath.Join(root, "Vendor", "Tools")
	if err := os.MkdirAll(group, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(group, "Example.lnk")
	if err := os.WriteFile(link, []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "missing.exe")
	notifier := &recordingNotifier{}
	cleaner := testCleaner(root, mapReader{link: target})
	cleaner.Notifier = notifier
	result, err := cleaner.Clean([]Item{staleItem(ScopeUser, link, target)})
	if err != nil {
		t.Fatalf("Clean() error = %v", err)
	}
	if result.Deleted != 1 || result.Pruned != 2 {
		t.Fatalf("unexpected result: %+v", result)
	}
	if _, err := os.Stat(root); err != nil {
		t.Fatalf("known-folder root was removed: %v", err)
	}
	if len(notifier.deleted) != 1 || len(notifier.removedDirectories) != 2 {
		t.Fatalf("unexpected notifications: %+v", notifier)
	}
}

func TestCleanerRejectsOutsideRoot(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.lnk")
	if err := os.WriteFile(outside, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "missing.exe")
	cleaner := testCleaner(root, mapReader{outside: target})
	result, err := cleaner.Clean([]Item{staleItem(ScopeUser, outside, target)})
	if err == nil || len(result.Errors) != 1 || result.Errors[0].ReasonCode != ReasonOutsideApprovedRoot {
		t.Fatalf("unexpected result: %+v, %v", result, err)
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatalf("outside file was touched: %v", err)
	}
}

func TestCleanerAbortsAllUsersSelectionBeforeMutationWithoutElevation(t *testing.T) {
	t.Parallel()
	userRoot := t.TempDir()
	commonRoot := t.TempDir()
	userLink := filepath.Join(userRoot, "user.lnk")
	commonLink := filepath.Join(commonRoot, "common.lnk")
	for _, path := range []string{userLink, commonLink} {
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	userTarget := filepath.Join(userRoot, "missing.exe")
	commonTarget := filepath.Join(commonRoot, "missing.exe")
	cleaner := testCleaner(userRoot, mapReader{userLink: userTarget, commonLink: commonTarget})
	cleaner.Roots.Common = commonRoot
	_, err := cleaner.Clean([]Item{
		staleItem(ScopeUser, userLink, userTarget),
		staleItem(ScopeCommon, commonLink, commonTarget),
	})
	if !errors.Is(err, ErrElevationRequired) {
		t.Fatalf("Clean() error = %v, want ErrElevationRequired", err)
	}
	for _, path := range []string{userLink, commonLink} {
		if _, statErr := os.Stat(path); statErr != nil {
			t.Fatalf("%s was mutated before elevation preflight: %v", path, statErr)
		}
	}
}

func TestCleanerAbortsWhenTargetChangedSinceScan(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	link := filepath.Join(root, "changed.lnk")
	if err := os.WriteFile(link, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	scannedTarget := filepath.Join(root, "old-missing.exe")
	newTarget := filepath.Join(root, "new-missing.exe")
	cleaner := testCleaner(root, mapReader{link: newTarget})
	result, err := cleaner.Clean([]Item{staleItem(ScopeUser, link, scannedTarget)})
	if err == nil || result.Deleted != 0 || result.Errors[0].ReasonCode != ReasonChangedSinceScan {
		t.Fatalf("unexpected result: %+v, %v", result, err)
	}
	if _, err := os.Stat(link); err != nil {
		t.Fatalf("changed link was deleted: %v", err)
	}
}

func TestCleanerPrevalidatesEntireSelectionBeforeMutation(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	validLink := filepath.Join(root, "valid.lnk")
	nonStaleLink := filepath.Join(root, "healthy.lnk")
	for _, path := range []string{validLink, nonStaleLink} {
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	validTarget := filepath.Join(root, "missing-valid.exe")
	nonStaleTarget := filepath.Join(root, "missing-but-not-scanned-stale.exe")
	cleaner := testCleaner(root, mapReader{validLink: validTarget, nonStaleLink: nonStaleTarget})
	nonStale := staleItem(ScopeUser, nonStaleLink, nonStaleTarget)
	nonStale.Classification = ClassificationHealthy

	result, err := cleaner.Clean([]Item{staleItem(ScopeUser, validLink, validTarget), nonStale})
	if err == nil || result.Deleted != 0 || len(result.Errors) != 1 {
		t.Fatalf("unexpected preflight result: %+v, %v", result, err)
	}
	for _, path := range []string{validLink, nonStaleLink} {
		if _, statErr := os.Stat(path); statErr != nil {
			t.Fatalf("%s was mutated before selection preflight completed: %v", path, statErr)
		}
	}
}

func TestCleanerRejectsUnsafeLinkAndWrongExtension(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	target := filepath.Join(root, "missing.exe")
	tests := []struct {
		name string
		item Item
		fs   InspectionFS
	}{
		{
			name: "symlink mode",
			item: staleItem(ScopeUser, filepath.Join(root, "unsafe.lnk"), target),
			fs: faultInspectionFS{
				InspectionFS: OSFS{},
				lstat: func(string) (fs.FileInfo, error) {
					return fakeFileInfo{name: "unsafe.lnk", mode: fs.ModeSymlink}, nil
				},
			},
		},
		{
			name: "wrong extension",
			item: staleItem(ScopeUser, filepath.Join(root, "not-a-link.url"), target),
			fs:   OSFS{},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			cleaner := testCleaner(root, mapReader{test.item.LinkPath: target})
			cleaner.FS = test.fs
			result, err := cleaner.Clean([]Item{test.item})
			if err == nil || result.Deleted != 0 || len(result.Errors) != 1 {
				t.Fatalf("unexpected safety result: %+v, %v", result, err)
			}
			wantReason := ReasonUnsafeLink
			if test.name == "wrong extension" {
				wantReason = ReasonOutsideApprovedRoot
			}
			if result.Errors[0].ReasonCode != wantReason {
				t.Fatalf("reason = %s, want %s", result.Errors[0].ReasonCode, wantReason)
			}
		})
	}
}

func TestCleanerRejectsTargetThatBecameHealthy(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	link := filepath.Join(root, "repaired.lnk")
	target := filepath.Join(root, "now-present.exe")
	for _, path := range []string{link, target} {
		if err := os.WriteFile(path, []byte("fixture"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cleaner := testCleaner(root, mapReader{link: target})
	cleaner.Classifier.Stat = os.Stat
	result, err := cleaner.Clean([]Item{staleItem(ScopeUser, link, target)})
	if err == nil || result.Deleted != 0 || result.Errors[0].ReasonCode != ReasonChangedSinceScan {
		t.Fatalf("unexpected repaired-target result: %+v, %v", result, err)
	}
	if _, statErr := os.Stat(link); statErr != nil {
		t.Fatalf("link to repaired target was deleted: %v", statErr)
	}
}

func TestCleanerReportsEarlierSuccessBeforeGuardedFailure(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	failingLink := filepath.Join(root, "cannot-delete.lnk")
	successfulLink := filepath.Join(root, "deleted.lnk")
	for _, path := range []string{failingLink, successfulLink} {
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	failingTarget := filepath.Join(root, "missing-one.exe")
	successfulTarget := filepath.Join(root, "missing-two.exe")
	cleaner := testCleaner(root, mapReader{failingLink: failingTarget, successfulLink: successfulTarget})
	cleaner.Remover = testGuardedRemover{
		deleteValidated: func(root, path string, validate func() error) error {
			if samePath(path, failingLink) {
				return fs.ErrPermission
			}
			return deleteTestLink(root, path, validate)
		},
	}

	result, err := cleaner.Clean([]Item{
		staleItem(ScopeUser, successfulLink, successfulTarget),
		staleItem(ScopeUser, failingLink, failingTarget),
	})
	if err == nil || result.Requested != 2 || result.Deleted != 1 || len(result.Errors) != 1 {
		t.Fatalf("unexpected partial result: %+v, %v", result, err)
	}
	if result.Errors[0].ReasonCode != ReasonDeleteFailure {
		t.Fatalf("reason = %s, want %s", result.Errors[0].ReasonCode, ReasonDeleteFailure)
	}
	if _, statErr := os.Stat(failingLink); statErr != nil {
		t.Fatalf("failed deletion unexpectedly removed its link: %v", statErr)
	}
	if _, statErr := os.Stat(successfulLink); !errors.Is(statErr, fs.ErrNotExist) {
		t.Fatalf("independent eligible link was not deleted: %v", statErr)
	}
}

func TestCleanerStopsAfterGuardedFailureWithoutTouchingLaterItems(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	failingLink := filepath.Join(root, "cannot-delete.lnk")
	laterLink := filepath.Join(root, "must-remain.lnk")
	for _, path := range []string{failingLink, laterLink} {
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	failingTarget := filepath.Join(root, "missing-one.exe")
	laterTarget := filepath.Join(root, "missing-two.exe")
	cleaner := testCleaner(root, mapReader{
		failingLink: failingTarget,
		laterLink:   laterTarget,
	})
	var guardedPaths []string
	cleaner.Remover = testGuardedRemover{
		deleteValidated: func(root, path string, validate func() error) error {
			guardedPaths = append(guardedPaths, path)
			if samePath(path, failingLink) {
				return fs.ErrPermission
			}
			return deleteTestLink(root, path, validate)
		},
	}

	result, err := cleaner.Clean([]Item{
		staleItem(ScopeUser, failingLink, failingTarget),
		staleItem(ScopeUser, laterLink, laterTarget),
	})
	if err == nil || result.Requested != 2 || result.Deleted != 0 || len(result.Errors) != 1 {
		t.Fatalf("unexpected guarded failure result: %+v, %v", result, err)
	}
	if len(guardedPaths) != 1 || !samePath(guardedPaths[0], failingLink) {
		t.Fatalf("guarded paths = %#v, want only %q", guardedPaths, failingLink)
	}
	for _, path := range []string{failingLink, laterLink} {
		if _, statErr := os.Stat(path); statErr != nil {
			t.Fatalf("%s was touched after a guarded failure: %v", path, statErr)
		}
	}
}

func TestCleanerReportsPruneFailureAfterSuccessfulDeletion(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	group := filepath.Join(root, "Vendor")
	if err := os.MkdirAll(group, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(group, "Stale.lnk")
	if err := os.WriteFile(link, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "missing.exe")
	cleaner := testCleaner(root, mapReader{link: target})
	cleaner.Remover = testGuardedRemover{
		removeEmptyDirectory: func(_ string, path string) (bool, error) {
			if samePath(path, group) {
				return false, fs.ErrPermission
			}
			return removeTestDirectory(path)
		},
	}

	result, err := cleaner.Clean([]Item{staleItem(ScopeUser, link, target)})
	if err == nil || result.Deleted != 1 || result.Pruned != 0 || len(result.Errors) != 1 {
		t.Fatalf("unexpected prune result: %+v, %v", result, err)
	}
	if result.Errors[0].ReasonCode != ReasonPruneFailure {
		t.Fatalf("reason = %s, want %s", result.Errors[0].ReasonCode, ReasonPruneFailure)
	}
	if _, statErr := os.Stat(group); statErr != nil {
		t.Fatalf("folder with failed prune inspection was removed: %v", statErr)
	}
}

func TestCleanerRevalidatesEachLinkInsideDeletionGuard(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	firstLink := filepath.Join(root, "first.lnk")
	secondLink := filepath.Join(root, "second.lnk")
	for _, path := range []string{firstLink, secondLink} {
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	firstTarget := filepath.Join(root, "first-missing.exe")
	changedTarget := filepath.Join(root, "changed-missing.exe")
	secondTarget := filepath.Join(root, "second-missing.exe")
	reader := &mutationReader{
		targets: map[string]string{firstLink: firstTarget, secondLink: secondTarget},
		trigger: secondLink,
		mutate: func(targets map[string]string) {
			targets[firstLink] = changedTarget
		},
	}
	cleaner := testCleaner(root, reader)
	result, err := cleaner.Clean([]Item{
		staleItem(ScopeUser, firstLink, firstTarget),
		staleItem(ScopeUser, secondLink, secondTarget),
	})
	if err == nil || result.Deleted != 0 || len(result.Errors) != 1 {
		t.Fatalf("unexpected guarded revalidation result: %+v, %v", result, err)
	}
	if result.Errors[0].ReasonCode != ReasonChangedSinceScan {
		t.Fatalf("reason = %s, want %s", result.Errors[0].ReasonCode, ReasonChangedSinceScan)
	}
	for _, path := range []string{firstLink, secondLink} {
		if _, statErr := os.Stat(path); statErr != nil {
			t.Fatalf("%s was deleted after selection changed during preflight: %v", path, statErr)
		}
	}
}

func TestCleanerMapsGuardedPathFailuresWithoutMutation(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		err    error
		reason ReasonCode
	}{
		{name: "outside final path", err: ErrDeletionOutsideRoot, reason: ReasonOutsideApprovedRoot},
		{name: "reparse or unsafe leaf", err: ErrUnsafeDeletionPath, reason: ReasonUnsafeLink},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			link := filepath.Join(root, "candidate.lnk")
			if err := os.WriteFile(link, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(root, "missing.exe")
			cleaner := testCleaner(root, mapReader{link: target})
			cleaner.Remover = testGuardedRemover{
				deleteValidated: func(string, string, func() error) error { return test.err },
			}
			result, err := cleaner.Clean([]Item{staleItem(ScopeUser, link, target)})
			if err == nil || result.Deleted != 0 || len(result.Errors) != 1 || result.Errors[0].ReasonCode != test.reason {
				t.Fatalf("unexpected guarded failure result: %+v, %v", result, err)
			}
			if _, statErr := os.Stat(link); statErr != nil {
				t.Fatalf("guard-rejected path was mutated: %v", statErr)
			}
		})
	}
}

func TestCleanerRefusesToMutateWithoutGuardedRemover(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	link := filepath.Join(root, "candidate.lnk")
	if err := os.WriteFile(link, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "missing.exe")
	cleaner := testCleaner(root, mapReader{link: target})
	cleaner.Remover = nil
	result, err := cleaner.Clean([]Item{staleItem(ScopeUser, link, target)})
	if err == nil || result.Deleted != 0 {
		t.Fatalf("unexpected missing-guard result: %+v, %v", result, err)
	}
	if _, statErr := os.Stat(link); statErr != nil {
		t.Fatalf("link was mutated without a guarded remover: %v", statErr)
	}
}

func testCleaner(root string, reader LinkReader) Cleaner {
	return Cleaner{
		Roots: Roots{User: root}, Reader: reader, FS: OSFS{}, Remover: testGuardedRemover{},
		Classifier: Classifier{
			ExpandEnv: func(value string) (string, error) { return value, nil },
			DriveKind: func(string) (DriveKind, error) { return DriveFixed, nil },
			Stat:      func(string) (fs.FileInfo, error) { return nil, fs.ErrNotExist },
		},
	}
}

func staleItem(scope Scope, link, target string) Item {
	return Item{
		Scope: scope, LinkPath: link, TargetPath: filepath.Clean(target),
		Classification: ClassificationStale, ReasonCode: ReasonTargetMissing,
		ElevationRequired: scope == ScopeCommon,
	}
}

type recordingNotifier struct {
	deleted            []string
	removedDirectories []string
}

type faultInspectionFS struct {
	InspectionFS
	lstat func(string) (fs.FileInfo, error)
}

func (f faultInspectionFS) Lstat(path string) (fs.FileInfo, error) {
	if f.lstat != nil {
		return f.lstat(path)
	}
	return f.InspectionFS.Lstat(path)
}

type testGuardedRemover struct {
	deleteValidated      func(string, string, func() error) error
	removeEmptyDirectory func(string, string) (bool, error)
}

func (r testGuardedRemover) DeleteValidated(root, path string, validate func() error) error {
	if r.deleteValidated != nil {
		return r.deleteValidated(root, path, validate)
	}
	return deleteTestLink(root, path, validate)
}

func (r testGuardedRemover) RemoveEmptyDirectory(root, path string) (bool, error) {
	if r.removeEmptyDirectory != nil {
		return r.removeEmptyDirectory(root, path)
	}
	return removeTestDirectory(path)
}

func deleteTestLink(_ string, path string, validate func() error) error {
	if err := validate(); err != nil {
		return err
	}
	return os.Remove(path)
}

func removeTestDirectory(path string) (bool, error) {
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

type mutationReader struct {
	targets map[string]string
	trigger string
	mutate  func(map[string]string)
	done    bool
}

func (r *mutationReader) Target(path string) (string, error) {
	target, ok := r.targets[path]
	if !ok {
		return "", fs.ErrNotExist
	}
	if path == r.trigger && !r.done {
		r.done = true
		r.mutate(r.targets)
	}
	return target, nil
}

func (n *recordingNotifier) Deleted(path string) {
	n.deleted = append(n.deleted, path)
}

func (n *recordingNotifier) DirectoryRemoved(path string) {
	n.removedDirectories = append(n.removedDirectories, path)
}
