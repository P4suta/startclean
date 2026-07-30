// SPDX-FileCopyrightText: 2026 startclean contributors <https://github.com/P4suta/startclean/graphs/contributors>
// SPDX-License-Identifier: MIT OR Apache-2.0

package core

import (
	"errors"
	"io/fs"
	"path/filepath"
	"testing"
)

func TestClassifier(t *testing.T) {
	t.Parallel()
	missing := filepath.Clean(`C:\不存在\アプリ.exe`)
	existing := filepath.Clean(`C:\Program Files\Example\example.exe`)
	permission := filepath.Clean(`C:\Private\secret.exe`)

	classifier := Classifier{
		ExpandEnv: func(value string) (string, error) {
			if value == `%APPROOT%\app.exe` {
				return `C:\Apps\app.exe`, nil
			}
			return value, nil
		},
		DriveKind: func(root string) (DriveKind, error) {
			switch root {
			case `D:\`:
				return DriveRemovable, nil
			case `E:\`:
				return DriveOther, nil
			case `Z:\`:
				return DriveNetwork, nil
			default:
				return DriveFixed, nil
			}
		},
		Stat: func(path string) (fs.FileInfo, error) {
			switch filepath.Clean(path) {
			case existing:
				return fakeFileInfo{name: "example.exe"}, nil
			case permission:
				return nil, fs.ErrPermission
			default:
				return nil, fs.ErrNotExist
			}
		},
	}

	tests := []struct {
		name           string
		target         string
		classification Classification
		reason         ReasonCode
	}{
		{"missing Unicode fixed target", missing, ClassificationStale, ReasonTargetMissing},
		{"existing fixed target", existing, ClassificationHealthy, ReasonTargetExists},
		{"expanded environment target", `%APPROOT%\app.exe`, ClassificationStale, ReasonTargetMissing},
		{"unresolved environment target", `%UNKNOWN%\app.exe`, ClassificationUnverifiable, ReasonUnresolvedEnv},
		{"relative target", `bin\app.exe`, ClassificationUnverifiable, ReasonRelativeTarget},
		{"empty target", "", ClassificationUnverifiable, ReasonEmptyTarget},
		{"shell namespace", `shell:AppsFolder\Example`, ClassificationUnverifiable, ReasonShellNamespace},
		{"UNC target", `\\server\share\app.exe`, ClassificationUnverifiable, ReasonNetworkTarget},
		{"removable target", `D:\app.exe`, ClassificationUnverifiable, ReasonRemovableTarget},
		{"mapped network target", `Z:\app.exe`, ClassificationUnverifiable, ReasonNetworkTarget},
		{"unsupported drive type", `E:\app.exe`, ClassificationUnverifiable, ReasonUnsupportedDrive},
		{"stat permission failure", permission, ClassificationError, ReasonAccessDenied},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, classification, reason, _ := classifier.Classify(test.target)
			if classification != test.classification || reason != test.reason {
				t.Fatalf("Classify(%q) = (%s, %s), want (%s, %s)",
					test.target, classification, reason, test.classification, test.reason)
			}
		})
	}
}

func TestClassifierDependencyFailuresHaveStableReasonCodes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		classifier Classifier
		reason     ReasonCode
	}{
		{
			name: "environment expansion failure",
			classifier: Classifier{
				ExpandEnv: func(string) (string, error) { return "", errors.New("expand failed") },
				DriveKind: func(string) (DriveKind, error) { return DriveFixed, nil },
				Stat:      func(string) (fs.FileInfo, error) { return nil, fs.ErrNotExist },
			},
			reason: ReasonInspectionFailure,
		},
		{
			name: "unexpected stat failure",
			classifier: Classifier{
				ExpandEnv: func(value string) (string, error) { return value, nil },
				DriveKind: func(string) (DriveKind, error) { return DriveFixed, nil },
				Stat:      func(string) (fs.FileInfo, error) { return nil, errors.New("I/O failure") },
			},
			reason: ReasonInspectionFailure,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, classification, reason, err := test.classifier.Classify(`C:\app.exe`)
			if err == nil || classification != ClassificationError || reason != test.reason {
				t.Fatalf("Classify() = (%s, %s, %v), want (error, %s, error)", classification, reason, err, test.reason)
			}
		})
	}
}

func TestClassifierDriveFailure(t *testing.T) {
	t.Parallel()
	classifier := Classifier{
		ExpandEnv: func(value string) (string, error) { return value, nil },
		DriveKind: func(string) (DriveKind, error) { return DriveOther, errors.New("drive unavailable") },
		Stat:      func(string) (fs.FileInfo, error) { return nil, fs.ErrNotExist },
	}
	_, classification, reason, err := classifier.Classify(`C:\app.exe`)
	if err == nil || classification != ClassificationError || reason != ReasonInspectionFailure {
		t.Fatalf("unexpected result: (%s, %s, %v)", classification, reason, err)
	}
}
