// SPDX-FileCopyrightText: 2026 startclean contributors <https://github.com/P4suta/startclean/graphs/contributors>
// SPDX-License-Identifier: MIT OR Apache-2.0

package core

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func TestScannerRecognizesCaseInsensitiveLinkExtensions(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	lower := filepath.Join(root, "lower.lnk")
	upper := filepath.Join(root, "UNICODE.LNK")
	ignored := filepath.Join(root, "website.url")
	for _, path := range []string{lower, upper, ignored} {
		if err := os.WriteFile(path, []byte("fixture"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	targetLower := filepath.Join(root, "missing-lower.exe")
	targetUpper := filepath.Join(root, "見つからない.exe")
	reader := mapReader{lower: targetLower, upper: targetUpper}
	classifier := Classifier{
		ExpandEnv: func(value string) (string, error) { return value, nil },
		DriveKind: func(string) (DriveKind, error) { return DriveFixed, nil },
		Stat:      func(string) (fs.FileInfo, error) { return nil, fs.ErrNotExist },
	}
	result := (Scanner{
		Roots: Roots{User: root}, Reader: reader, Classifier: classifier,
	}).Scan(context.Background(), ScopeUser)
	if result.Summary.Scanned != 2 || result.Summary.Stale != 2 {
		t.Fatalf("unexpected summary: %+v", result.Summary)
	}
	for _, item := range result.Items {
		if item.LinkPath == ignored {
			t.Fatal(".url files must be left out of the scan")
		}
	}
}

func TestScannerMarksUnreadableLinkAsAccessDenied(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	link := filepath.Join(root, "denied.lnk")
	if err := os.WriteFile(link, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	classifier := Classifier{
		ExpandEnv: func(value string) (string, error) { return value, nil },
		DriveKind: func(string) (DriveKind, error) { return DriveFixed, nil },
		Stat:      os.Stat,
	}
	result := (Scanner{
		Roots: Roots{User: root}, Reader: errorReader{err: fs.ErrPermission}, Classifier: classifier,
	}).Scan(context.Background(), ScopeUser)
	if result.Summary.Errors != 1 || result.Items[0].ReasonCode != ReasonAccessDenied {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestScannerReportsUnavailableKnownFolder(t *testing.T) {
	t.Parallel()
	result := (Scanner{}).Scan(context.Background(), ScopeCommon)
	if result.Summary.Errors != 1 || len(result.Items) != 1 {
		t.Fatalf("unexpected result: %+v", result)
	}
	item := result.Items[0]
	if item.Scope != ScopeCommon || item.ReasonCode != ReasonWalkFailure || !item.ElevationRequired {
		t.Fatalf("unexpected unavailable-root item: %+v", item)
	}
}

func TestScannerCancellationIsNotReportedAsAnInspectionError(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "ignored.lnk"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result := (Scanner{Roots: Roots{User: root}}).Scan(ctx, ScopeUser)
	if result.Summary.Errors != 0 || len(result.Items) != 0 {
		t.Fatalf("cancelled scan emitted misleading items: %+v", result)
	}
}

type errorReader struct{ err error }

func (r errorReader) Target(string) (string, error) {
	if r.err == nil {
		return "", errors.New("missing configured error")
	}
	return "", r.err
}

func TestScannerReportsParseFailures(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	link := filepath.Join(root, "bad.lnk")
	if err := os.WriteFile(link, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	classifier := Classifier{
		ExpandEnv: func(value string) (string, error) { return value, nil },
		DriveKind: func(string) (DriveKind, error) { return DriveFixed, nil },
		Stat:      os.Stat,
	}
	result := (Scanner{Roots: Roots{User: root}, Reader: mapReader{}, Classifier: classifier}).
		Scan(context.Background(), ScopeUser)
	if result.Summary.Errors != 1 || result.Items[0].ReasonCode != ReasonParseFailure {
		t.Fatalf("unexpected result: %+v", result)
	}
}
