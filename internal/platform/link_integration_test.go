// SPDX-FileCopyrightText: 2026 startclean contributors <https://github.com/P4suta/startclean/graphs/contributors>
// SPDX-License-Identifier: MIT OR Apache-2.0

//go:build windows && integration

package platform

import (
	"context"
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

func TestReadsRealTemporaryShellLinkWithoutResolving(t *testing.T) {
	temp := t.TempDir()
	target := filepath.Join(temp, "実行可能ファイル.exe")
	link := filepath.Join(temp, "テスト.LNK")
	if err := os.WriteFile(target, []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := createTemporaryShellLink(link, target); err != nil {
		t.Fatalf("create temporary Shell Link: %v", err)
	}
	got, err := New().Target(link)
	if err != nil {
		t.Fatalf("Target() error = %v", err)
	}
	if !strings.EqualFold(filepath.Clean(got), filepath.Clean(target)) {
		t.Fatalf("Target() = %q, want %q", got, target)
	}
}

func TestRealShellLinkEndToEndScanClassifyAndGuardedDelete(t *testing.T) {
	root := t.TempDir()
	vendor := filepath.Join(root, "Unicode ベンダー")
	if err := os.MkdirAll(vendor, 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "missing-target.exe")
	link := filepath.Join(vendor, "壊れたショートカット.LNK")
	if err := createTemporaryShellLink(link, target); err != nil {
		t.Fatalf("create temporary Shell Link: %v", err)
	}

	system := New()
	classifier := core.Classifier{
		Stat:      os.Stat,
		ExpandEnv: system.ExpandEnvironment,
		DriveKind: system.DriveKind,
	}
	result := (core.Scanner{
		Roots:      core.Roots{User: root},
		Reader:     system,
		Classifier: classifier,
	}).Scan(context.Background(), core.ScopeUser)
	if result.Summary.Scanned != 1 || result.Summary.Stale != 1 || len(result.Items) != 1 {
		t.Fatalf("real Shell Link scan result = %+v", result)
	}
	if result.Items[0].ReasonCode != core.ReasonTargetMissing ||
		!strings.EqualFold(filepath.Clean(result.Items[0].TargetPath), filepath.Clean(target)) {
		t.Fatalf("real Shell Link classification = %+v", result.Items[0])
	}

	cleaned, err := (core.Cleaner{
		Roots:      core.Roots{User: root},
		Reader:     system,
		Classifier: classifier,
		FS:         core.OSFS{},
		Remover:    system,
		Elevated:   true,
		Notifier:   system,
	}).Clean(result.Items)
	if err != nil {
		t.Fatalf("clean real Shell Link: %v (result=%+v)", err, cleaned)
	}
	if cleaned.Requested != 1 || cleaned.Deleted != 1 || cleaned.Pruned != 1 || len(cleaned.Errors) != 0 {
		t.Fatalf("real Shell Link cleanup result = %+v", cleaned)
	}
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Fatalf("real Shell Link still exists or stat failed unexpectedly: %v", err)
	}
	if _, err := os.Stat(vendor); !os.IsNotExist(err) {
		t.Fatalf("empty parent directory was not pruned: %v", err)
	}
	if _, err := os.Stat(root); err != nil {
		t.Fatalf("approved root was removed: %v", err)
	}
}

func createTemporaryShellLink(linkPath, targetPath string) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	initResult, _, _ := procCoInitializeEx.Call(0, coinitApartment)
	switch uint32(initResult) {
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
