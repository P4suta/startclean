// SPDX-FileCopyrightText: 2026 startclean contributors <https://github.com/P4suta/startclean/graphs/contributors>
// SPDX-License-Identifier: MIT OR Apache-2.0

package core

import (
	"errors"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

func FuzzInsideRootContainment(f *testing.F) {
	for _, seed := range []struct {
		root string
		path string
	}{
		{"", ""},
		{`C:\Users\例\Start Menu\Programs`, `C:\Users\例\Start Menu\Programs\会社\アプリ.lnk`},
		{`C:\Start\Programs`, `C:\Start\Programs`},
		{`C:\Start\Programs`, `C:\Start\Programs-elsewhere\app.lnk`},
		{`C:\Start\Programs`, `C:\Start\Programs\..\outside.lnk`},
		{`C:\Start\Programs`, `D:\Start\Programs\app.lnk`},
		{`\\server\share\Programs`, `\\server\share\Programs\Vendor\app.lnk`},
		{"relative-root", filepath.Join("relative-root", "子", "app.lnk")},
		{".", filepath.Join(".", "nested", "app.lnk")},
	} {
		f.Add(seed.root, seed.path)
	}

	f.Fuzz(func(t *testing.T, root, path string) {
		if !insideRoot(root, path) {
			return
		}

		absoluteRoot, err := filepath.Abs(root)
		if err != nil {
			t.Fatalf("insideRoot(%q, %q) = true, but Abs(root) failed: %v", root, path, err)
		}
		absolutePath, err := filepath.Abs(path)
		if err != nil {
			t.Fatalf("insideRoot(%q, %q) = true, but Abs(path) failed: %v", root, path, err)
		}
		relative, err := filepath.Rel(absoluteRoot, absolutePath)
		if err != nil {
			t.Fatalf("insideRoot(%q, %q) = true, but Rel failed: %v", root, path, err)
		}

		cleanRelative := filepath.Clean(relative)
		if cleanRelative == "." ||
			cleanRelative == ".." ||
			filepath.IsAbs(cleanRelative) ||
			strings.HasPrefix(cleanRelative, ".."+string(filepath.Separator)) {
			t.Fatalf(
				"insideRoot(%q, %q) = true with escaping relative path %q",
				root,
				path,
				cleanRelative,
			)
		}

	})
}

func FuzzClassifierOnlyMarksDefinitivelyMissingFixedTargetsStale(f *testing.F) {
	for _, seed := range []struct {
		target      string
		driveChoice uint8
		statChoice  uint8
		expandFails bool
	}{
		{"", 0, 1, false},
		{`C:\不存在\アプリ.exe`, 0, 1, false},
		{`C:\existing.exe`, 0, 0, false},
		{`bin\relative.exe`, 0, 1, false},
		{`\\server\share\app.exe`, 0, 1, false},
		{`//server/share/app.exe`, 0, 1, false},
		{`D:\removable.exe`, 1, 1, false},
		{`Z:\network.exe`, 2, 1, false},
		{`%UNKNOWN%\app.exe`, 0, 1, false},
		{`shell:AppsFolder\Example`, 0, 1, false},
		{`::namespace`, 0, 1, false},
		{`C:\denied.exe`, 0, 2, false},
		{`C:\inspection-error.exe`, 0, 3, false},
		{`C:\expansion-error.exe`, 0, 1, true},
	} {
		f.Add(seed.target, seed.driveChoice, seed.statChoice, seed.expandFails)
	}

	f.Fuzz(func(t *testing.T, rawTarget string, driveChoice, statChoice uint8, expandFails bool) {
		driveKind := []DriveKind{DriveFixed, DriveRemovable, DriveNetwork, DriveOther}[driveChoice%4]
		var driveCalls, statCalls int
		classifier := Classifier{
			ExpandEnv: func(value string) (string, error) {
				if expandFails {
					return "", errors.New("injected expansion failure")
				}
				return value, nil
			},
			DriveKind: func(root string) (DriveKind, error) {
				driveCalls++
				if strings.HasPrefix(root, `\\`) {
					return DriveNetwork, nil
				}
				return driveKind, nil
			},
			Stat: func(string) (fs.FileInfo, error) {
				statCalls++
				switch statChoice % 4 {
				case 0:
					return fakeFileInfo{name: "present.exe"}, nil
				case 1:
					return nil, fs.ErrNotExist
				case 2:
					return nil, fs.ErrPermission
				default:
					return nil, errors.New("injected inspection failure")
				}
			},
		}

		target, classification, reason, err := classifier.Classify(rawTarget)
		if classification != ClassificationStale {
			return
		}

		if err != nil || reason != ReasonTargetMissing {
			t.Fatalf("stale result = (%q, %s, %s, %v)", target, classification, reason, err)
		}
		if expandFails || driveKind != DriveFixed || statChoice%4 != 1 {
			t.Fatalf(
				"unsafe stale result for target %q: expandFails=%v drive=%s statChoice=%d",
				rawTarget,
				expandFails,
				driveKind,
				statChoice%4,
			)
		}
		if driveCalls != 1 || statCalls != 1 {
			t.Fatalf("stale result used drive %d time(s) and stat %d time(s)", driveCalls, statCalls)
		}
		if target == "" ||
			!filepath.IsAbs(target) ||
			filepath.VolumeName(target) == "" ||
			strings.HasPrefix(target, `\\`) ||
			unresolvedEnvironment.MatchString(target) {
			t.Fatalf("stale result has unsafe normalized target %q from %q", target, rawTarget)
		}
	})
}
