// SPDX-FileCopyrightText: 2026 startclean contributors <https://github.com/P4suta/startclean/graphs/contributors>
// SPDX-License-Identifier: MIT OR Apache-2.0

package tui

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	"github.com/P4suta/startclean/internal/core"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-runewidth"
)

func TestModelEnterWithoutSelectionStaysInBrowse(t *testing.T) {
	t.Parallel()
	model := loadedModel(true)

	model, command := updateKeyWithCommand(model, "enter")

	if command != nil {
		t.Fatalf("Enter without a selection returned command %T", command)
	}
	if model.state != stateBrowse {
		t.Fatalf("state = %v, want browse", model.state)
	}
	if len(model.selected) != 0 {
		t.Fatalf("selection changed: %#v", model.selected)
	}
}

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

func TestModelBackspaceReturnsFromReviewWithSelection(t *testing.T) {
	t.Parallel()
	model := loadedModel(true)
	model = updateKey(model, " ")
	model = updateKey(model, "enter")

	model = updateKey(model, "backspace")

	if model.state != stateBrowse {
		t.Fatalf("state = %v, want browse", model.state)
	}
	selected := model.selectedItems()
	if len(selected) != 1 || selected[0].LinkPath != `C:\User\User App.lnk` {
		t.Fatalf("selection was not preserved: %#v", selected)
	}
}

func TestModelDeclinesConfirmationWithoutCleaning(t *testing.T) {
	t.Parallel()
	for _, key := range []string{"n", "enter"} {
		t.Run(key, func(t *testing.T) {
			t.Parallel()
			cleanCalls := 0
			model := loadedModel(true)
			model.config.Clean = func(items []core.Item) (core.CleanResult, error) {
				cleanCalls++
				return core.CleanResult{Requested: len(items)}, nil
			}
			model = updateKey(model, " ")
			model = updateKey(model, "enter")
			model = updateKey(model, "enter")

			model, command := updateKeyWithCommand(model, key)

			if command != nil {
				t.Fatalf("%q returned cleanup command %T", key, command)
			}
			if cleanCalls != 0 {
				t.Fatalf("Clean called %d time(s), want 0", cleanCalls)
			}
			if model.state != stateBrowse {
				t.Fatalf("state = %v, want browse", model.state)
			}
			selected := model.selectedItems()
			if len(selected) != 1 || selected[0].LinkPath != `C:\User\User App.lnk` {
				t.Fatalf("selection was not preserved: %#v", selected)
			}
		})
	}
}

func TestModelYesCleansExactlySelectedItems(t *testing.T) {
	t.Parallel()
	var cleanedItems []core.Item
	cleanCalls := 0
	model := loadedModel(true)
	model.config.Clean = func(items []core.Item) (core.CleanResult, error) {
		cleanCalls++
		cleanedItems = append([]core.Item(nil), items...)
		return core.CleanResult{Requested: len(items), Deleted: len(items)}, nil
	}
	model = updateKey(model, "down")
	model = updateKey(model, " ")
	wantItems := append([]core.Item(nil), model.selectedItems()...)
	model = updateKey(model, "enter")
	model = updateKey(model, "enter")

	model, command := updateKeyWithCommand(model, "y")

	if model.state != stateCleaning {
		t.Fatalf("state = %v, want cleaning", model.state)
	}
	if cleanCalls != 0 {
		t.Fatalf("Clean ran before its command was executed: %d call(s)", cleanCalls)
	}
	message := executeCleanupCommand(t, command)
	if message.err != nil {
		t.Fatalf("cleanup command error = %v", message.err)
	}
	if cleanCalls != 1 {
		t.Fatalf("Clean called %d time(s), want 1", cleanCalls)
	}
	if !reflect.DeepEqual(cleanedItems, wantItems) {
		t.Fatalf("Clean items = %#v, want %#v", cleanedItems, wantItems)
	}
	if len(cleanedItems) != 1 || cleanedItems[0].LinkPath != `C:\Common\Common App.lnk` {
		t.Fatalf("Clean received an unexpected selection: %#v", cleanedItems)
	}
}

func TestModelCleanupFailureShowsReasonOnDoneScreen(t *testing.T) {
	t.Parallel()
	failure := errors.New("injected cleanup failure")
	model := loadedModel(true)
	model.config.Clean = func(items []core.Item) (core.CleanResult, error) {
		return core.CleanResult{Requested: len(items)}, failure
	}
	model = updateKey(model, " ")
	model = updateKey(model, "enter")
	model = updateKey(model, "enter")

	model, command := updateKeyWithCommand(model, "y")
	message := executeCleanupCommand(t, command)
	updated, nextCommand := model.Update(message)
	model = updated.(Model)

	if nextCommand != nil {
		t.Fatalf("cleanup failure returned command %T", nextCommand)
	}
	if model.state != stateDone {
		t.Fatalf("state = %v, want done", model.state)
	}
	if model.cleaned == nil || len(model.cleaned.Errors) != 1 {
		t.Fatalf("cleanup errors = %#v, want one", model.cleaned)
	}
	cleanError := model.cleaned.Errors[0]
	if cleanError.ReasonCode != core.ReasonDeleteFailure || cleanError.Error != failure.Error() {
		t.Fatalf("cleanup error = %#v", cleanError)
	}
	view := model.View()
	for _, content := range []string{
		"Cleanup completed with errors",
		"Error [" + string(core.ReasonDeleteFailure) + "]",
		failure.Error(),
	} {
		if !strings.Contains(view, content) {
			t.Fatalf("done view is missing %q:\n%s", content, view)
		}
	}
}

func TestModelCannotQuitWhilePermanentDeletionIsRunning(t *testing.T) {
	t.Parallel()
	for _, key := range []tea.KeyMsg{
		{Type: tea.KeyRunes, Runes: []rune("q")},
		{Type: tea.KeyEsc},
		{Type: tea.KeyCtrlC},
	} {
		model := loadedModel(true)
		model.state = stateCleaning
		updated, command := model.Update(key)
		model = updated.(Model)
		if command != nil {
			t.Fatalf("key %q returned quit command while cleaning", key.String())
		}
		if model.state != stateCleaning || model.cancelled {
			t.Fatalf("key %q changed cleaning state: state=%v cancelled=%v", key.String(), model.state, model.cancelled)
		}
	}
}

func TestModelHelpRestoresPreviousState(t *testing.T) {
	t.Parallel()
	stateModels := []struct {
		name  string
		build func() Model
	}{
		{
			name: "browse",
			build: func() Model {
				return loadedModel(true)
			},
		},
		{
			name: "review",
			build: func() Model {
				model := loadedModel(true)
				model = updateKey(model, " ")
				return updateKey(model, "enter")
			},
		},
		{
			name: "confirm",
			build: func() Model {
				model := loadedModel(true)
				model = updateKey(model, " ")
				model = updateKey(model, "enter")
				return updateKey(model, "enter")
			},
		},
	}

	for _, stateModel := range stateModels {
		for _, closeKey := range []string{"?", "esc"} {
			t.Run(stateModel.name+"/"+closeKey, func(t *testing.T) {
				t.Parallel()
				model := stateModel.build()
				originalState := model.state

				model = updateKey(model, "?")

				if model.state != stateHelp || model.previous != originalState {
					t.Fatalf("help state = %v, previous = %v, want help/%v", model.state, model.previous, originalState)
				}
				if view := model.View(); !strings.Contains(view, "Help") {
					t.Fatalf("help view is missing its heading:\n%s", view)
				}

				model = updateKey(model, closeKey)

				if model.state != originalState {
					t.Fatalf("state after %q = %v, want %v", closeKey, model.state, originalState)
				}
				if model.cancelled {
					t.Fatalf("%q closed help by cancelling the model", closeKey)
				}
			})
		}
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

func TestModelResizeKeepsImportantBrowseContent(t *testing.T) {
	t.Parallel()
	for _, size := range []struct {
		name                  string
		width, height         int
		wantWidth, wantHeight int
	}{
		{name: "narrow", width: 1, height: 1, wantWidth: 30, wantHeight: 10},
		{name: "wide", width: 140, height: 40, wantWidth: 140, wantHeight: 40},
	} {
		t.Run(size.name, func(t *testing.T) {
			t.Parallel()
			model := loadedModel(true)
			updated, command := model.Update(tea.WindowSizeMsg{Width: size.width, Height: size.height})
			model = updated.(Model)

			if command != nil {
				t.Fatalf("resize returned command %T", command)
			}
			if model.width != size.wantWidth || model.height != size.wantHeight {
				t.Fatalf(
					"size = %dx%d, want %dx%d",
					model.width,
					model.height,
					size.wantWidth,
					size.wantHeight,
				)
			}
			view := model.View()
			for _, content := range []string{
				"startclean",
				"Scanned 2 shortcuts",
				"USER PROGRAMS",
				"User App",
				"ALL-USERS PROGRAMS",
				"Common App",
				"DETAILS",
				"Missing target",
			} {
				if !strings.Contains(view, content) {
					t.Fatalf("%s view is missing %q:\n%s", size.name, content, view)
				}
			}
		})
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

func TestTerminalWidthHelpersPreserveUnicodeCellBoundaries(t *testing.T) {
	t.Parallel()

	value := "日本語🔒e\u0301 shortcut"
	for _, width := range []int{1, 2, 5, 8, 12} {
		got := truncate(value, width)
		if !utf8.ValidString(got) {
			t.Fatalf("truncate width %d produced invalid UTF-8: %q", width, got)
		}
		if cells := runewidth.StringWidth(got); cells > width {
			t.Fatalf("truncate width %d uses %d terminal cells: %q", width, cells, got)
		}
	}

	const wrapWidth = 6
	value = `C:\ユーザー\🔒é\missing.exe`
	wrapped := wrap(value, wrapWidth)
	if strings.ReplaceAll(wrapped, "\n", "") != value {
		t.Fatalf("wrap changed Unicode content: %q", wrapped)
	}
	for line := range strings.SplitSeq(wrapped, "\n") {
		if cells := runewidth.StringWidth(line); cells > wrapWidth {
			t.Fatalf("wrapped line uses %d cells, want at most %d: %q", cells, wrapWidth, line)
		}
		if first, _ := utf8.DecodeRuneInString(line); unicode.Is(unicode.Mn, first) {
			t.Fatalf("wrap split a combining sequence before %q", line)
		}
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
	updated, _ := updateKeyWithCommand(model, key)
	return updated
}

func updateKeyWithCommand(model Model, key string) (Model, tea.Cmd) {
	var message tea.KeyMsg
	switch key {
	case " ":
		message = tea.KeyMsg{Type: tea.KeySpace}
	case "backspace":
		message = tea.KeyMsg{Type: tea.KeyBackspace}
	case "down":
		message = tea.KeyMsg{Type: tea.KeyDown}
	case "enter":
		message = tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		message = tea.KeyMsg{Type: tea.KeyEsc}
	default:
		message = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)}
	}
	updated, command := model.Update(message)
	return updated.(Model), command
}

func executeCleanupCommand(t *testing.T, command tea.Cmd) cleanMessage {
	t.Helper()
	if command == nil {
		t.Fatal("cleanup command is nil")
	}
	message := command()
	if clean, ok := message.(cleanMessage); ok {
		return clean
	}
	batch, ok := message.(tea.BatchMsg)
	if !ok || len(batch) == 0 {
		t.Fatalf("cleanup command returned %T, want cleanMessage or non-empty tea.BatchMsg", message)
	}
	message = batch[len(batch)-1]()
	clean, ok := message.(cleanMessage)
	if !ok {
		t.Fatalf("last batched command returned %T, want cleanMessage", message)
	}
	return clean
}
