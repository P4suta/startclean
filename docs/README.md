<!--
SPDX-FileCopyrightText: 2026 startclean contributors <https://github.com/P4suta/startclean/graphs/contributors>
SPDX-License-Identifier: MIT OR Apache-2.0
-->

# Guide

`startclean` finds orphaned Windows 10/11 Start Menu shortcuts.
It examines `.lnk` files in the per-user and all-users Programs known folders and permanently removes only candidates that you select and confirm.
It works offline and has no telemetry or configuration file.

## Safety

A shortcut is selectable only when all three conditions hold:

1. Windows parsed the Shell Link without resolving or updating it.
2. Its target is an absolute path on a local fixed drive.
3. The target is definitively absent.

Shell namespace targets, empty or relative paths, unresolved environment variables, network paths, removable drives, access failures, and parse failures remain ineligible.
Missing working directories, icons, or argument paths do not make a shortcut stale.

Before deletion, startclean checks that the shortcut remains inside its approved known-folder root, reloads it, and confirms that the same target is still stale.
It rejects an all-users selection before changing anything unless the process is elevated.
Successful deletion is announced to the Windows Shell.
Explorer is never killed or restarted.

The tool leaves application files, pinned-layout databases, taskbar pins, Microsoft Store registrations, `.url` files, registry entries, and non-empty application folders untouched.
Known folders come from `SHGetKnownFolderPath`, following [Microsoft's Start Menu shortcut model](https://learn.microsoft.com/en-us/windows/win32/shell/how-to-add-shortcuts-to-the-start-menu).

## Usage

Build the executable using the [installation instructions](../README.md#installation), then run it from PowerShell.

```powershell
.\startclean.exe scan
.\startclean.exe scan --scope all --format table
.\startclean.exe scan --show-skipped
.\startclean.exe scan --format json --check
.\startclean.exe doctor --format json
```

Double-click the executable, or run it without arguments, to open the interactive interface.
Nothing starts selected.

- `Space` toggles one eligible item.
- `a` toggles all eligible items.
- `Enter` opens the review and exact-count confirmation.
- `?` opens help.
- `q` or `Esc` cancels without deleting.

All-users entries are visible but locked when the process is not elevated.
The detail panel shows a `Start-Process ... -Verb RunAs` command for the current executable.
Relaunching opens a new review without preselecting or deleting anything.
The tool never requests automatic elevation.

Automation requires both deletion flags.
Without a terminal, a clean command that lacks either flag is refused.

```powershell
.\startclean.exe clean --all --yes
```

Enable completion in the current PowerShell session with:

```powershell
.\startclean.exe completion powershell | Out-String | Invoke-Expression
```

### Exit codes

| Code | Meaning |
| ---: | --- |
| 0 | Success or cancellation |
| 1 | Operational or partial failure |
| 2 | Invalid usage |
| 3 | Candidates found by `scan --check` |
| 4 | Elevation required |

JSON output uses a `schema_version: 1` envelope.
The `--color auto|always|never` option honors `NO_COLOR`.

## Development

The product targets Windows x64 and arm64 and builds without CGO.
The [mise configuration](https://github.com/P4suta/startclean/blob/main/mise.toml) pins the development tools, and its lockfile covers Linux x64 and both Windows architectures.
The Go devtool implements the project commands; just and Lefthook provide thin entry points.

```powershell
mise install
mise x -- go run ./cmd/devtool ci
```

Use `devtool help` for individual checks.
Generated files in [completions](../completions/) are release assets.
Integration tests create real Shell Links in temporary directories and never modify the live Start Menu.

Every pull request checks workflow metadata, prose, licenses, secrets, and the required-job predicate.
The predicate check imports the production filter and covers all 8,192 combinations of job results and request modes, malformed inputs, and deliberately broken controls.
Runtime changes select the native Windows quality, race, and policy checks.
Packaging changes select a fast release configuration check.
Fuzz seeds run with ordinary tests; extended fuzzing and stress repeats run in release verification or on demand.

Coverage floors protect the whole project at 65%, the deletion policy at 80%, and the Windows native boundary at 65%.
Native COM integration, race detection, Go vet, semantic lint, dependency audits, CodeQL, and dependency review cover their respective boundaries.

## Releases

The tag workflow verifies the source before building unsigned Windows x64 and arm64 ZIP archives.
It stages checksums, SPDX 2.3 and CycloneDX SBOMs, completions, and GitHub artifact attestations before publishing the assembled draft.
Binaries remain unsigned until a signing certificate is configured.

For a downloaded release, verify its checksum and provenance with:

```powershell
Get-FileHash .\startclean_*_Windows_x86_64.zip -Algorithm SHA256
Get-Content .\checksums.txt
gh attestation verify .\startclean_*_Windows_x86_64.zip --repo P4suta/startclean
```

Attestations identify the producing repository workflow.
They do not replace code review or vulnerability analysis.
See [GitHub's attestation documentation](https://docs.github.com/en/actions/concepts/security/artifact-attestations).

## License

Licensed under either the [MIT license](../LICENSES/MIT.txt) or the [Apache License 2.0](../LICENSES/Apache-2.0.txt), at your option.
The repository follows REUSE Specification 3.3.
