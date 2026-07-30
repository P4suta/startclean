// SPDX-FileCopyrightText: 2026 startclean contributors <https://github.com/P4suta/startclean/graphs/contributors>
// SPDX-License-Identifier: MIT OR Apache-2.0

//go:build windows

package platform

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/P4suta/startclean/internal/core"
)

func TestEscapePowerShellSingleQuoted(t *testing.T) {
	t.Parallel()
	input := `C:\Users\O'Brien\startclean.exe`
	want := `C:\Users\O''Brien\startclean.exe`
	if got := EscapePowerShellSingleQuoted(input); got != want {
		t.Fatalf("EscapePowerShellSingleQuoted(%q) = %q, want %q", input, got, want)
	}
}

func TestExpandEnvironmentUsesUnicodeSafeWindowsAPI(t *testing.T) {
	value := `C:\テスト folder\開始`
	t.Setenv("STARTCLEAN_EXPAND_TEST", value)
	got, err := New().ExpandEnvironment(`%STARTCLEAN_EXPAND_TEST%\app.exe`)
	if err != nil {
		t.Fatalf("ExpandEnvironment() error = %v", err)
	}
	want := value + `\app.exe`
	if got != want {
		t.Fatalf("ExpandEnvironment() = %q, want %q", got, want)
	}
}

func TestSystemDriveIsReportedAsFixed(t *testing.T) {
	t.Parallel()
	root := os.Getenv("SystemDrive")
	if root == "" {
		t.Skip("SystemDrive is unavailable")
	}
	root = strings.TrimRight(root, `\/`) + `\`
	kind, err := New().DriveKind(root)
	if err != nil {
		t.Fatalf("DriveKind(%q) error = %v", root, err)
	}
	if kind != core.DriveFixed {
		t.Fatalf("DriveKind(%q) = %s, want %s", root, kind, core.DriveFixed)
	}
}

func TestKnownFolderRootsResolveReadOnly(t *testing.T) {
	t.Parallel()

	system := New()
	roots, err := system.Roots()
	if err != nil {
		t.Fatalf("Roots() error = %v", err)
	}
	for scope, path := range map[string]string{"user": roots.User, "common": roots.Common} {
		if path == "" || !filepath.IsAbs(path) {
			t.Fatalf("%s known folder path = %q, want nonempty absolute path", scope, path)
		}
	}
	if first, second := system.Elevated(), system.Elevated(); first != second {
		t.Fatalf("elevation probe changed without a token change: first=%v second=%v", first, second)
	}
}

func TestHRESULTClassificationAndFormatting(t *testing.T) {
	t.Parallel()
	if failedHRESULT(0) || failedHRESULT(1) {
		t.Fatal("successful HRESULT was classified as failure")
	}
	const accessDenied = uintptr(0x80070005)
	if !failedHRESULT(accessDenied) {
		t.Fatal("failing HRESULT was classified as success")
	}
	if got := hresultError("operation", accessDenied).Error(); got != "operation failed with HRESULT 0x80070005" {
		t.Fatalf("unexpected HRESULT error: %q", got)
	}
}
