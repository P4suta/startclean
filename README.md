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
an exact `Start-Process ... -Verb RunAs` command using the current executable.
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

The supported runtime targets are Windows x64 and arm64. Install the exact
toolchain and run the complete local gate:

```powershell
mise install
just setup
just ci
```

Useful focused recipes include `just format`, `just generate`, `just lint`,
`just test`, `just integration`, `just coverage`, `just security`,
`just reuse`, and `just release-check`.

Integration tests create real Shell Links only under temporary test directories.
Automated tests never write to or delete from the live Start Menu.

Release tags matching `v*` produce unsigned Windows x64 and arm64 ZIP archives,
checksums, SPDX SBOMs, completions, and provenance attestations. Binaries will
remain unsigned until a code-signing certificate is available.

## License

Licensed under either the [MIT license](LICENSES/MIT.txt) or the
[Apache License 2.0](LICENSES/Apache-2.0.txt), at your option. The repository
follows REUSE Specification 3.3.
