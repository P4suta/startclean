// SPDX-FileCopyrightText: 2026 startclean contributors <https://github.com/P4suta/startclean/graphs/contributors>
// SPDX-License-Identifier: MIT OR Apache-2.0

package core

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type LinkReader interface {
	Target(string) (string, error)
}

type Scanner struct {
	Roots      Roots
	Reader     LinkReader
	Classifier Classifier
}

func (s Scanner) Scan(ctx context.Context, scope Scope) ScanResult {
	var items []Item
	if scope == ScopeUser || scope == ScopeAll {
		items = append(items, s.scanRoot(ctx, ScopeUser, s.Roots.User)...)
	}
	if scope == ScopeCommon || scope == ScopeAll {
		items = append(items, s.scanRoot(ctx, ScopeCommon, s.Roots.Common)...)
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Scope != items[j].Scope {
			return items[i].Scope == ScopeUser
		}
		return strings.ToLower(items[i].LinkPath) < strings.ToLower(items[j].LinkPath)
	})
	return ScanResult{Roots: s.Roots, Summary: Summarize(items), Items: items}
}

func (s Scanner) scanRoot(ctx context.Context, scope Scope, root string) []Item {
	if root == "" {
		return []Item{errorItem(scope, root, ReasonWalkFailure, errors.New("known folder is unavailable"))}
	}
	var items []Item
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if walkErr != nil {
			items = append(items, errorItem(scope, path, walkReason(walkErr), walkErr))
			if entry != nil && entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if path == root {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			items = append(items, errorItem(scope, path, walkReason(err), err))
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			if isReparsePoint(info) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.EqualFold(filepath.Ext(entry.Name()), ".lnk") {
			return nil
		}
		if isReparsePoint(info) {
			items = append(items, Item{
				Scope: scope, LinkPath: path, Classification: ClassificationUnverifiable,
				ReasonCode: ReasonUnsafeLink, ElevationRequired: scope == ScopeCommon,
			})
			return nil
		}

		rawTarget, err := s.Reader.Target(path)
		if err != nil {
			reason := ReasonParseFailure
			if errors.Is(err, fs.ErrPermission) {
				reason = ReasonAccessDenied
			}
			items = append(items, errorItem(scope, path, reason, err))
			return nil
		}
		target, classification, reason, err := s.Classifier.Classify(rawTarget)
		item := Item{
			Scope: scope, LinkPath: path, TargetPath: target, Classification: classification,
			ReasonCode: reason, ElevationRequired: scope == ScopeCommon,
		}
		if err != nil {
			item.Error = err.Error()
		}
		items = append(items, item)
		return nil
	})
	if err != nil && !errors.Is(err, context.Canceled) {
		items = append(items, errorItem(scope, root, walkReason(err), err))
	}
	return items
}

func errorItem(scope Scope, path string, reason ReasonCode, err error) Item {
	message := ""
	if err != nil {
		message = err.Error()
	}
	return Item{
		Scope: scope, LinkPath: path, Classification: ClassificationError,
		ReasonCode: reason, ElevationRequired: scope == ScopeCommon, Error: message,
	}
}

func walkReason(err error) ReasonCode {
	if errors.Is(err, os.ErrPermission) {
		return ReasonAccessDenied
	}
	return ReasonWalkFailure
}
