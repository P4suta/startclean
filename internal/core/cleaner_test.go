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

func testCleaner(root string, reader LinkReader) Cleaner {
	return Cleaner{
		Roots: Roots{User: root}, Reader: reader, FS: OSFS{},
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

func (n *recordingNotifier) Deleted(path string) {
	n.deleted = append(n.deleted, path)
}

func (n *recordingNotifier) DirectoryRemoved(path string) {
	n.removedDirectories = append(n.removedDirectories, path)
}
