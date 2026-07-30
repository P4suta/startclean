// SPDX-FileCopyrightText: 2026 startclean contributors <https://github.com/P4suta/startclean/graphs/contributors>
// SPDX-License-Identifier: MIT OR Apache-2.0

package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/P4suta/startclean/internal/core"
)

func TestScanJSONMatchesVersionOneGolden(t *testing.T) {
	t.Parallel()
	result := core.ScanResult{
		Roots: core.Roots{
			User:   `C:\Users\Alice\Start Menu\Programs`,
			Common: `C:\ProgramData\Start Menu\Programs`,
		},
		Items: []core.Item{
			{
				Scope: core.ScopeUser, LinkPath: `C:\Users\Alice\Start Menu\Programs\日本語.lnk`,
				TargetPath: `C:\Missing\日本語.exe`, Classification: core.ClassificationStale,
				ReasonCode: core.ReasonTargetMissing,
			},
			{
				Scope: core.ScopeCommon, LinkPath: `C:\ProgramData\Start Menu\Programs\Healthy.lnk`,
				TargetPath: `C:\Program Files\Healthy.exe`, Classification: core.ClassificationHealthy,
				ReasonCode: core.ReasonTargetExists, ElevationRequired: true,
			},
		},
	}
	result.Summary = core.Summarize(result.Items)
	var actual bytes.Buffer
	if err := writeJSON(&actual, core.Envelope(result)); err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(filepath.Join("testdata", "scan_v1.json.golden"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(actual.Bytes(), want) {
		t.Fatalf("schema v1 JSON changed\n--- want ---\n%s\n--- got ---\n%s", want, actual.Bytes())
	}
}
