// SPDX-FileCopyrightText: 2026 startclean contributors <https://github.com/P4suta/startclean/graphs/contributors>
// SPDX-License-Identifier: MIT OR Apache-2.0

package core

import (
	"fmt"
	"strings"
)

type Scope string

const (
	ScopeUser   Scope = "user"
	ScopeCommon Scope = "common"
	ScopeAll    Scope = "all"
)

func ParseScope(value string) (Scope, error) {
	switch Scope(strings.ToLower(value)) {
	case ScopeUser:
		return ScopeUser, nil
	case ScopeCommon:
		return ScopeCommon, nil
	case ScopeAll:
		return ScopeAll, nil
	default:
		return "", fmt.Errorf("invalid scope %q (want user, common, or all)", value)
	}
}

type Classification string

const (
	ClassificationStale        Classification = "stale"
	ClassificationHealthy      Classification = "healthy"
	ClassificationUnverifiable Classification = "unverifiable"
	ClassificationError        Classification = "error"
)

type ReasonCode string

const (
	ReasonTargetMissing       ReasonCode = "target_missing"
	ReasonTargetExists        ReasonCode = "target_exists"
	ReasonEmptyTarget         ReasonCode = "empty_target"
	ReasonRelativeTarget      ReasonCode = "relative_target"
	ReasonUnresolvedEnv       ReasonCode = "unresolved_environment"
	ReasonShellNamespace      ReasonCode = "shell_namespace"
	ReasonNetworkTarget       ReasonCode = "network_target"
	ReasonRemovableTarget     ReasonCode = "removable_target"
	ReasonUnsupportedDrive    ReasonCode = "unsupported_drive"
	ReasonUnsupportedTarget   ReasonCode = "unsupported_target"
	ReasonParseFailure        ReasonCode = "parse_failure"
	ReasonAccessDenied        ReasonCode = "access_denied"
	ReasonInspectionFailure   ReasonCode = "inspection_failure"
	ReasonWalkFailure         ReasonCode = "walk_failure"
	ReasonChangedSinceScan    ReasonCode = "changed_since_scan"
	ReasonOutsideApprovedRoot ReasonCode = "outside_approved_root"
	ReasonUnsafeLink          ReasonCode = "unsafe_link"
	ReasonDeleteFailure       ReasonCode = "delete_failure"
	ReasonPruneFailure        ReasonCode = "prune_failure"
)

type Item struct {
	Scope             Scope          `json:"scope"`
	LinkPath          string         `json:"link_path"`
	TargetPath        string         `json:"target_path,omitempty"`
	Classification    Classification `json:"classification"`
	ReasonCode        ReasonCode     `json:"reason_code"`
	ElevationRequired bool           `json:"elevation_required"`
	Error             string         `json:"error,omitempty"`
}

func (i Item) Eligible() bool {
	return i.Classification == ClassificationStale
}

type Summary struct {
	Scanned      int `json:"scanned"`
	Stale        int `json:"stale"`
	Healthy      int `json:"healthy"`
	Unverifiable int `json:"unverifiable"`
	Errors       int `json:"errors"`
	UserStale    int `json:"user_stale"`
	CommonStale  int `json:"common_stale"`
}

func Summarize(items []Item) Summary {
	var summary Summary
	for _, item := range items {
		summary.Scanned++
		switch item.Classification {
		case ClassificationStale:
			summary.Stale++
			if item.Scope == ScopeCommon {
				summary.CommonStale++
			} else {
				summary.UserStale++
			}
		case ClassificationHealthy:
			summary.Healthy++
		case ClassificationUnverifiable:
			summary.Unverifiable++
		case ClassificationError:
			summary.Errors++
		}
	}
	return summary
}

type ScanResult struct {
	Roots   Roots   `json:"roots"`
	Summary Summary `json:"summary"`
	Items   []Item  `json:"items"`
}

type Roots struct {
	User   string `json:"user"`
	Common string `json:"common"`
}

func (r Roots) For(scope Scope) string {
	if scope == ScopeCommon {
		return r.Common
	}
	return r.User
}

type JSONEnvelope[T any] struct {
	SchemaVersion int `json:"schema_version"`
	Data          T   `json:"data"`
}

func Envelope[T any](data T) JSONEnvelope[T] {
	return JSONEnvelope[T]{SchemaVersion: 1, Data: data}
}
