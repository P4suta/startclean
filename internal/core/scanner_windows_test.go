// SPDX-FileCopyrightText: 2026 startclean contributors <https://github.com/P4suta/startclean/graphs/contributors>
// SPDX-License-Identifier: MIT OR Apache-2.0

//go:build windows

package core

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func TestScannerNeverFollowsReparseDirectories(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	outside := t.TempDir()
	escapedLink := filepath.Join(outside, "escaped.lnk")
	if err := os.WriteFile(escapedLink, []byte("must not be inspected"), 0o600); err != nil {
		t.Fatal(err)
	}
	redirect := filepath.Join(root, "redirect")
	if err := os.Symlink(outside, redirect); err != nil {
		t.Fatalf("create directory reparse point: %v", err)
	}

	result := (Scanner{
		Roots:  Roots{User: root},
		Reader: mapReader{escapedLink: filepath.Join(outside, "missing.exe")},
		Classifier: Classifier{
			ExpandEnv: func(value string) (string, error) { return value, nil },
			DriveKind: func(string) (DriveKind, error) { return DriveFixed, nil },
			Stat:      func(string) (fs.FileInfo, error) { return nil, fs.ErrNotExist },
		},
	}).Scan(context.Background(), ScopeUser)

	if len(result.Items) != 0 || result.Summary.Scanned != 0 {
		t.Fatalf("scanner followed a directory reparse point: %+v", result)
	}
}

func TestScannerRejectsReparseLinkWithoutReadingTarget(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	target := filepath.Join(root, "target.txt")
	if err := os.WriteFile(target, []byte("target"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "alias.lnk")
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("create link reparse point: %v", err)
	}

	result := (Scanner{
		Roots:  Roots{User: root},
		Reader: errorReader{err: fs.ErrPermission},
	}).Scan(context.Background(), ScopeUser)

	if len(result.Items) != 1 || result.Items[0].Classification != ClassificationUnverifiable ||
		result.Items[0].ReasonCode != ReasonUnsafeLink {
		t.Fatalf("reparse .lnk was not rejected before target parsing: %+v", result)
	}
}
