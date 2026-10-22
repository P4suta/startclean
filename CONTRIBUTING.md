<!--
SPDX-FileCopyrightText: 2026 startclean contributors <https://github.com/P4suta/startclean/graphs/contributors>
SPDX-License-Identifier: MIT OR Apache-2.0
-->

# Contributing

Contributions are welcome.

Keep detection conservative: uncertain shortcuts must remain unselectable and must never become deletion candidates.

Add table-driven policy tests for new classifications and temporary-root tests for cleanup changes.

## Development

Trust and install the pinned development tools with `mise trust mise.toml` and `mise install`.

Run the common local checks with `mise x -- go run ./cmd/devtool ci`.

Use `mise x -- go run ./cmd/devtool help` to see focused checks.

After a Go upgrade, rebuild previously installed analysis tools with `mise install --force go:golang.org/x/vuln/cmd/govulncheck go:github.com/google/osv-scanner/v2/cmd/osv-scanner`.

The local `ci` task covers formatting, module integrity, generated files, lint, vet, tests, Windows integration, coverage, security, license policy, build, and fast release checks.

Pull request CI runs metadata checks and selects native Windows quality, race, policy, and fast release-configuration checks according to the changed files.

Extended fuzzing, repeated stress checks, and cross-platform campaigns belong to release verification or a focused investigation.

Rust is not required for this project.

## Pull requests

Use Conventional Commit syntax in pull request titles because squash-merge titles appear in the release changelog.

Add tests for new behavior and important failure paths, and update user-facing documentation or generated completions when needed.

By contributing, you agree that your contribution is licensed under the `MIT OR Apache-2.0` expression.
