// SPDX-FileCopyrightText: 2026 startclean contributors <https://github.com/P4suta/startclean/graphs/contributors>
// SPDX-License-Identifier: MIT OR Apache-2.0

//go:build !windows

package platform

import "github.com/P4suta/startclean/internal/core"

var _ core.GuardedRemover = (*System)(nil)

func (*System) DeleteValidated(string, string, func() error) error {
	return errUnsupported
}

func (*System) RemoveEmptyDirectory(string, string) (bool, error) {
	return false, errUnsupported
}
