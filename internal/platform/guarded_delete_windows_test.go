// SPDX-FileCopyrightText: 2026 startclean contributors <https://github.com/P4suta/startclean/graphs/contributors>
// SPDX-License-Identifier: MIT OR Apache-2.0

//go:build windows

package platform

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"unsafe"

	"github.com/P4suta/startclean/internal/core"
	"golang.org/x/sys/windows"
)

func TestGuardedChildRelationIsCaseInsensitiveAndBoundaryAware(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		root      string
		candidate string
		wantError error
	}{
		{
			name:      "case insensitive child",
			root:      `\\?\C:\Users\Example\Programs`,
			candidate: `\\?\c:\users\example\PROGRAMS\Vendor\App.lnk`,
		},
		{
			name:      "sibling prefix",
			root:      `\\?\C:\Users\Example\Programs`,
			candidate: `\\?\C:\Users\Example\Programs-old\App.lnk`,
			wantError: core.ErrDeletionOutsideRoot,
		},
		{
			name:      "root itself",
			root:      `\\?\C:\Users\Example\Programs`,
			candidate: `\\?\c:\users\example\programs`,
			wantError: core.ErrUnsafeDeletionPath,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			err := guardedChildRelation(test.root, test.candidate)
			if test.wantError == nil {
				if err != nil {
					t.Fatalf("guardedChildRelation() error = %v", err)
				}
				return
			}
			if !errors.Is(err, test.wantError) {
				t.Fatalf("guardedChildRelation() error = %v, want %v", err, test.wantError)
			}
		})
	}
}

func TestSystemDeleteValidatedDeletesExactTemporaryFile(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	path := filepath.Join(root, "Vendor", "Unicode アプリ.lnk")
	mustWriteTestFile(t, path, "shortcut")
	validationCalls := 0

	err := New().DeleteValidated(root, path, func() error {
		validationCalls++
		_, statErr := os.Stat(path)
		return statErr
	})

	if err != nil {
		t.Fatalf("DeleteValidated() error = %v", err)
	}
	if validationCalls != 1 {
		t.Fatalf("validation calls = %d, want 1", validationCalls)
	}
	assertPathMissing(t, path)
}

func TestSystemDeleteValidatedValidationFailureKeepsFile(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	path := filepath.Join(root, "App.lnk")
	mustWriteTestFile(t, path, "shortcut")
	validationFailure := errors.New("injected validation failure")

	err := New().DeleteValidated(root, path, func() error {
		return validationFailure
	})

	if !errors.Is(err, validationFailure) {
		t.Fatalf("DeleteValidated() error = %v, want validation failure", err)
	}
	assertPathExists(t, path)
}

func TestSystemDeleteValidatedAllowsShellLinkReloadWhileHandleIsPinned(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	target := filepath.Join(root, "Target.exe")
	path := filepath.Join(root, "Reload.lnk")
	mustWriteTestFile(t, target, "target")
	if err := createGuardedDeleteShellLink(path, target); err != nil {
		t.Fatalf("create temporary Shell Link: %v", err)
	}
	system := New()
	var reloadedTarget string

	err := system.DeleteValidated(root, path, func() error {
		var reloadErr error
		reloadedTarget, reloadErr = system.Target(path)
		return reloadErr
	})

	if err != nil {
		t.Fatalf("DeleteValidated() error = %v", err)
	}
	if !sameTemporaryTestPath(reloadedTarget, target) {
		t.Fatalf("Target() = %q, want %q", reloadedTarget, target)
	}
	assertPathMissing(t, path)
	assertPathExists(t, target)
}
func TestSystemDeleteValidatedBlocksRenameAndReplacementDuringValidation(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	path := filepath.Join(root, "App.lnk")
	moved := filepath.Join(root, "Moved.lnk")
	replacement := filepath.Join(root, "Replacement.lnk")
	mustWriteTestFile(t, path, "original")
	mustWriteTestFile(t, replacement, "replacement")
	var writeErr, renameErr, replaceErr error

	err := New().DeleteValidated(root, path, func() error {
		writeErr = os.WriteFile(path, []byte("mutated"), 0o600)
		renameErr = os.Rename(path, moved)
		replaceErr = os.Rename(replacement, path)
		return nil
	})

	if err != nil {
		t.Fatalf("DeleteValidated() error = %v (write=%v rename=%v replacement=%v)", err, writeErr, renameErr, replaceErr)
	}
	assertSharingViolation(t, "in-place write", writeErr)
	assertSharingViolation(t, "rename", renameErr)
	assertReplacementBlocked(t, replaceErr)
	assertPathMissing(t, path)
	assertPathMissing(t, moved)
	assertPathExists(t, replacement)
}

func TestSystemDeleteValidatedRejectsUnsafeLeaves(t *testing.T) {
	t.Parallel()
	t.Run("directory", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		path := filepath.Join(root, "Folder.lnk")
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
		validationCalled := false

		err := New().DeleteValidated(root, path, func() error {
			validationCalled = true
			return nil
		})

		if !errors.Is(err, core.ErrUnsafeDeletionPath) {
			t.Fatalf("DeleteValidated() error = %v, want unsafe path", err)
		}
		if validationCalled {
			t.Fatal("validation ran for an unsafe directory leaf")
		}
		assertPathExists(t, path)
	})

	t.Run("reparse point", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		target := filepath.Join(root, "Target.txt")
		path := filepath.Join(root, "Alias.lnk")
		mustWriteTestFile(t, target, "target")
		makeSymlinkOrSkip(t, target, path)
		validationCalled := false

		err := New().DeleteValidated(root, path, func() error {
			validationCalled = true
			return nil
		})

		if !errors.Is(err, core.ErrUnsafeDeletionPath) {
			t.Fatalf("DeleteValidated() error = %v, want unsafe path", err)
		}
		if validationCalled {
			t.Fatal("validation ran for a reparse-point leaf")
		}
		assertPathExists(t, target)
		assertPathExists(t, path)
	})
}

func TestSystemDeleteValidatedRejectsAmbiguousHardLink(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	target := filepath.Join(root, "Original.lnk")
	path := filepath.Join(root, "Alias.lnk")
	mustWriteTestFile(t, target, "target")
	if err := os.Link(target, path); err != nil {
		t.Fatal(err)
	}
	validationCalled := false

	err := New().DeleteValidated(root, path, func() error {
		validationCalled = true
		return nil
	})

	if !errors.Is(err, core.ErrUnsafeDeletionPath) {
		t.Fatalf("DeleteValidated() error = %v, want unsafe path", err)
	}
	if validationCalled {
		t.Fatal("validation ran for an ambiguous hard-link leaf")
	}
	assertPathExists(t, target)
	assertPathExists(t, path)
}
func TestIdentityAnchorProtectsValidationToDeleteGap(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	path := filepath.Join(root, "App.lnk")
	moved := filepath.Join(root, "Moved.lnk")
	replacement := filepath.Join(root, "Replacement.lnk")
	mustWriteTestFile(t, path, "original")
	mustWriteTestFile(t, replacement, "replacement")

	rootPath, candidatePath, err := guardedAbsoluteChild(root, path)
	if err != nil {
		t.Fatal(err)
	}
	rootHandle, err := openGuardedHandle(
		rootPath,
		windows.FILE_READ_ATTRIBUTES|windows.FILE_LIST_DIRECTORY,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE,
		windows.FILE_FLAG_BACKUP_SEMANTICS,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = windows.CloseHandle(rootHandle) }()

	validationHandle, err := openGuardedHandle(
		candidatePath,
		windows.FILE_READ_DATA|windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ,
		windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT,
	)
	if err != nil {
		t.Fatal(err)
	}
	validationOpen := true
	defer func() {
		if validationOpen {
			_ = windows.CloseHandle(validationHandle)
		}
	}()
	validatedIdentity, err := guardedLeafIdentity(validationHandle, candidatePath, false)
	if err != nil {
		t.Fatal(err)
	}

	anchorHandle, err := openGuardedHandle(
		candidatePath,
		windows.FILE_READ_DATA|windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_DELETE,
		windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = windows.CloseHandle(anchorHandle) }()
	anchorIdentity, err := guardedLeafIdentity(anchorHandle, candidatePath, false)
	if err != nil {
		t.Fatal(err)
	}
	if anchorIdentity != validatedIdentity {
		t.Fatal("anchor did not retain the validated file identity")
	}
	if err := windows.CloseHandle(validationHandle); err != nil {
		t.Fatal(err)
	}
	validationOpen = false

	writeErr := os.WriteFile(path, []byte("mutated"), 0o600)
	assertSharingViolation(t, "write during anchored re-open gap", writeErr)
	if err := os.Rename(path, moved); err != nil {
		t.Fatalf("rename original while delete-sharing anchor is held: %v", err)
	}
	if err := os.Rename(replacement, path); err != nil {
		t.Fatalf("place replacement while identity anchor is held: %v", err)
	}

	deleteHandle, err := openGuardedHandle(
		candidatePath,
		windows.DELETE|windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE,
		windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = windows.CloseHandle(deleteHandle) }()
	deleteIdentity, err := guardedLeafIdentity(deleteHandle, candidatePath, false)
	if err != nil {
		t.Fatal(err)
	}
	if deleteIdentity == anchorIdentity {
		t.Fatal("replacement unexpectedly retained the anchored file identity")
	}
	assertPathExists(t, moved)
	assertPathExists(t, path)
}
func TestSystemDeleteValidatedBlocksAncestorReplacementDuringValidation(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	ancestor := filepath.Join(root, "Vendor")
	path := filepath.Join(ancestor, "App.lnk")
	movedAncestor := filepath.Join(root, "Moved Vendor")
	replacementAncestor := filepath.Join(root, "Replacement Vendor")
	replacementPath := filepath.Join(replacementAncestor, "App.lnk")
	mustWriteTestFile(t, path, "original")
	mustWriteTestFile(t, replacementPath, "replacement")
	var renameErr, replaceErr error

	err := New().DeleteValidated(root, path, func() error {
		renameErr = os.Rename(ancestor, movedAncestor)
		replaceErr = os.Rename(replacementAncestor, ancestor)
		return nil
	})

	if err != nil {
		t.Fatalf("DeleteValidated() error = %v (ancestor rename=%v replacement=%v)", err, renameErr, replaceErr)
	}
	assertSharingViolation(t, "ancestor rename", renameErr)
	if replaceErr == nil {
		t.Fatal("ancestor replacement unexpectedly succeeded")
	}
	assertPathMissing(t, path)
	assertPathMissing(t, movedAncestor)
	assertPathExists(t, replacementPath)
}

func TestGuardedAncestorLocksProtectDispositionPhase(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name          string
		candidateName string
		directory     bool
	}{
		{name: "shortcut", candidateName: "App.lnk"},
		{name: "directory prune", candidateName: "Empty", directory: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			ancestor := filepath.Join(root, "Vendor")
			candidate := filepath.Join(ancestor, test.candidateName)
			movedAncestor := filepath.Join(root, "Moved Vendor")
			if test.directory {
				if err := os.MkdirAll(candidate, 0o700); err != nil {
					t.Fatal(err)
				}
			} else {
				mustWriteTestFile(t, candidate, "shortcut")
			}

			rootPath, candidatePath, err := guardedAbsoluteChild(root, candidate)
			if err != nil {
				t.Fatal(err)
			}
			rootHandle, err := openGuardedHandle(
				rootPath,
				windows.FILE_READ_ATTRIBUTES|windows.FILE_LIST_DIRECTORY,
				windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE,
				windows.FILE_FLAG_BACKUP_SEMANTICS,
			)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = windows.CloseHandle(rootHandle) }()
			ancestorHandles, err := lockGuardedAncestors(rootPath, candidatePath, rootHandle)
			if err != nil {
				t.Fatal(err)
			}
			defer closeGuardedHandles(ancestorHandles)

			access := uint32(windows.DELETE | windows.FILE_READ_ATTRIBUTES)
			if test.directory {
				access |= windows.FILE_LIST_DIRECTORY
			}
			candidateHandle, err := openGuardedHandle(
				candidatePath,
				access,
				windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE,
				windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT,
			)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = windows.CloseHandle(candidateHandle) }()
			if _, err := guardedLeafIdentity(candidateHandle, candidatePath, test.directory); err != nil {
				t.Fatal(err)
			}
			if err := requireFinalChild(rootHandle, candidateHandle); err != nil {
				t.Fatal(err)
			}

			renameErr := os.Rename(ancestor, movedAncestor)
			assertSharingViolation(t, "ancestor rename during disposition phase", renameErr)
			assertPathExists(t, candidate)
		})
	}
}
func TestSystemDeleteValidatedRejectsAncestorSymlinkOutsideRoot(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	outside := t.TempDir()
	redirect := filepath.Join(root, "Redirect")
	path := filepath.Join(redirect, "Outside App.lnk")
	mustWriteTestFile(t, filepath.Join(outside, "Outside App.lnk"), "outside")
	makeSymlinkOrSkip(t, outside, redirect)
	validationCalled := false

	err := New().DeleteValidated(root, path, func() error {
		validationCalled = true
		return nil
	})

	if !errors.Is(err, core.ErrDeletionOutsideRoot) {
		t.Fatalf("DeleteValidated() error = %v, want outside-root error", err)
	}
	if validationCalled {
		t.Fatal("validation ran for a candidate whose final path is outside root")
	}
	assertPathExists(t, filepath.Join(outside, "Outside App.lnk"))
}

func TestSystemRemoveEmptyDirectoryDeletesOnlyEmptyChildren(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	empty := filepath.Join(root, "Vendor", "Empty")
	nonEmpty := filepath.Join(root, "NonEmpty")
	if err := os.MkdirAll(empty, 0o700); err != nil {
		t.Fatal(err)
	}
	mustWriteTestFile(t, filepath.Join(nonEmpty, "keep.txt"), "keep")
	system := New()

	removed, err := system.RemoveEmptyDirectory(root, empty)
	if err != nil || !removed {
		t.Fatalf("RemoveEmptyDirectory(empty) = (%v, %v), want (true, nil)", removed, err)
	}
	assertPathMissing(t, empty)

	removed, err = system.RemoveEmptyDirectory(root, nonEmpty)
	if err != nil || removed {
		t.Fatalf("RemoveEmptyDirectory(non-empty) = (%v, %v), want (false, nil)", removed, err)
	}
	assertPathExists(t, filepath.Join(nonEmpty, "keep.txt"))

	removed, err = system.RemoveEmptyDirectory(root, root)
	if removed || !errors.Is(err, core.ErrUnsafeDeletionPath) {
		t.Fatalf("RemoveEmptyDirectory(root) = (%v, %v), want unsafe-path error", removed, err)
	}

	outside := t.TempDir()
	removed, err = system.RemoveEmptyDirectory(root, outside)
	if removed || !errors.Is(err, core.ErrDeletionOutsideRoot) {
		t.Fatalf("RemoveEmptyDirectory(outside) = (%v, %v), want outside-root error", removed, err)
	}
	assertPathExists(t, outside)
}

func TestSystemRemoveEmptyDirectoryRejectsReparsePoint(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	target := filepath.Join(root, "Target")
	path := filepath.Join(root, "Alias")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	makeSymlinkOrSkip(t, target, path)

	removed, err := New().RemoveEmptyDirectory(root, path)

	if removed || !errors.Is(err, core.ErrUnsafeDeletionPath) {
		t.Fatalf("RemoveEmptyDirectory(reparse) = (%v, %v), want unsafe-path error", removed, err)
	}
	assertPathExists(t, target)
	assertPathExists(t, path)
}

func mustWriteTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func makeSymlinkOrSkip(t *testing.T, target, path string) {
	t.Helper()
	if err := os.Symlink(target, path); err != nil {
		t.Skipf("creating a symlink requires Developer Mode or elevation: %v", err)
	}
}

func sameTemporaryTestPath(first, second string) bool {
	firstInfo, firstErr := os.Stat(first)
	secondInfo, secondErr := os.Stat(second)
	if firstErr == nil && secondErr == nil {
		return os.SameFile(firstInfo, secondInfo)
	}
	if !strings.EqualFold(filepath.Base(first), filepath.Base(second)) {
		return false
	}
	firstParent, firstParentErr := os.Stat(filepath.Dir(first))
	secondParent, secondParentErr := os.Stat(filepath.Dir(second))
	return firstParentErr == nil && secondParentErr == nil && os.SameFile(firstParent, secondParent)
}

//nolint:gosec // Test-only COM ABI calls intentionally mirror the audited production wrapper.
func createGuardedDeleteShellLink(linkPath, targetPath string) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	initResult, _, _ := procCoInitializeEx.Call(0, coinitApartment)
	switch initResult {
	case 0, 1:
		defer func() { _, _, _ = procCoUninitialize.Call() }()
	case rpcEChangedMode:
		// COM is already initialized with a different apartment model.
	default:
		if failedHRESULT(initResult) {
			return hresultError("CoInitializeEx", initResult)
		}
	}

	var shellLink unsafe.Pointer
	result, _, _ := procCoCreateInstance.Call(
		uintptr(unsafe.Pointer(&clsidShellLink)),
		0,
		clsctxInprocServer,
		uintptr(unsafe.Pointer(&iidShellLinkW)),
		uintptr(unsafe.Pointer(&shellLink)),
	)
	if failedHRESULT(result) {
		return hresultError("CoCreateInstance(CLSID_ShellLink)", result)
	}
	if shellLink == nil {
		return fmt.Errorf("CoCreateInstance returned a nil IShellLinkW")
	}
	defer comRelease(shellLink)

	targetPathUTF16, err := windows.UTF16PtrFromString(targetPath)
	if err != nil {
		return fmt.Errorf("encode shortcut target path: %w", err)
	}
	result = comCall(shellLink, 20, uintptr(unsafe.Pointer(targetPathUTF16)))
	runtime.KeepAlive(targetPathUTF16)
	if failedHRESULT(result) {
		return hresultError("IShellLinkW.SetPath", result)
	}

	var persistFile unsafe.Pointer
	result = comCall(
		shellLink,
		0,
		uintptr(unsafe.Pointer(&iidPersistFile)),
		uintptr(unsafe.Pointer(&persistFile)),
	)
	if failedHRESULT(result) {
		return hresultError("IShellLinkW.QueryInterface(IPersistFile)", result)
	}
	if persistFile == nil {
		return fmt.Errorf("QueryInterface returned a nil IPersistFile")
	}
	defer comRelease(persistFile)

	linkPathUTF16, err := windows.UTF16PtrFromString(linkPath)
	if err != nil {
		return fmt.Errorf("encode shortcut path: %w", err)
	}
	result = comCall(persistFile, 6, uintptr(unsafe.Pointer(linkPathUTF16)), 1)
	runtime.KeepAlive(linkPathUTF16)
	if failedHRESULT(result) {
		return hresultError("IPersistFile.Save", result)
	}
	return nil
}
func assertPathExists(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); err != nil {
		t.Fatalf("expected %q to exist: %v", path, err)
	}
}

func assertPathMissing(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected %q to be absent, got %v", path, err)
	}
}

func assertSharingViolation(t *testing.T, operation string, err error) {
	t.Helper()
	if !errors.Is(err, windows.ERROR_SHARING_VIOLATION) {
		t.Fatalf("%s error = %v, want sharing violation", operation, err)
	}
}

func assertReplacementBlocked(t *testing.T, err error) {
	t.Helper()
	if !errors.Is(err, windows.ERROR_SHARING_VIOLATION) &&
		!errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		t.Fatalf("replacement error = %v, want sharing violation or access denied", err)
	}
}
