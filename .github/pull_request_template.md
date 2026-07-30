<!--
SPDX-FileCopyrightText: 2026 startclean contributors <https://github.com/P4suta/startclean/graphs/contributors>
SPDX-License-Identifier: MIT OR Apache-2.0
-->

## Summary

<!-- Explain what changed and why. Link related issues with "Closes #123". -->

## User-visible behavior

<!-- Describe CLI/TUI/output changes. Write "None" if there are none. -->

## Safety impact

<!--
Explain any effect on candidate classification, selection, elevation, path
containment, TOCTOU revalidation, deletion, or folder pruning. Write "None" if
the change cannot affect cleanup safety.
-->

## Validation

<!-- List the exact commands and manual checks performed. -->

- [ ] `go run ./cmd/devtool ci`
- [ ] Additional Windows or integration validation, when applicable
- [ ] Manual validation, when applicable

## Screenshots

<!-- Required for visible TUI changes. Remove this section if not applicable. -->

## Checklist

- [ ] The change is focused and the commit history is understandable.
- [ ] Tests cover new behavior and important failure paths.
- [ ] User-facing documentation and completions are updated when needed.
- [ ] New files carry SPDX metadata and pass `reuse lint`.
- [ ] Generated files are up to date.
- [ ] No secrets, personal paths, or unrelated artifacts are included.
