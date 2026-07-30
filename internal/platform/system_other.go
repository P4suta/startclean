// SPDX-FileCopyrightText: 2026 startclean contributors <https://github.com/P4suta/startclean/graphs/contributors>
// SPDX-License-Identifier: MIT OR Apache-2.0

//go:build !windows

package platform

import (
	"errors"

	"github.com/P4suta/startclean/internal/core"
)

var errUnsupported = errors.New("startclean supports Windows 10 and Windows 11 only")

type System struct{}

func New() *System                                         { return &System{} }
func (s *System) Supported() bool                          { return false }
func (s *System) Roots() (core.Roots, error)               { return core.Roots{}, errUnsupported }
func (s *System) Elevated() bool                           { return false }
func (s *System) Target(string) (string, error)            { return "", errUnsupported }
func (s *System) ExpandEnvironment(string) (string, error) { return "", errUnsupported }
func (s *System) DriveKind(string) (core.DriveKind, error) { return core.DriveOther, errUnsupported }
func (s *System) Deleted(string)                           {}
func (s *System) DirectoryRemoved(string)                  {}
func EscapePowerShellSingleQuoted(value string) string     { return value }
