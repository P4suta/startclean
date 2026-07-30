<!--
SPDX-FileCopyrightText: 2026 startclean contributors <https://github.com/P4suta/startclean/graphs/contributors>
SPDX-License-Identifier: MIT OR Apache-2.0
-->

# startclean

`startclean` is a focused Windows 10/11 CLI for finding and removing orphaned
Start Menu shortcuts. It examines `.lnk` files in the per-user and all-users
Programs known folders and permanently removes only high-confidence candidates
that you explicitly select.

Nothing is selected when the interactive interface opens. `startclean` does not
modify application files, pinned-layout databases, taskbar pins, Microsoft Store
registrations, `.url` files, registry entries, or non-empty application folders.
It is offline, telemetry-free, and configuration-free.

## Safety model

A shortcut is selectable only when all of these statements are true:

1. Windows parsed the Shell Link successfully without resolving or updating it.
2. Its target is an absolute path on a local fixed drive.
3. The target is definitively absent.

Shell namespace targets, empty or relative paths, unresolved environment
variables, UNC/network paths, removable drives, access failures, and parse
failures remain visible when requested but are never eligible. Missing working
directories, icons, or argument paths do not make a shortcut stale.

Immediately before deletion, `startclean` verifies that each `.lnk` remains
inside the approved known-folder root, reloads it, and confirms that the same
target is still stale. An all-users selection is rejected before any mutation
unless the process is elevated. Successful deletion is announced to the Windows
Shell; Explorer is never killed or restarted.

The known folders are discovered with `SHGetKnownFolderPath`, following
[Microsoft's Start Menu shortcut model](https://learn.microsoft.com/en-us/windows/win32/shell/how-to-add-shortcuts-to-the-start-menu).

## Usage

On Windows, you can double-click `startclean.exe` in Explorer to open the
interactive TUI. Unlike Cobra's default command-line behavior, startclean does
not reject Explorer launches or direct users specifically to `cmd.exe`.

From PowerShell, run the executable from its extracted directory:

```powershell
.\startclean.exe
.\startclean.exe scan
```

To enable completion in the current PowerShell session:

```powershell
.\startclean.exe completion powershell | Out-String | Invoke-Expression
```

```text
startclean
startclean clean
startclean scan --scope all --format table
startclean scan --show-skipped
startclean scan --format json --check
startclean clean --all --yes
startclean doctor --format json
startclean completion powershell
startclean version
```

Interactive keys:

- `Space` toggles one eligible item.
- `a` toggles all eligible items.
- `Enter` reviews and continues to the exact-count confirmation.
- `?` opens contextual help.
- `q` or `Esc` cancels without deleting.

All-users entries are shown but locked when not elevated. The detail panel shows
an exact `Start-Process ... -Verb RunAs` command using the current executable;
it relaunches the interactive review without preselecting or deleting anything.
There is no automatic UAC elevation.

For automation, deletion requires both `--all` and `--yes`. A non-TTY invocation
without these explicit flags refuses to clean.

### Exit codes

| Code | Meaning |
| ---: | --- |
| 0 | Success or user cancellation |
| 1 | Operational or partial failure |
| 2 | Invalid usage |
| 3 | Candidates found by `scan --check` |
| 4 | Elevation required |

JSON output has a stable `schema_version: 1` envelope. Global
`--color auto|always|never` honors the `NO_COLOR` environment variable.

## Development

The supported runtime targets are Windows x64 and arm64. `mise.toml` provides a
fully version-pinned Go and development-tool environment; `mise.lock` records
resolved URLs, checksums, and available upstream provenance for Windows x64,
Windows arm64, and Linux x64. After activating mise, install that environment
and run the canonical local gate:

```powershell
mise install
go run ./cmd/devtool ci
```

The Go-based devtool is the source of development workflow logic; it does not
depend on Bash or PowerShell scripts. Use `go run ./cmd/devtool help` to list
focused commands such as `format`, `generate`, `lint`, `test`, `integration`,
`stress`, `coverage`, `module-verify`, `race`, `fuzz`, `security`, `reuse`, `build`,
`release-source`, `release-check`, and `release-smoke`.

`just` recipes and Lefthook hooks are optional thin wrappers around the same
`go run ./cmd/devtool ...` commands. They add convenience, not a second task
implementation.

The generated Bash, Zsh, Fish, and PowerShell files under `completions/` are
command-completion assets shipped with releases. They are not development
scripts and do not introduce a shell dependency into the development workflow.

Integration tests create real Shell Links only under temporary test directories.
Automated tests never write to or delete from the live Start Menu.

The quality gates deliberately overlap: table-driven and failure-injection
tests exercise policy, golden tests freeze the JSON contract, the standard Go
race detector checks concurrency, native Go fuzzing stresses containment and
conservative classification, and Windows integration tests use real COM Shell
Links. `golangci-lint`, `go vet`, CodeQL, `govulncheck`, OSV-Scanner, Gitleaks,
Dependency Review, OpenSSF Scorecard, REUSE, and a 65% non-regression coverage
floor cover different classes of defects. Separate floors protect the deletion
policy core at 80% and the Windows native/COM boundary at 65%, preventing broad
packages from masking regressions in safety-critical code. The heavier race,
fuzz, and release
snapshot checks run as independent GitHub Actions jobs. When fuzzing discovers
a new crashing input, CI retains the generated reproducer as a short-lived
artifact instead of losing it with the runner.

Release tags matching `v*` produce unsigned Windows x64 and arm64 ZIP archives,
SHA-256 checksums, Syft-generated SPDX 2.3 and CycloneDX SBOMs, completions, and
Sigstore-backed GitHub artifact attestations. The release smoke test builds both
architectures, inspects each ZIP, validates both SBOM formats through Syft, and
checks every published digest before a tag is accepted. Binaries remain
unsigned until a code-signing certificate is available. Release automation
assembles a retryable draft and publishes it only after every asset and
attestation succeeds, so immutable releases never expose a half-finished set.

After downloading a release, PowerShell users can inspect its checksum and
verify its build provenance with the GitHub CLI:

```powershell
Get-FileHash .\startclean_*_Windows_x86_64.zip -Algorithm SHA256
Get-Content .\checksums.txt
gh attestation verify .\startclean_*_Windows_x86_64.zip --repo P4suta/startclean
```

Artifact attestations establish which repository workflow produced a file;
they do not replace code review or vulnerability analysis. See
[GitHub's artifact-attestation documentation](https://docs.github.com/en/actions/concepts/security/artifact-attestations).

## License

Licensed under either the [MIT license](LICENSES/MIT.txt) or the
[Apache License 2.0](LICENSES/Apache-2.0.txt), at your option. The repository
follows REUSE Specification 3.3.
