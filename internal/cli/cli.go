// SPDX-FileCopyrightText: 2026 startclean contributors <https://github.com/P4suta/startclean/graphs/contributors>
// SPDX-License-Identifier: MIT OR Apache-2.0

package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/P4suta/startclean/internal/buildinfo"
	"github.com/P4suta/startclean/internal/core"
	"github.com/P4suta/startclean/internal/platform"
	"github.com/P4suta/startclean/internal/tui"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

const (
	ExitSuccess           = 0
	ExitOperational       = 1
	ExitUsage             = 2
	ExitCandidatesFound   = 3
	ExitElevationRequired = 4
)

func init() {
	// The root command intentionally opens an interactive TUI with no
	// arguments, including when Explorer starts the executable by
	// double-click. Cobra's Windows mousetrap would otherwise replace the TUI
	// with a cmd.exe-only message and terminate before RunE is reached.
	cobra.MousetrapHelpText = ""
}

type codedError struct {
	code int
	err  error
}

func (e codedError) Error() string { return e.err.Error() }
func (e codedError) Unwrap() error { return e.err }

func withCode(code int, err error) error {
	if err == nil {
		return nil
	}
	return codedError{code: code, err: err}
}

type app struct {
	in        io.Reader
	out       io.Writer
	errOut    io.Writer
	colorMode string
	system    systemAdapter
}

// systemAdapter keeps command policy independent from the Windows API layer.
// Tests use the same command tree with temporary roots and an in-memory link
// reader, so every documented exit code can be exercised without touching the
// real Start Menu.
type systemAdapter interface {
	core.LinkReader
	core.ShellNotifier
	core.GuardedRemover
	Supported() bool
	Roots() (core.Roots, error)
	Elevated() bool
	ExpandEnvironment(string) (string, error)
	DriveKind(string) (core.DriveKind, error)
}

type services struct {
	roots    core.Roots
	elevated bool
	scanner  core.Scanner
	cleaner  core.Cleaner
}

func Execute(args []string, in io.Reader, out, errOut io.Writer) (int, error) {
	return executeWithSystem(args, in, out, errOut, platform.New())
}

func executeWithSystem(args []string, in io.Reader, out, errOut io.Writer, system systemAdapter) (int, error) {
	application := &app{in: in, out: out, errOut: errOut, colorMode: "auto", system: system}
	command := application.rootCommand()
	command.SetArgs(args)
	command.SetIn(in)
	command.SetOut(out)
	command.SetErr(errOut)
	err := command.Execute()
	if err == nil {
		return ExitSuccess, nil
	}
	var coded codedError
	if errors.As(err, &coded) {
		return coded.code, coded.err
	}
	if isUsageError(err) {
		return ExitUsage, err
	}
	return ExitOperational, err
}

func (a *app) rootCommand() *cobra.Command {
	root := &cobra.Command{
		Use:           "startclean",
		Short:         "Safely remove orphaned Windows Start Menu shortcuts",
		Long:          "startclean finds definitively broken .lnk files in the Windows Start Menu and permanently removes only the shortcuts you explicitly select.",
		SilenceErrors: true,
		SilenceUsage:  true,
		Version:       buildinfo.Version,
		Args:          cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.runInteractive(cmd.Context())
		},
	}
	root.SetVersionTemplate(buildinfo.String() + "\n")
	root.PersistentFlags().StringVar(&a.colorMode, "color", "auto", "color output: auto, always, or never")
	root.PersistentPreRunE = func(_ *cobra.Command, _ []string) error {
		switch a.colorMode {
		case "auto", "always", "never":
			return nil
		default:
			return withCode(ExitUsage, fmt.Errorf("invalid --color value %q (want auto, always, or never)", a.colorMode))
		}
	}
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return withCode(ExitUsage, err)
	})

	root.AddCommand(a.scanCommand())
	root.AddCommand(a.cleanCommand())
	root.AddCommand(a.doctorCommand())
	root.AddCommand(a.completionCommand(root))
	root.AddCommand(&cobra.Command{
		Use:   "version",
		Short: "Print version information",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			_, err := fmt.Fprintln(a.out, buildinfo.String())
			return err
		},
	})
	return root
}

func (a *app) loadServices() (services, error) {
	if !a.system.Supported() {
		return services{}, errors.New("startclean supports Windows 10 and Windows 11 only")
	}
	roots, err := a.system.Roots()
	if err != nil {
		return services{}, err
	}
	classifier := core.Classifier{
		Stat: os.Stat, ExpandEnv: a.system.ExpandEnvironment, DriveKind: a.system.DriveKind,
	}
	elevated := a.system.Elevated()
	return services{
		roots:    roots,
		elevated: elevated,
		scanner: core.Scanner{
			Roots: roots, Reader: a.system, Classifier: classifier,
		},
		cleaner: core.Cleaner{
			Roots: roots, Reader: a.system, Classifier: classifier, FS: core.OSFS{},
			Remover: a.system, Elevated: elevated, Notifier: a.system,
		},
	}, nil
}

func (a *app) scanCommand() *cobra.Command {
	var scopeValue string
	var format string
	var showSkipped bool
	var check bool
	command := &cobra.Command{
		Use:   "scan",
		Short: "Scan Start Menu shortcuts without changing anything",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			scope, err := core.ParseScope(scopeValue)
			if err != nil {
				return withCode(ExitUsage, err)
			}
			if format != "table" && format != "json" {
				return withCode(ExitUsage, fmt.Errorf("invalid --format value %q (want table or json)", format))
			}
			svc, err := a.loadServices()
			if err != nil {
				return err
			}
			result := svc.scanner.Scan(cmd.Context(), scope)
			visible := result
			if !showSkipped {
				visible.Items = staleItems(result.Items)
			}
			if format == "json" {
				err = writeJSON(a.out, core.Envelope(visible))
			} else {
				err = writeScanTable(a.out, visible, showSkipped)
			}
			if err != nil {
				return err
			}
			if result.Summary.Errors > 0 {
				return withCode(ExitOperational, fmt.Errorf("scan completed with %d error(s)", result.Summary.Errors))
			}
			if check && result.Summary.Stale > 0 {
				return withCode(ExitCandidatesFound, fmt.Errorf("%d stale shortcut(s) found", result.Summary.Stale))
			}
			return nil
		},
	}
	command.Flags().StringVar(&scopeValue, "scope", "all", "scope to scan: user, common, or all")
	command.Flags().StringVar(&format, "format", "table", "output format: table or json")
	command.Flags().BoolVar(&showSkipped, "show-skipped", false, "include healthy, unverifiable, and unreadable shortcuts")
	command.Flags().BoolVar(&check, "check", false, "exit with code 3 when stale shortcuts are found")
	return command
}

func (a *app) cleanCommand() *cobra.Command {
	var all bool
	var yes bool
	command := &cobra.Command{
		Use:   "clean",
		Short: "Interactively review and remove stale shortcuts",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !all && !yes {
				return a.runInteractive(cmd.Context())
			}
			if !all || !yes {
				return withCode(ExitUsage, errors.New("non-interactive cleanup requires both --all and --yes"))
			}
			svc, err := a.loadServices()
			if err != nil {
				return err
			}
			result := svc.scanner.Scan(cmd.Context(), core.ScopeAll)
			if result.Summary.Errors > 0 {
				return fmt.Errorf("refusing cleanup because the scan had %d error(s)", result.Summary.Errors)
			}
			selected := staleItems(result.Items)
			if len(selected) == 0 {
				_, err = fmt.Fprintln(a.out, "No stale Start Menu shortcuts found.")
				return err
			}
			if !svc.elevated && containsCommon(selected) {
				_, _ = fmt.Fprintln(a.errOut, "All-users shortcuts require elevation. Run:")
				_, _ = fmt.Fprintln(a.errOut, automationElevationCommand())
				return withCode(ExitElevationRequired, core.ErrElevationRequired)
			}
			cleaned, cleanErr := svc.cleaner.Clean(selected)
			summaryErr := writeCleanSummary(a.out, cleaned)
			if cleanErr != nil {
				if errors.Is(cleanErr, core.ErrElevationRequired) {
					return withCode(ExitElevationRequired, cleanErr)
				}
				return cleanErr
			}
			return summaryErr
		},
	}
	command.Flags().BoolVar(&all, "all", false, "select every eligible stale shortcut")
	command.Flags().BoolVar(&yes, "yes", false, "confirm permanent deletion")
	return command
}

func (a *app) runInteractive(ctx context.Context) error {
	if !isTerminalReader(a.in) || !isTerminalWriter(a.out) {
		return withCode(ExitUsage, errors.New("interactive cleanup requires a TTY; use 'startclean clean --all --yes' for explicit automation"))
	}
	svc, err := a.loadServices()
	if err != nil {
		return err
	}
	result, err := tui.Run(ctx, a.in, a.out, tui.Config{
		Scan: func(ctx context.Context) core.ScanResult {
			return svc.scanner.Scan(ctx, core.ScopeAll)
		},
		Clean:        svc.cleaner.Clean,
		Elevated:     svc.elevated,
		ElevateHint:  interactiveElevationCommand(),
		ColorEnabled: a.colorEnabled(),
	})
	if err != nil {
		return err
	}
	if result.Cancelled {
		_, err = fmt.Fprintln(a.out, "Cancelled. Nothing was deleted.")
		return err
	}
	if result.Cleaned != nil {
		if err := writeCleanSummary(a.out, *result.Cleaned); err != nil {
			return err
		}
		if len(result.Cleaned.Errors) > 0 {
			return errors.New("cleanup completed with operational errors")
		}
	}
	return nil
}

func (a *app) completionCommand(root *cobra.Command) *cobra.Command {
	return &cobra.Command{
		Use:       "completion powershell|bash|zsh|fish",
		Short:     "Generate shell completion",
		Args:      cobra.ExactArgs(1),
		ValidArgs: []string{"powershell", "bash", "zsh", "fish"},
		RunE: func(_ *cobra.Command, args []string) error {
			switch args[0] {
			case "powershell":
				return root.GenPowerShellCompletion(a.out)
			case "bash":
				return root.GenBashCompletion(a.out)
			case "zsh":
				return root.GenZshCompletion(a.out)
			case "fish":
				return root.GenFishCompletion(a.out, true)
			default:
				return withCode(ExitUsage, fmt.Errorf("unsupported shell %q", args[0]))
			}
		},
	}
}

func staleItems(items []core.Item) []core.Item {
	selected := make([]core.Item, 0, len(items))
	for _, item := range items {
		if item.Eligible() {
			selected = append(selected, item)
		}
	}
	return selected
}

func containsCommon(items []core.Item) bool {
	for _, item := range items {
		if item.Scope == core.ScopeCommon {
			return true
		}
	}
	return false
}

func interactiveElevationCommand() string {
	return elevationCommand("clean")
}

func automationElevationCommand() string {
	return elevationCommand("clean", "--all", "--yes")
}

func elevationCommand(arguments ...string) string {
	executable, err := os.Executable()
	if err != nil {
		executable = "startclean.exe"
	}
	executable, _ = filepath.Abs(executable)
	executable = platform.EscapePowerShellSingleQuoted(executable)
	quotedArguments := make([]string, len(arguments))
	for index, argument := range arguments {
		quotedArguments[index] = "'" + platform.EscapePowerShellSingleQuoted(argument) + "'"
	}
	return "Start-Process -FilePath '" + executable + "' -ArgumentList " +
		strings.Join(quotedArguments, ",") + " -Verb RunAs"
}

func (a *app) colorEnabled() bool {
	if a.colorMode == "never" || os.Getenv("NO_COLOR") != "" {
		return false
	}
	return a.colorMode == "always" || isTerminalWriter(a.out)
}

func isTerminalReader(reader io.Reader) bool {
	file, ok := reader.(interface{ Fd() uintptr })
	return ok && term.IsTerminal(int(file.Fd()))
}

func isTerminalWriter(writer io.Writer) bool {
	file, ok := writer.(interface{ Fd() uintptr })
	return ok && term.IsTerminal(int(file.Fd()))
}

func isUsageError(err error) bool {
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "unknown command") ||
		strings.Contains(message, "unknown flag") ||
		strings.Contains(message, "requires") ||
		strings.Contains(message, "accepts")
}

func writeJSON(writer io.Writer, value any) error {
	encoder := json.NewEncoder(writer)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

type doctorReport struct {
	OS           string       `json:"os"`
	Architecture string       `json:"architecture"`
	Supported    bool         `json:"supported"`
	Elevated     bool         `json:"elevated"`
	Folders      []folderInfo `json:"folders"`
	Candidates   core.Summary `json:"candidates"`
}

type folderInfo struct {
	Scope      core.Scope `json:"scope"`
	Path       string     `json:"path"`
	Accessible bool       `json:"accessible"`
	Error      string     `json:"error,omitempty"`
}

func (a *app) doctorCommand() *cobra.Command {
	var format string
	command := &cobra.Command{
		Use:   "doctor",
		Short: "Report Windows support, Start Menu access, and candidate counts",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if format != "table" && format != "json" {
				return withCode(ExitUsage, fmt.Errorf("invalid --format value %q (want table or json)", format))
			}
			report := doctorReport{
				OS: runtime.GOOS, Architecture: runtime.GOARCH,
				Supported: a.system.Supported(), Elevated: a.system.Elevated(),
			}
			var operationalErrors int
			roots, rootsErr := a.system.Roots()
			if rootsErr != nil {
				operationalErrors++
			}
			for _, pair := range []struct {
				scope core.Scope
				path  string
			}{{core.ScopeUser, roots.User}, {core.ScopeCommon, roots.Common}} {
				info := folderInfo{Scope: pair.scope, Path: pair.path}
				if pair.path == "" {
					info.Error = "known folder unavailable"
					operationalErrors++
				} else if _, err := os.ReadDir(pair.path); err != nil {
					info.Error = err.Error()
					operationalErrors++
				} else {
					info.Accessible = true
				}
				report.Folders = append(report.Folders, info)
			}
			if roots.User != "" || roots.Common != "" {
				classifier := core.Classifier{
					Stat: os.Stat, ExpandEnv: a.system.ExpandEnvironment, DriveKind: a.system.DriveKind,
				}
				scan := core.Scanner{Roots: roots, Reader: a.system, Classifier: classifier}.Scan(cmd.Context(), core.ScopeAll)
				report.Candidates = scan.Summary
				operationalErrors += scan.Summary.Errors
			}
			var err error
			if format == "json" {
				err = writeJSON(a.out, core.Envelope(report))
			} else {
				err = writeDoctorTable(a.out, report)
			}
			if err != nil {
				return err
			}
			if operationalErrors > 0 {
				return fmt.Errorf("doctor found %d operational issue(s)", operationalErrors)
			}
			return nil
		},
	}
	command.Flags().StringVar(&format, "format", "table", "output format: table or json")
	return command
}
