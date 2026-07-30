// SPDX-FileCopyrightText: 2026 startclean contributors <https://github.com/P4suta/startclean/graphs/contributors>
// SPDX-License-Identifier: MIT OR Apache-2.0

package cli

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/P4suta/startclean/internal/core"
)

func writeScanTable(writer io.Writer, result core.ScanResult, showSkipped bool) error {
	if len(result.Items) == 0 {
		if showSkipped {
			_, err := fmt.Fprintf(writer, "No Start Menu shortcuts found.\n\nScanned: %d  Stale: %d  Healthy: %d  Unverifiable: %d  Errors: %d\n",
				result.Summary.Scanned, result.Summary.Stale, result.Summary.Healthy,
				result.Summary.Unverifiable, result.Summary.Errors)
			return err
		}
		_, err := fmt.Fprintf(writer, "No stale Start Menu shortcuts found.\n\nScanned: %d  Stale: 0\n", result.Summary.Scanned)
		return err
	}
	table := tabwriter.NewWriter(writer, 0, 4, 2, ' ', 0)
	if _, err := fmt.Fprintln(table, "SCOPE\tCLASSIFICATION\tREASON\tSHORTCUT\tTARGET"); err != nil {
		return err
	}
	for _, item := range result.Items {
		target := item.TargetPath
		if target == "" {
			target = "-"
		}
		if _, err := fmt.Fprintf(table, "%s\t%s\t%s\t%s\t%s\n",
			item.Scope, item.Classification, item.ReasonCode, item.LinkPath, target); err != nil {
			return err
		}
	}
	if err := table.Flush(); err != nil {
		return err
	}
	_, err := fmt.Fprintf(writer, "\nScanned: %d  Stale: %d  Healthy: %d  Unverifiable: %d  Errors: %d\n",
		result.Summary.Scanned, result.Summary.Stale, result.Summary.Healthy,
		result.Summary.Unverifiable, result.Summary.Errors)
	return err
}

func writeCleanSummary(writer io.Writer, result core.CleanResult) error {
	if _, err := fmt.Fprintf(writer, "Deleted %d of %d selected shortcut(s).", result.Deleted, result.Requested); err != nil {
		return err
	}
	if result.Pruned > 0 {
		if _, err := fmt.Fprintf(writer, " Removed %d empty folder(s).", result.Pruned); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintln(writer); err != nil {
		return err
	}
	for _, cleanError := range result.Errors {
		if _, err := fmt.Fprintf(writer, "error [%s] %s: %s\n", cleanError.ReasonCode, cleanError.LinkPath, cleanError.Error); err != nil {
			return err
		}
	}
	return nil
}

func writeDoctorTable(writer io.Writer, report doctorReport) error {
	table := tabwriter.NewWriter(writer, 0, 4, 2, ' ', 0)
	rows := [][2]string{
		{"OS", report.OS + "/" + report.Architecture},
		{"Supported", yesNo(report.Supported)},
		{"Elevated", yesNo(report.Elevated)},
	}
	for _, row := range rows {
		if _, err := fmt.Fprintf(table, "%s\t%s\n", row[0], row[1]); err != nil {
			return err
		}
	}
	for _, folder := range report.Folders {
		value := folder.Path
		if value == "" {
			value = "-"
		}
		status := "accessible"
		if !folder.Accessible {
			status = "unavailable"
			if folder.Error != "" {
				status += ": " + folder.Error
			}
		}
		label := titleCaseASCII(string(folder.Scope)) + " Programs"
		if _, err := fmt.Fprintf(table, "%s\t%s (%s)\n", label, value, status); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(table, "Candidates\t%d total (%d user, %d common)\n",
		report.Candidates.Stale, report.Candidates.UserStale, report.Candidates.CommonStale); err != nil {
		return err
	}
	return table.Flush()
}

func yesNo(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}

func titleCaseASCII(value string) string {
	if value == "" {
		return value
	}
	return strings.ToUpper(value[:1]) + value[1:]
}
