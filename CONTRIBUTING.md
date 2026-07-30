<!--
SPDX-FileCopyrightText: 2026 startclean contributors <https://github.com/P4suta/startclean/graphs/contributors>
SPDX-License-Identifier: MIT OR Apache-2.0
-->

# Contributing

Contributions are welcome. Keep detection conservative: uncertainty must produce
an unselectable result, never a deletion candidate. Add table-driven policy
tests for new classifications and temporary-root tests for cleanup changes.

## Development workflow

`mise.toml` defines the fully version-pinned development environment. Install
it with `mise install`, then use the Go-based devtool as the canonical task
entry point:

```powershell
go run ./cmd/devtool ci
```

Run `go run ./cmd/devtool help` for focused commands, including `format`,
`generate`, `lint`, `test`, `integration`, `coverage`, `module-verify`, `race`,
`stress`, `fuzz`, `security`, `reuse`, `build`, `release-source`, `release-check`, and
`release-smoke`. `just` and Lefthook are optional thin wrappers that delegate to
these commands; no development workflow is implemented in shell or PowerShell
scripts.

Pull request titles use Conventional Commit subjects because squash merges feed
those titles directly into the release changelog; CI validates the title on the
server in addition to the local commit-message hook.

The Windows product build is CGO-free. Running `devtool race` locally requires a
race-supported C compiler because that is a Go race-detector requirement; the
required GitHub check runs it on Ubuntu. Containment fuzzing runs on Windows so
that `filepath` uses the same semantics as the product. Before changing release
packaging, run `devtool release-smoke` to build and validate the x64/arm64 ZIPs,
checksums, SPDX SBOMs, and CycloneDX SBOMs.

Generated Bash, Zsh, Fish, and PowerShell completions are release assets, not
development automation. Keep them synchronized with the devtool's `generate`
command, but do not place development logic in them.

## Pull request checklist

- [ ] `go run ./cmd/devtool ci` passes in the mise-managed environment.
- [ ] Relevant race, fuzz, or release-smoke checks pass for affected code.
- [ ] New behavior and important failure paths have tests.
- [ ] User-facing documentation and generated completions are current.
- [ ] New files have SPDX metadata and pass REUSE validation.
- [ ] Commits use Conventional Commits.

By contributing, you agree that your contribution is licensed under the
`MIT OR Apache-2.0` expression.
