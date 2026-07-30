// SPDX-FileCopyrightText: 2026 startclean contributors <https://github.com/P4suta/startclean/graphs/contributors>
// SPDX-License-Identifier: MIT OR Apache-2.0

package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/P4suta/startclean/internal/cli"
)

func main() {
	outputDirectory := filepath.Join("..", "..", "completions")
	if err := os.MkdirAll(outputDirectory, 0o750); err != nil {
		panic(err)
	}
	for shell, filename := range map[string]string{
		"powershell": "startclean.ps1",
		"bash":       "startclean.bash",
		"zsh":        "_startclean",
		"fish":       "startclean.fish",
	} {
		file, err := os.Create(filepath.Join(outputDirectory, filename))
		if err != nil {
			panic(err)
		}
		code, executeErr := cli.Execute([]string{"completion", shell}, os.Stdin, file, os.Stderr)
		closeErr := file.Close()
		if executeErr != nil || code != 0 {
			panic(fmt.Sprintf("generate %s completion: code %d: %v", shell, code, executeErr))
		}
		if closeErr != nil {
			panic(closeErr)
		}
	}
}
