// SPDX-FileCopyrightText: 2026 startclean contributors <https://github.com/P4suta/startclean/graphs/contributors>
// SPDX-License-Identifier: MIT OR Apache-2.0

//go:build windows

package platform

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/P4suta/startclean/internal/core"
	"golang.org/x/sys/windows"
)

const guardedFinalPathLimit = 32768

var _ core.GuardedRemover = (*System)(nil)

func (*System) DeleteValidated(root, path string, validate func() error) error {
	if validate == nil {
		return fmt.Errorf("%w: validation callback is nil", core.ErrUnsafeDeletionPath)
	}
	rootPath, candidatePath, err := guardedAbsoluteChild(root, path)
	if err != nil {
		return err
	}
	if !strings.EqualFold(filepath.Ext(candidatePath), ".lnk") {
		return fmt.Errorf("%w: %q is not a .lnk file", core.ErrUnsafeDeletionPath, candidatePath)
	}

	rootHandle, err := openGuardedHandle(
		rootPath,
		windows.FILE_READ_ATTRIBUTES|windows.FILE_LIST_DIRECTORY,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE,
		windows.FILE_FLAG_BACKUP_SEMANTICS,
	)
	if err != nil {
		return fmt.Errorf("open approved root %q: %w", rootPath, err)
	}
	defer func() { _ = windows.CloseHandle(rootHandle) }()
	if err := requireGuardedRoot(rootHandle, rootPath); err != nil {
		return err
	}

	ancestorHandles, err := lockGuardedAncestors(rootPath, candidatePath, rootHandle)
	if err != nil {
		return err
	}
	defer closeGuardedHandles(ancestorHandles)

	// Holding DELETE access would make IPersistFile.Load fail its symmetric
	// sharing check. A data-read handle with share-read only still blocks writes,
	// renames, and replacement while allowing the callback to reload the link.
	validationHandle, err := openGuardedHandle(
		candidatePath,
		windows.FILE_READ_DATA|windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ,
		windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT,
	)
	if err != nil {
		return fmt.Errorf("lock guarded shortcut %q for validation: %w", candidatePath, err)
	}
	validationOpen := true
	defer func() {
		if validationOpen {
			_ = windows.CloseHandle(validationHandle)
		}
	}()
	identity, err := guardedLeafIdentity(validationHandle, candidatePath, false)
	if err != nil {
		return err
	}
	if err := requireFinalChild(rootHandle, validationHandle); err != nil {
		return err
	}

	if err := validate(); err != nil {
		return fmt.Errorf("validate guarded shortcut %q: %w", candidatePath, err)
	}
	if err := requireFinalChild(rootHandle, validationHandle); err != nil {
		return err
	}
	// Keep the validated object alive across the unavoidable close/re-open gap.
	// FILE_READ_DATA makes the missing share-write bit effective, while
	// FILE_SHARE_DELETE permits the later DELETE handle to coexist.
	anchorHandle, err := openGuardedHandle(
		candidatePath,
		windows.FILE_READ_DATA|windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_DELETE,
		windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT,
	)
	if err != nil {
		return fmt.Errorf("anchor guarded shortcut identity %q: %w", candidatePath, err)
	}
	// Registered before the DELETE handle's defer, so the anchor closes last
	// and the validated file ID cannot be recycled during the gap.
	defer func() { _ = windows.CloseHandle(anchorHandle) }()
	anchorIdentity, err := guardedLeafIdentity(anchorHandle, candidatePath, false)
	if err != nil {
		return err
	}
	if anchorIdentity != identity {
		return fmt.Errorf("%w: %q changed identity before anchoring", core.ErrUnsafeDeletionPath, candidatePath)
	}
	if err := requireFinalChild(rootHandle, anchorHandle); err != nil {
		return err
	}

	// Windows cannot add DELETE access to an existing handle. Re-open only
	// after anchoring, then require that same strong file identity before using
	// the second handle for the irreversible disposition operation.
	if err := windows.CloseHandle(validationHandle); err != nil {
		return fmt.Errorf("release validation lock for %q: %w", candidatePath, err)
	}
	validationOpen = false

	deleteHandle, err := openGuardedHandle(
		candidatePath,
		windows.DELETE|windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE,
		windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT,
	)
	if err != nil {
		return fmt.Errorf("re-open guarded shortcut %q for deletion: %w", candidatePath, err)
	}
	defer func() { _ = windows.CloseHandle(deleteHandle) }()
	deleteIdentity, err := guardedLeafIdentity(deleteHandle, candidatePath, false)
	if err != nil {
		return err
	}
	if deleteIdentity != anchorIdentity {
		return fmt.Errorf("%w: %q changed identity after validation", core.ErrUnsafeDeletionPath, candidatePath)
	}
	if err := requireFinalChild(rootHandle, deleteHandle); err != nil {
		return err
	}
	if err := setDeleteDisposition(deleteHandle); err != nil {
		return fmt.Errorf("delete guarded shortcut %q: %w", candidatePath, err)
	}
	return nil
}

func (*System) RemoveEmptyDirectory(root, path string) (bool, error) {
	rootPath, candidatePath, err := guardedAbsoluteChild(root, path)
	if err != nil {
		return false, err
	}

	rootHandle, err := openGuardedHandle(
		rootPath,
		windows.FILE_READ_ATTRIBUTES|windows.FILE_LIST_DIRECTORY,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE,
		windows.FILE_FLAG_BACKUP_SEMANTICS,
	)
	if err != nil {
		return false, fmt.Errorf("open approved root %q: %w", rootPath, err)
	}
	defer func() { _ = windows.CloseHandle(rootHandle) }()
	if err := requireGuardedRoot(rootHandle, rootPath); err != nil {
		return false, err
	}

	ancestorHandles, err := lockGuardedAncestors(rootPath, candidatePath, rootHandle)
	if err != nil {
		return false, err
	}
	defer closeGuardedHandles(ancestorHandles)

	candidateHandle, err := openGuardedHandle(
		candidatePath,
		windows.DELETE|windows.FILE_READ_ATTRIBUTES|windows.FILE_LIST_DIRECTORY,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE,
		windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT,
	)
	if err != nil {
		return false, fmt.Errorf("open guarded directory %q: %w", candidatePath, err)
	}
	candidateFile := os.NewFile(uintptr(candidateHandle), candidatePath)
	if candidateFile == nil {
		_ = windows.CloseHandle(candidateHandle)
		return false, fmt.Errorf("own guarded directory handle %q", candidatePath)
	}
	defer func() { _ = candidateFile.Close() }()
	if _, err := guardedLeafIdentity(candidateHandle, candidatePath, true); err != nil {
		return false, err
	}
	if err := requireFinalChild(rootHandle, candidateHandle); err != nil {
		return false, err
	}

	empty, err := guardedDirectoryIsEmpty(candidateFile)
	if err != nil {
		return false, fmt.Errorf("enumerate guarded directory %q: %w", candidatePath, err)
	}
	if !empty {
		return false, nil
	}
	if err := requireFinalChild(rootHandle, candidateHandle); err != nil {
		return false, err
	}
	if err := setDeleteDisposition(candidateHandle); err != nil {
		if errors.Is(err, windows.ERROR_DIR_NOT_EMPTY) {
			return false, nil
		}
		return false, fmt.Errorf("delete guarded directory %q: %w", candidatePath, err)
	}
	return true, nil
}

func lockGuardedAncestors(rootPath, candidatePath string, rootHandle windows.Handle) ([]windows.Handle, error) {
	parentPath := filepath.Dir(candidatePath)
	relative, err := filepath.Rel(rootPath, parentPath)
	if err != nil {
		return nil, fmt.Errorf("%w: compare candidate parent %q with root %q: %w", core.ErrDeletionOutsideRoot, parentPath, rootPath, err)
	}
	if relative == "." {
		return nil, nil
	}
	if relative == ".." ||
		filepath.IsAbs(relative) ||
		strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return nil, fmt.Errorf("%w: candidate parent %q is outside %q", core.ErrDeletionOutsideRoot, parentPath, rootPath)
	}

	handles := make([]windows.Handle, 0, strings.Count(relative, string(filepath.Separator))+1)
	current := rootPath
	for component := range strings.SplitSeq(relative, string(filepath.Separator)) {
		if component == "" || component == "." || component == ".." {
			closeGuardedHandles(handles)
			return nil, fmt.Errorf("%w: unsafe ancestor component %q", core.ErrDeletionOutsideRoot, component)
		}
		current = filepath.Join(current, component)
		handle, openErr := openGuardedHandle(
			current,
			windows.FILE_READ_ATTRIBUTES|windows.FILE_LIST_DIRECTORY,
			windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE,
			windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT,
		)
		if openErr != nil {
			closeGuardedHandles(handles)
			return nil, fmt.Errorf("lock guarded ancestor %q: %w", current, openErr)
		}
		if _, inspectErr := guardedLeafIdentity(handle, current, true); inspectErr != nil {
			_ = windows.CloseHandle(handle)
			closeGuardedHandles(handles)
			if errors.Is(inspectErr, core.ErrUnsafeDeletionPath) {
				return nil, fmt.Errorf("%w: reject guarded ancestor %q: %w", core.ErrDeletionOutsideRoot, current, inspectErr)
			}
			return nil, inspectErr
		}
		if containmentErr := requireFinalChild(rootHandle, handle); containmentErr != nil {
			_ = windows.CloseHandle(handle)
			closeGuardedHandles(handles)
			return nil, containmentErr
		}
		handles = append(handles, handle)
	}
	return handles, nil
}

func closeGuardedHandles(handles []windows.Handle) {
	for _, handle := range slices.Backward(handles) {
		_ = windows.CloseHandle(handle)
	}
}
func guardedAbsoluteChild(root, path string) (string, string, error) {
	if root == "" || path == "" {
		return "", "", fmt.Errorf("%w: root and candidate paths must be non-empty", core.ErrDeletionOutsideRoot)
	}
	rootPath, err := filepath.Abs(root)
	if err != nil {
		return "", "", fmt.Errorf("%w: resolve root %q: %w", core.ErrDeletionOutsideRoot, root, err)
	}
	candidatePath, err := filepath.Abs(path)
	if err != nil {
		return "", "", fmt.Errorf("%w: resolve candidate %q: %w", core.ErrDeletionOutsideRoot, path, err)
	}
	rootPath = filepath.Clean(rootPath)
	candidatePath = filepath.Clean(candidatePath)
	if err := guardedChildRelation(rootPath, candidatePath); err != nil {
		return "", "", err
	}
	return rootPath, candidatePath, nil
}

func guardedChildRelation(root, candidate string) error {
	relative, err := filepath.Rel(filepath.Clean(root), filepath.Clean(candidate))
	if err != nil {
		return fmt.Errorf("%w: compare %q with root %q: %w", core.ErrDeletionOutsideRoot, candidate, root, err)
	}
	if relative == "." {
		return fmt.Errorf("%w: refusing to delete approved root %q", core.ErrUnsafeDeletionPath, root)
	}
	if relative == ".." ||
		filepath.IsAbs(relative) ||
		strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return fmt.Errorf("%w: %q is outside %q", core.ErrDeletionOutsideRoot, candidate, root)
	}
	return nil
}

func openGuardedHandle(path string, access, share, flags uint32) (windows.Handle, error) {
	encoded, err := windows.UTF16PtrFromString(extendedWindowsPath(path))
	if err != nil {
		return windows.InvalidHandle, fmt.Errorf("encode path: %w", err)
	}
	return windows.CreateFile(
		encoded,
		access,
		share,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_ATTRIBUTE_NORMAL|flags,
		0,
	)
}

func extendedWindowsPath(path string) string {
	switch {
	case strings.HasPrefix(path, `\\?\`), strings.HasPrefix(path, `\\.\`):
		return path
	case strings.HasPrefix(path, `\\`):
		return `\\?\UNC\` + strings.TrimPrefix(path, `\\`)
	default:
		return `\\?\` + path
	}
}

func requireGuardedRoot(handle windows.Handle, path string) error {
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		return fmt.Errorf("inspect approved root %q: %w", path, err)
	}
	fileType, err := windows.GetFileType(handle)
	if err != nil {
		return fmt.Errorf("inspect approved root type %q: %w", path, err)
	}
	if fileType != windows.FILE_TYPE_DISK ||
		info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0 {
		return fmt.Errorf("%w: approved root %q is not a filesystem directory", core.ErrUnsafeDeletionPath, path)
	}
	return nil
}

type guardedFileIdentity struct {
	modern       bool
	volumeSerial uint64
	fileID       [16]byte
	legacyVolume uint32
	legacyIndex  uint64
}

func guardedLeafIdentity(handle windows.Handle, path string, wantDirectory bool) (guardedFileIdentity, error) {
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		return guardedFileIdentity{}, fmt.Errorf("inspect guarded path %q: %w", path, err)
	}
	fileType, err := windows.GetFileType(handle)
	if err != nil {
		return guardedFileIdentity{}, fmt.Errorf("inspect guarded path type %q: %w", path, err)
	}
	isDirectory := info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0
	isUnsafe := fileType != windows.FILE_TYPE_DISK ||
		info.FileAttributes&(windows.FILE_ATTRIBUTE_REPARSE_POINT|windows.FILE_ATTRIBUTE_DEVICE) != 0 ||
		isDirectory != wantDirectory ||
		(!wantDirectory && info.NumberOfLinks != 1)
	if isUnsafe {
		kind := "regular file"
		if wantDirectory {
			kind = "directory"
		}
		return guardedFileIdentity{}, fmt.Errorf(
			"%w: %q is not an unambiguous non-reparse %s",
			core.ErrUnsafeDeletionPath,
			path,
			kind,
		)
	}
	identity, err := fileIdentity(handle, info)
	if err != nil {
		return guardedFileIdentity{}, fmt.Errorf("read guarded path identity %q: %w", path, err)
	}
	return identity, nil
}

func fileIdentity(handle windows.Handle, legacy windows.ByHandleFileInformation) (guardedFileIdentity, error) {
	var buffer [24]byte
	err := windows.GetFileInformationByHandleEx(handle, windows.FileIdInfo, &buffer[0], 24)
	if err == nil {
		identity := guardedFileIdentity{
			modern:       true,
			volumeSerial: binary.LittleEndian.Uint64(buffer[:8]),
		}
		copy(identity.fileID[:], buffer[8:])
		return identity, nil
	}
	if !errors.Is(err, windows.ERROR_INVALID_PARAMETER) &&
		!errors.Is(err, windows.ERROR_NOT_SUPPORTED) {
		return guardedFileIdentity{}, err
	}
	// FileIdInfo is available on modern local filesystems. Keep a fail-closed
	// legacy identity for older filesystem drivers that explicitly reject it.
	return guardedFileIdentity{
		legacyVolume: legacy.VolumeSerialNumber,
		legacyIndex: uint64(legacy.FileIndexHigh)<<32 |
			uint64(legacy.FileIndexLow),
	}, nil
}

func requireFinalChild(rootHandle, candidateHandle windows.Handle) error {
	rootPath, err := finalPathName(rootHandle)
	if err != nil {
		return fmt.Errorf("resolve approved root handle: %w", err)
	}
	candidatePath, err := finalPathName(candidateHandle)
	if err != nil {
		return fmt.Errorf("resolve candidate handle: %w", err)
	}
	return guardedChildRelation(rootPath, candidatePath)
}

func finalPathName(handle windows.Handle) (string, error) {
	size := uint32(512)
	for {
		if size > guardedFinalPathLimit+1 {
			return "", fmt.Errorf("final path exceeds %d UTF-16 code units", guardedFinalPathLimit)
		}
		buffer := make([]uint16, size)
		length, err := windows.GetFinalPathNameByHandle(handle, &buffer[0], size, 0)
		if err != nil {
			return "", err
		}
		if length < size {
			return windows.UTF16ToString(buffer[:length]), nil
		}
		size = length + 1
	}
}

func guardedDirectoryIsEmpty(directory *os.File) (bool, error) {
	entries, err := directory.ReadDir(1)
	if err != nil && !errors.Is(err, io.EOF) {
		return false, err
	}
	return len(entries) == 0, nil
}

func setDeleteDisposition(handle windows.Handle) error {
	deleteFile := byte(1)
	return windows.SetFileInformationByHandle(
		handle,
		windows.FileDispositionInfo,
		&deleteFile,
		1,
	)
}
