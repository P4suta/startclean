// SPDX-FileCopyrightText: 2026 startclean contributors <https://github.com/P4suta/startclean/graphs/contributors>
// SPDX-License-Identifier: MIT OR Apache-2.0

//go:build windows && integration

package platform

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadsRealTemporaryShellLinkWithoutResolving(t *testing.T) {
	temp := t.TempDir()
	target := filepath.Join(temp, "実行可能ファイル.exe")
	link := filepath.Join(temp, "テスト.LNK")
	scriptPath := filepath.Join(temp, "create-shortcut.ps1")
	if err := os.WriteFile(target, []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	script := `param([string]$LinkPath, [string]$TargetPath); ` +
		`$shell = New-Object -ComObject WScript.Shell; ` +
		`$shortcut = $shell.CreateShortcut($LinkPath); ` +
		`$shortcut.TargetPath = $TargetPath; $shortcut.Save()`
	if err := os.WriteFile(scriptPath, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(
		"powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", scriptPath,
		"-LinkPath", link, "-TargetPath", target,
	)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("create temporary Shell Link: %v\n%s", err, output)
	}
	got, err := New().Target(link)
	if err != nil {
		t.Fatalf("Target() error = %v", err)
	}
	if !strings.EqualFold(filepath.Clean(got), filepath.Clean(target)) {
		t.Fatalf("Target() = %q, want %q", got, target)
	}
}
