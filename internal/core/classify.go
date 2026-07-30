// SPDX-FileCopyrightText: 2026 startclean contributors <https://github.com/P4suta/startclean/graphs/contributors>
// SPDX-License-Identifier: MIT OR Apache-2.0

package core

import (
	"errors"
	"io/fs"
	"path/filepath"
	"regexp"
	"strings"
)

type DriveKind string

const (
	DriveFixed     DriveKind = "fixed"
	DriveRemovable DriveKind = "removable"
	DriveNetwork   DriveKind = "network"
	DriveOther     DriveKind = "other"
)

type Classifier struct {
	Stat      func(string) (fs.FileInfo, error)
	ExpandEnv func(string) (string, error)
	DriveKind func(string) (DriveKind, error)
}

var unresolvedEnvironment = regexp.MustCompile(`%[^%]+%`)

func (c Classifier) Classify(rawTarget string) (string, Classification, ReasonCode, error) {
	target := strings.TrimSpace(rawTarget)
	if target == "" {
		return "", ClassificationUnverifiable, ReasonEmptyTarget, nil
	}
	lowerTarget := strings.ToLower(target)
	if strings.HasPrefix(lowerTarget, "shell:") || strings.HasPrefix(target, "::") {
		return target, ClassificationUnverifiable, ReasonShellNamespace, nil
	}

	expanded, err := c.ExpandEnv(target)
	if err != nil {
		return target, ClassificationError, ReasonInspectionFailure, err
	}
	if unresolvedEnvironment.MatchString(expanded) {
		return expanded, ClassificationUnverifiable, ReasonUnresolvedEnv, nil
	}
	target = filepath.Clean(expanded)

	if strings.HasPrefix(target, `\\`) {
		return target, ClassificationUnverifiable, ReasonNetworkTarget, nil
	}
	if !filepath.IsAbs(target) {
		return target, ClassificationUnverifiable, ReasonRelativeTarget, nil
	}

	volume := filepath.VolumeName(target)
	if volume == "" {
		return target, ClassificationUnverifiable, ReasonUnsupportedTarget, nil
	}
	kind, err := c.DriveKind(volume + string(filepath.Separator))
	if err != nil {
		return target, ClassificationError, ReasonInspectionFailure, err
	}
	switch kind {
	case DriveNetwork:
		return target, ClassificationUnverifiable, ReasonNetworkTarget, nil
	case DriveRemovable:
		return target, ClassificationUnverifiable, ReasonRemovableTarget, nil
	case DriveFixed:
	case DriveOther:
		return target, ClassificationUnverifiable, ReasonUnsupportedDrive, nil
	}

	_, err = c.Stat(target)
	switch {
	case err == nil:
		return target, ClassificationHealthy, ReasonTargetExists, nil
	case errors.Is(err, fs.ErrNotExist):
		return target, ClassificationStale, ReasonTargetMissing, nil
	case errors.Is(err, fs.ErrPermission):
		return target, ClassificationError, ReasonAccessDenied, err
	default:
		return target, ClassificationError, ReasonInspectionFailure, err
	}
}
