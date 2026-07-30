// SPDX-FileCopyrightText: 2026 startclean contributors <https://github.com/P4suta/startclean/graphs/contributors>
// SPDX-License-Identifier: MIT OR Apache-2.0

package core

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

var ErrElevationRequired = errors.New("elevation is required for all-users shortcuts")

var (
	ErrDeletionOutsideRoot = errors.New("deletion handle resolves outside the approved root")
	ErrUnsafeDeletionPath  = errors.New("deletion path is a directory, reparse point, or unsupported file")
)

type InspectionFS interface {
	Lstat(string) (fs.FileInfo, error)
}

type OSFS struct{}

func (OSFS) Lstat(path string) (fs.FileInfo, error) { return os.Lstat(path) }

// GuardedRemover owns the platform-specific, handle-based mutation boundary.
// DeleteValidated must hold the exact candidate file against rename or
// replacement, prove its final path is inside root, call validate while that
// handle remains locked, and delete through that handle or an identity-matched
// handle reopened fail-closed after releasing a validation-only lock.
type GuardedRemover interface {
	DeleteValidated(root, path string, validate func() error) error
	RemoveEmptyDirectory(root, path string) (bool, error)
}

type ShellNotifier interface {
	Deleted(string)
	DirectoryRemoved(string)
}

type NopNotifier struct{}

func (NopNotifier) Deleted(string)          {}
func (NopNotifier) DirectoryRemoved(string) {}

type Cleaner struct {
	Roots      Roots
	Reader     LinkReader
	Classifier Classifier
	FS         InspectionFS
	Remover    GuardedRemover
	Elevated   bool
	Notifier   ShellNotifier
}

type CleanError struct {
	LinkPath   string     `json:"link_path"`
	ReasonCode ReasonCode `json:"reason_code"`
	Error      string     `json:"error"`
}

type CleanResult struct {
	Requested int          `json:"requested"`
	Deleted   int          `json:"deleted"`
	Pruned    int          `json:"pruned_directories"`
	Errors    []CleanError `json:"errors,omitempty"`
}

func (c Cleaner) Clean(items []Item) (CleanResult, error) {
	result := CleanResult{Requested: len(items)}
	if len(items) == 0 {
		return result, nil
	}
	for _, item := range items {
		if item.Scope == ScopeCommon && !c.Elevated {
			return result, ErrElevationRequired
		}
	}
	if c.Remover == nil {
		return result, errors.New("safe handle-based deletion service is unavailable")
	}

	// Validate the entire selection before the first mutation. This prevents a
	// changed shortcut from causing a surprising partial cleanup.
	for _, item := range items {
		if err := c.revalidate(item); err != nil {
			result.Errors = append(result.Errors, CleanError{
				LinkPath: item.LinkPath, ReasonCode: reasonFromValidation(err), Error: err.Error(),
			})
		}
	}
	if len(result.Errors) > 0 {
		return result, errors.New("selection changed or failed safety validation; nothing was deleted")
	}

	notifier := c.Notifier
	if notifier == nil {
		notifier = NopNotifier{}
	}
	for _, item := range items {
		root := c.Roots.For(item.Scope)
		err := c.Remover.DeleteValidated(root, item.LinkPath, func() error {
			// This second validation happens while the platform holds the exact
			// file handle without FILE_SHARE_DELETE. It closes the gap between
			// selection-wide preflight and the irreversible mutation.
			return c.revalidate(item)
		})
		if err != nil {
			result.Errors = append(result.Errors, CleanError{
				LinkPath: item.LinkPath, ReasonCode: reasonFromDeletion(err), Error: err.Error(),
			})
			// Stop after the first guarded failure. Earlier items may already be
			// gone, but no later path should be mutated after safety state changes.
			break
		}
		result.Deleted++
		notifier.Deleted(item.LinkPath)
		pruned, errs := c.prune(filepath.Dir(item.LinkPath), root, notifier)
		result.Pruned += pruned
		result.Errors = append(result.Errors, errs...)
	}
	if len(result.Errors) > 0 {
		return result, errors.New("cleanup completed with operational errors")
	}
	return result, nil
}

type validationError struct {
	reason ReasonCode
	err    error
}

func (e validationError) Error() string { return e.err.Error() }
func (e validationError) Unwrap() error { return e.err }

func reasonFromValidation(err error) ReasonCode {
	var validation validationError
	if errors.As(err, &validation) {
		return validation.reason
	}
	return ReasonChangedSinceScan
}

func reasonFromDeletion(err error) ReasonCode {
	switch {
	case errors.Is(err, ErrDeletionOutsideRoot):
		return ReasonOutsideApprovedRoot
	case errors.Is(err, ErrUnsafeDeletionPath):
		return ReasonUnsafeLink
	}
	var validation validationError
	if errors.As(err, &validation) {
		return validation.reason
	}
	return ReasonDeleteFailure
}

func (c Cleaner) revalidate(item Item) error {
	root := c.Roots.For(item.Scope)
	if !insideRoot(root, item.LinkPath) || !strings.EqualFold(filepath.Ext(item.LinkPath), ".lnk") {
		return validationError{ReasonOutsideApprovedRoot, fmt.Errorf("%q is not a .lnk inside its approved root", item.LinkPath)}
	}
	if item.Classification != ClassificationStale {
		return validationError{ReasonChangedSinceScan, fmt.Errorf("%q is not a stale scan result", item.LinkPath)}
	}
	info, err := c.FS.Lstat(item.LinkPath)
	if err != nil {
		return validationError{ReasonChangedSinceScan, fmt.Errorf("re-open link: %w", err)}
	}
	if info.IsDir() || isReparsePoint(info) || info.Mode()&fs.ModeSymlink != 0 {
		return validationError{ReasonUnsafeLink, fmt.Errorf("%q is a directory or reparse point", item.LinkPath)}
	}
	target, err := c.Reader.Target(item.LinkPath)
	if err != nil {
		return validationError{ReasonChangedSinceScan, fmt.Errorf("reload link: %w", err)}
	}
	expanded, classification, _, err := c.Classifier.Classify(target)
	if err != nil {
		return validationError{ReasonChangedSinceScan, fmt.Errorf("recheck target: %w", err)}
	}
	if classification != ClassificationStale || !samePath(expanded, item.TargetPath) {
		return validationError{ReasonChangedSinceScan, fmt.Errorf("shortcut target changed or is no longer stale")}
	}
	return nil
}

func (c Cleaner) prune(start, root string, notifier ShellNotifier) (int, []CleanError) {
	var count int
	var cleanErrors []CleanError
	for current := filepath.Clean(start); insideRoot(root, current) && !samePath(current, root); current = filepath.Dir(current) {
		removed, err := c.Remover.RemoveEmptyDirectory(root, current)
		if err != nil {
			cleanErrors = append(cleanErrors, CleanError{LinkPath: current, ReasonCode: ReasonPruneFailure, Error: err.Error()})
			break
		}
		if !removed {
			break
		}
		count++
		notifier.DirectoryRemoved(current)
	}
	return count, cleanErrors
}

func insideRoot(root, path string) bool {
	if root == "" || path == "" {
		return false
	}
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	absolutePath, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	relative, err := filepath.Rel(absoluteRoot, absolutePath)
	if err != nil || relative == "." {
		return false
	}
	return relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative)
}

func samePath(left, right string) bool {
	return strings.EqualFold(filepath.Clean(left), filepath.Clean(right))
}
