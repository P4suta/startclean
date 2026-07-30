// SPDX-FileCopyrightText: 2026 startclean contributors <https://github.com/P4suta/startclean/graphs/contributors>
// SPDX-License-Identifier: MIT OR Apache-2.0

//go:build !windows

package core

import "io/fs"

func isReparsePoint(info fs.FileInfo) bool {
	return info.Mode()&fs.ModeSymlink != 0
}
