// SPDX-FileCopyrightText: 2026 startclean contributors <https://github.com/P4suta/startclean/graphs/contributors>
// SPDX-License-Identifier: MIT OR Apache-2.0

package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/P4suta/startclean/internal/core"
	"github.com/spf13/cobra"
)

func TestExplorerMousetrapIsDisabled(t *testing.T) {
	t.Parallel()
	if cobra.MousetrapHelpText != "" {
		t.Fatal("Cobra mousetrap must stay disabled so Explorer double-click reaches the TUI")
	}
}

func TestScanJSONSchemaEnvelope(t *testing.T) {
	t.Parallel()
	value := core.Envelope(core.ScanResult{
		Summary: core.Summary{Scanned: 1, Stale: 1},
		Items: []core.Item{{
			Scope: core.ScopeUser, LinkPath: `C:\Start\App.lnk`, TargetPath: `C:\Missing\App.exe`,
			Classification: core.ClassificationStale, ReasonCode: core.ReasonTargetMissing,
		}},
	})
	var output bytes.Buffer
	if err := writeJSON(&output, value); err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(output.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["schema_version"] != float64(1) {
		t.Fatalf("schema version = %#v", decoded["schema_version"])
	}
	data := decoded["data"].(map[string]any)
	item := data["items"].([]any)[0].(map[string]any)
	for _, field := range []string{"scope", "link_path", "target_path", "classification", "reason_code", "elevation_required"} {
		if _, ok := item[field]; !ok {
			t.Errorf("JSON item is missing %q: %s", field, output.String())
		}
	}
}

func TestScanTableIsColorless(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	result := core.ScanResult{
		Summary: core.Summary{Scanned: 1, Stale: 1},
		Items: []core.Item{{
			Scope: core.ScopeUser, LinkPath: `C:\Start\日本語.lnk`, TargetPath: `C:\Missing\日本語.exe`,
			Classification: core.ClassificationStale, ReasonCode: core.ReasonTargetMissing,
		}},
	}
	if err := writeScanTable(&output, result, false); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), "\x1b[") {
		t.Fatalf("table contains ANSI escapes: %q", output.String())
	}
	if !strings.Contains(output.String(), "日本語") {
		t.Fatalf("Unicode path missing: %s", output.String())
	}
}

func TestInvalidCommandUsesUsageExitCode(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	code, err := Execute([]string{"definitely-not-a-command"}, strings.NewReader(""), &stdout, &stderr)
	if err == nil || code != ExitUsage {
		t.Fatalf("Execute() = (%d, %v), want (%d, error)", code, err, ExitUsage)
	}
}
