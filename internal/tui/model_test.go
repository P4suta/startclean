// SPDX-FileCopyrightText: 2026 startclean contributors <https://github.com/P4suta/startclean/graphs/contributors>
// SPDX-License-Identifier: MIT OR Apache-2.0

package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/P4suta/startclean/internal/core"
	tea "github.com/charmbracelet/bubbletea"
)

func TestModelSelectionReviewAndConfirmation(t *testing.T) {
	t.Parallel()
	model := loadedModel(true)
	if len(model.selected) != 0 {
		t.Fatal("items must not be selected initially")
	}
	model = updateKey(model, " ")
	if len(model.selected) != 1 {
		t.Fatalf("selected = %d, want 1", len(model.selected))
	}
	model = updateKey(model, "enter")
	if model.state != stateReview {
		t.Fatalf("state = %v, want review", model.state)
	}
	model = updateKey(model, "enter")
	if model.state != stateConfirm {
		t.Fatalf("state = %v, want confirmation", model.state)
	}
	if !strings.Contains(model.View(), "exactly 1 selected shortcut") {
		t.Fatalf("confirmation does not show exact count:\n%s", model.View())
	}
}

func TestModelLocksCommonItemsWithoutElevation(t *testing.T) {
	t.Parallel()
	model := loadedModel(false)
	model.cursor = 1
	model = updateKey(model, " ")
	if len(model.selected) != 0 {
		t.Fatal("locked common item was selected")
	}
	model = updateKey(model, "a")
	if len(model.selected) != 1 || !model.selected[`C:\User\User App.lnk`] {
		t.Fatalf("toggle all selected locked entries: %#v", model.selected)
	}
	if !strings.Contains(model.View(), "LOCKED") || !strings.Contains(model.View(), "Start-Process") {
		t.Fatalf("locked guidance missing:\n%s", model.View())
	}
}

func TestModelNarrowResizeAndCancel(t *testing.T) {
	t.Parallel()
	model := loadedModel(true)
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 35, Height: 12})
	model = updated.(Model)
	if view := model.View(); !strings.Contains(view, "DETAILS") {
		t.Fatalf("narrow view lost details:\n%s", view)
	}
	model = updateKey(model, "esc")
	if !model.cancelled {
		t.Fatal("Esc must cancel without deletion")
	}
}

func TestModelKeepsCleanupResultVisibleUntilAcknowledged(t *testing.T) {
	t.Parallel()
	model := loadedModel(true)
	updated, command := model.Update(cleanMessage{result: core.CleanResult{Requested: 2, Deleted: 2, Pruned: 1}})
	model = updated.(Model)
	if command != nil || model.state != stateDone {
		t.Fatalf("cleanup result should remain visible: state=%v command=%v", model.state, command)
	}
	if view := model.View(); !strings.Contains(view, "Deleted 2 of 2") || !strings.Contains(view, "Enter/q/Esc close") {
		t.Fatalf("cleanup completion view is incomplete:\n%s", view)
	}
	updated, command = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	model = updated.(Model)
	if command == nil || model.cancelled {
		t.Fatal("acknowledging a completed cleanup must quit without marking it cancelled")
	}
}

func loadedModel(elevated bool) Model {
	model := NewModel(context.Background(), Config{
		Elevated: elevated, ElevateHint: "Start-Process -Verb RunAs",
		Clean: func(items []core.Item) (core.CleanResult, error) {
			return core.CleanResult{Requested: len(items), Deleted: len(items)}, nil
		},
	})
	items := []core.Item{
		{Scope: core.ScopeUser, LinkPath: `C:\User\User App.lnk`, TargetPath: `C:\missing-user.exe`, Classification: core.ClassificationStale},
		{Scope: core.ScopeCommon, LinkPath: `C:\Common\Common App.lnk`, TargetPath: `C:\missing-common.exe`, Classification: core.ClassificationStale},
	}
	updated, _ := model.Update(scanMessage{result: core.ScanResult{Summary: core.Summarize(items), Items: items}})
	return updated.(Model)
}

func updateKey(model Model, key string) Model {
	var message tea.KeyMsg
	switch key {
	case " ":
		message = tea.KeyMsg{Type: tea.KeySpace}
	case "enter":
		message = tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		message = tea.KeyMsg{Type: tea.KeyEsc}
	default:
		message = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)}
	}
	updated, _ := model.Update(message)
	return updated.(Model)
}
