// SPDX-FileCopyrightText: 2026 startclean contributors <https://github.com/P4suta/startclean/graphs/contributors>
// SPDX-License-Identifier: MIT OR Apache-2.0

package tui

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"

	"github.com/P4suta/startclean/internal/core"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type Config struct {
	Scan         func(context.Context) core.ScanResult
	Clean        func([]core.Item) (core.CleanResult, error)
	Elevated     bool
	ElevateHint  string
	ColorEnabled bool
}

type RunResult struct {
	Cancelled bool
	Cleaned   *core.CleanResult
}

type state uint8

const (
	stateScanning state = iota
	stateBrowse
	stateReview
	stateConfirm
	stateCleaning
	stateDone
	stateHelp
)

type scanMessage struct{ result core.ScanResult }
type cleanMessage struct {
	result core.CleanResult
	err    error
}

type Model struct {
	ctx          context.Context
	config       Config
	spinner      spinner.Model
	state        state
	previous     state
	width        int
	height       int
	result       core.ScanResult
	items        []core.Item
	cursor       int
	selected     map[string]bool
	cancelled    bool
	cleaned      *core.CleanResult
	colorEnabled bool
}

func NewModel(ctx context.Context, config Config) Model {
	progress := spinner.New()
	progress.Spinner = spinner.Dot
	return Model{
		ctx: ctx, config: config, spinner: progress, state: stateScanning,
		width: 80, height: 24, selected: make(map[string]bool),
		colorEnabled: config.ColorEnabled,
	}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, m.scanCommand())
}

func (m Model) scanCommand() tea.Cmd {
	return func() tea.Msg {
		return scanMessage{result: m.config.Scan(m.ctx)}
	}
}

func (m Model) cleanCommand(items []core.Item) tea.Cmd {
	return func() tea.Msg {
		result, err := m.config.Clean(items)
		return cleanMessage{result: result, err: err}
	}
}

func (m Model) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch message := message.(type) {
	case tea.WindowSizeMsg:
		m.width = max(message.Width, 30)
		m.height = max(message.Height, 10)
		return m, nil
	case scanMessage:
		m.result = message.result
		for _, item := range message.result.Items {
			if item.Eligible() {
				m.items = append(m.items, item)
			}
		}
		sort.SliceStable(m.items, func(i, j int) bool {
			if m.items[i].Scope != m.items[j].Scope {
				return m.items[i].Scope == core.ScopeUser
			}
			return strings.ToLower(m.items[i].LinkPath) < strings.ToLower(m.items[j].LinkPath)
		})
		m.state = stateBrowse
		return m, nil
	case cleanMessage:
		m.cleaned = &message.result
		if message.err != nil && len(m.cleaned.Errors) == 0 {
			m.cleaned.Errors = append(m.cleaned.Errors, core.CleanError{
				ReasonCode: core.ReasonDeleteFailure, Error: message.err.Error(),
			})
		}
		m.state = stateDone
		return m, nil
	case spinner.TickMsg:
		if m.state == stateScanning || m.state == stateCleaning {
			var command tea.Cmd
			m.spinner, command = m.spinner.Update(message)
			return m, command
		}
	}

	key, ok := message.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	if m.state == stateDone {
		switch key.String() {
		case "enter", "q", "esc", "ctrl+c":
			return m, tea.Quit
		}
		return m, nil
	}
	if key.String() == "ctrl+c" || key.String() == "q" || key.String() == "esc" {
		if m.state == stateHelp && key.String() == "esc" {
			m.state = m.previous
			return m, nil
		}
		m.cancelled = true
		return m, tea.Quit
	}
	if key.String() == "?" && m.state != stateScanning && m.state != stateCleaning {
		if m.state == stateHelp {
			m.state = m.previous
		} else {
			m.previous = m.state
			m.state = stateHelp
		}
		return m, nil
	}

	switch m.state {
	case stateBrowse:
		switch key.String() {
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor+1 < len(m.items) {
				m.cursor++
			}
		case " ":
			m.toggleCurrent()
		case "a":
			m.toggleAll()
		case "enter":
			if len(m.selectedItems()) > 0 {
				m.state = stateReview
			}
		}
	case stateReview:
		switch key.String() {
		case "enter":
			m.state = stateConfirm
		case "backspace":
			m.state = stateBrowse
		}
	case stateConfirm:
		switch strings.ToLower(key.String()) {
		case "y":
			selected := m.selectedItems()
			m.state = stateCleaning
			return m, tea.Batch(m.spinner.Tick, m.cleanCommand(selected))
		case "n", "enter":
			m.state = stateBrowse
		}
	case stateScanning, stateCleaning, stateDone, stateHelp:
	}
	return m, nil
}

func (m *Model) toggleCurrent() {
	if m.cursor < 0 || m.cursor >= len(m.items) {
		return
	}
	item := m.items[m.cursor]
	if item.Scope == core.ScopeCommon && !m.config.Elevated {
		return
	}
	m.selected[item.LinkPath] = !m.selected[item.LinkPath]
	if !m.selected[item.LinkPath] {
		delete(m.selected, item.LinkPath)
	}
}

func (m *Model) toggleAll() {
	eligible := make([]core.Item, 0, len(m.items))
	allSelected := true
	for _, item := range m.items {
		if item.Scope == core.ScopeCommon && !m.config.Elevated {
			continue
		}
		eligible = append(eligible, item)
		if !m.selected[item.LinkPath] {
			allSelected = false
		}
	}
	for _, item := range eligible {
		if allSelected {
			delete(m.selected, item.LinkPath)
		} else {
			m.selected[item.LinkPath] = true
		}
	}
}

func (m Model) selectedItems() []core.Item {
	items := make([]core.Item, 0, len(m.selected))
	for _, item := range m.items {
		if m.selected[item.LinkPath] {
			items = append(items, item)
		}
	}
	return items
}

func (m Model) View() string {
	switch m.state {
	case stateScanning:
		return m.frame(fmt.Sprintf("%s Scanning the user and all-users Start Menus…", m.spinner.View()))
	case stateCleaning:
		return m.frame(fmt.Sprintf("%s Permanently deleting %d selected shortcut(s)…", m.spinner.View(), len(m.selected)))
	case stateDone:
		return m.doneView()
	case stateReview:
		return m.reviewView()
	case stateConfirm:
		return m.confirmView()
	case stateHelp:
		return m.helpView()
	case stateBrowse:
		return m.browseView()
	}
	return ""
}

func (m Model) browseView() string {
	var body strings.Builder
	body.WriteString(m.heading("startclean"))
	body.WriteString("\n")
	_, _ = fmt.Fprintf(&body,
		"Scanned %d shortcuts · %d stale · %d selected",
		m.result.Summary.Scanned, m.result.Summary.Stale, len(m.selected),
	)
	if m.result.Summary.Errors > 0 {
		_, _ = fmt.Fprintf(&body, " · %d unreadable", m.result.Summary.Errors)
	}
	body.WriteString("\n\n")

	if len(m.items) == 0 {
		body.WriteString(m.good("No definitively stale Start Menu shortcuts were found."))
		body.WriteString("\n\n")
		body.WriteString(m.muted("q/Esc cancel  ? help"))
		return m.frame(body.String())
	}

	listWidth := m.width - 4
	if m.width >= 96 {
		listWidth = (m.width - 7) / 2
	}
	list := m.listView(listWidth)
	detail := m.detailView(m.width - listWidth - 7)
	if m.width >= 96 {
		body.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, list, "   ", detail))
	} else {
		body.WriteString(list)
		body.WriteString("\n\n")
		body.WriteString(m.detailView(m.width - 4))
	}
	body.WriteString("\n\n")
	body.WriteString(m.muted("↑/↓ move  Space toggle  a toggle all  Enter review  ? help  q/Esc cancel"))
	return m.frame(body.String())
}

func (m Model) listView(width int) string {
	available := max(m.height-12, 4)
	start := 0
	if m.cursor >= available {
		start = m.cursor - available + 1
	}
	end := min(len(m.items), start+available)
	var builder strings.Builder
	var lastScope core.Scope
	for index := start; index < end; index++ {
		item := m.items[index]
		if item.Scope != lastScope {
			if builder.Len() > 0 {
				builder.WriteString("\n")
			}
			label := "USER PROGRAMS"
			if item.Scope == core.ScopeCommon {
				label = "ALL-USERS PROGRAMS"
				if !m.config.Elevated {
					label += " · LOCKED"
				}
			}
			builder.WriteString(m.section(label))
			builder.WriteString("\n")
			lastScope = item.Scope
		}
		cursor := "  "
		if index == m.cursor {
			cursor = "› "
		}
		marker := "○"
		if m.selected[item.LinkPath] {
			marker = "●"
		}
		if item.Scope == core.ScopeCommon && !m.config.Elevated {
			marker = "🔒"
		}
		name := strings.TrimSuffix(filepath.Base(item.LinkPath), filepath.Ext(item.LinkPath))
		builder.WriteString(truncate(cursor+marker+" "+name, width))
		builder.WriteString("\n")
	}
	return builder.String()
}

func (m Model) detailView(width int) string {
	if len(m.items) == 0 {
		return ""
	}
	width = max(width, 20)
	item := m.items[m.cursor]
	var builder strings.Builder
	builder.WriteString(m.section("DETAILS"))
	builder.WriteString("\n")
	builder.WriteString(m.label("Shortcut"))
	builder.WriteString("\n")
	builder.WriteString(wrap(item.LinkPath, width))
	builder.WriteString("\n\n")
	builder.WriteString(m.label("Missing target"))
	builder.WriteString("\n")
	builder.WriteString(wrap(item.TargetPath, width))
	builder.WriteString("\n\n")
	builder.WriteString(m.label("Safety"))
	builder.WriteString("\n")
	if item.Scope == core.ScopeCommon && !m.config.Elevated {
		builder.WriteString("Locked until startclean is elevated.\n")
		builder.WriteString(wrap(m.config.ElevateHint, width))
	} else {
		builder.WriteString("High confidence: local fixed-drive target is definitively absent.")
	}
	return builder.String()
}

func (m Model) reviewView() string {
	selected := m.selectedItems()
	var builder strings.Builder
	builder.WriteString(m.heading("Review permanent deletion"))
	builder.WriteString("\n\n")
	_, _ = fmt.Fprintf(&builder, "%d shortcut(s) selected. No application files will be removed.\n\n", len(selected))
	for _, item := range selected {
		builder.WriteString("  • ")
		builder.WriteString(strings.TrimSuffix(filepath.Base(item.LinkPath), filepath.Ext(item.LinkPath)))
		builder.WriteString("  ")
		builder.WriteString(m.muted("[" + string(item.Scope) + "]"))
		builder.WriteString("\n")
	}
	builder.WriteString("\n")
	builder.WriteString(m.muted("Enter continue  Backspace edit  q/Esc cancel"))
	return m.frame(builder.String())
}

func (m Model) confirmView() string {
	count := len(m.selected)
	var builder strings.Builder
	builder.WriteString(m.warning("Permanent deletion"))
	builder.WriteString("\n\n")
	_, _ = fmt.Fprintf(&builder, "Permanently delete exactly %d selected shortcut(s)? [y/N]", count)
	builder.WriteString("\n\nOnly the .lnk files listed on the review screen will be removed.")
	return m.frame(builder.String())
}

func (m Model) doneView() string {
	if m.cleaned == nil {
		return m.frame(m.warning("Cleanup finished without a result."))
	}
	var builder strings.Builder
	if len(m.cleaned.Errors) == 0 {
		builder.WriteString(m.good("Cleanup complete"))
	} else {
		builder.WriteString(m.warning("Cleanup completed with errors"))
	}
	builder.WriteString("\n\n")
	_, _ = fmt.Fprintf(&builder, "Deleted %d of %d selected shortcut(s).", m.cleaned.Deleted, m.cleaned.Requested)
	if m.cleaned.Pruned > 0 {
		_, _ = fmt.Fprintf(&builder, " Removed %d empty folder(s).", m.cleaned.Pruned)
	}
	for _, cleanError := range m.cleaned.Errors {
		builder.WriteString("\n\n")
		_, _ = fmt.Fprintf(&builder, "Error [%s]\n%s", cleanError.ReasonCode, cleanError.Error)
	}
	builder.WriteString("\n\n")
	builder.WriteString(m.muted("Enter/q/Esc close"))
	return m.frame(builder.String())
}

func (m Model) helpView() string {
	help := m.heading("Help") + `

Space       Toggle the highlighted eligible shortcut
a           Toggle all eligible shortcuts
↑/↓, j/k    Move through results
Enter       Review the selection / continue
Backspace   Return from review to the results
?           Open or close this help
q, Esc      Cancel without deleting

Only missing absolute targets on local fixed drives are eligible. Network,
removable, relative, environment-dependent, shell namespace, and unreadable
targets are never selectable. Nothing is selected initially.`
	return m.frame(help)
}

func (m Model) frame(content string) string {
	return lipgloss.NewStyle().Padding(1, 2).MaxWidth(max(m.width, 30)).Render(content)
}

func (m Model) heading(value string) string {
	if !m.colorEnabled {
		return value
	}
	return lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("39")).Render(value)
}

func (m Model) section(value string) string {
	if !m.colorEnabled {
		return value
	}
	return lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("75")).Render(value)
}

func (m Model) label(value string) string {
	if !m.colorEnabled {
		return value + ":"
	}
	return lipgloss.NewStyle().Bold(true).Render(value + ":")
}

func (m Model) muted(value string) string {
	if !m.colorEnabled {
		return value
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Render(value)
}

func (m Model) good(value string) string {
	if !m.colorEnabled {
		return value
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Render(value)
}

func (m Model) warning(value string) string {
	if !m.colorEnabled {
		return value
	}
	return lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("203")).Render(value)
}

func truncate(value string, width int) string {
	runes := []rune(value)
	if len(runes) <= width {
		return value
	}
	if width <= 1 {
		return "…"
	}
	return string(runes[:width-1]) + "…"
}

func wrap(value string, width int) string {
	if value == "" {
		return "-"
	}
	width = max(width, 1)
	runes := []rune(value)
	var lines []string
	for len(runes) > width {
		lines = append(lines, string(runes[:width]))
		runes = runes[width:]
	}
	lines = append(lines, string(runes))
	return strings.Join(lines, "\n")
}

func Run(ctx context.Context, input io.Reader, output io.Writer, config Config) (RunResult, error) {
	model := NewModel(ctx, config)
	program := tea.NewProgram(model, tea.WithInput(input), tea.WithOutput(output), tea.WithAltScreen())
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			program.Quit()
		case <-done:
		}
	}()
	finalModel, err := program.Run()
	close(done)
	if err != nil {
		return RunResult{}, err
	}
	final, ok := finalModel.(Model)
	if !ok {
		return RunResult{}, fmt.Errorf("unexpected final TUI model %T", finalModel)
	}
	return RunResult{Cancelled: final.cancelled, Cleaned: final.cleaned}, nil
}
