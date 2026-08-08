# Changelog

All notable changes to tuido are documented in this file.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project follows [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.5.0] - 2026-08-08

### Added

- `tuido init` now asks whether to keep tuido's automatic commits off your
  GitHub contribution graph (flag equivalent: `--commit-email`). Saying yes
  sets a repo-local author email GitHub cannot link to your account, so
  autosave commits never count. The setting is per-clone; opt an existing
  repo in with `git -C <root> config user.email tuido@localhost`.

## [0.4.0] - 2026-08-08

### Added

- Added the `:done` and `:drop`/`:cancel` shorthand tokens: they close the
  task (`[x]` or `[-]`) and stamp today's ✅/❌ date. Deliberately no ➕ stamp —
  closing a task says nothing about when it was written.
- `tuido fmt` now turns a bare `- some task` dash bullet into a checkboxed
  task stamped as created today. `*` and `+` bullets and `- [...` lines
  (markdown links, checkbox attempts) are never touched, so notes stay notes.

## [0.3.2] - 2026-08-08

### Added

- Added the undocumented `tuido _commit <path>` plumbing command for editor
  save hooks: it commits the one saved file with an `edit: <ref>` message and
  kicks off the usual detached background push, so edits made in the editor
  sync exactly like edits made through tuido commands. Documented the paired
  `BufWritePre`/`BufWritePost` Neovim integration in the README.

## [0.3.1] - 2026-08-08

### Added

- Added the `:new` shorthand token to stamp a task with today's creation date
  without setting another field.

### Fixed

- `tuido fmt` now repairs lazily typed `- []` checkboxes to `- [ ]` and stamps
  them as newly created, including indented subtasks, while leaving fenced code
  and non-checkbox text untouched.

## [0.3.0] - 2026-08-08

### Added

- Added `tuido fmt [list]` and the `tuido fmt -` stdin/stdout filter for
  expanding plain-ASCII priority and date shorthand into the canonical
  Obsidian Tasks emoji format.
- Added idempotent shorthand expansion, explicit warnings for malformed or
  conflicting fields, creation-date stamping for newly captured tasks, and
  warnings for fields accidentally hard-wrapped onto continuation lines.
- Added zsh completion and Neovim save-time integration guidance for `fmt`.
- Added worked sorting examples showing flat and heading-delimited files.

## [0.2.0] - 2026-08-08

### Added

- Added checksum-verified, atomic self-updates through `tuido upgrade`.
- Added detached background release checks that do not add network latency to
  normal commands.
- Added reproducible macOS and Linux release binaries for arm64 and amd64 with
  a SHA-256 manifest.

### Fixed

- Fixed version reporting for `go install` binaries and prevented development
  builds from running background update checks.

## [0.1.1] - 2026-08-08

### Added

- Added discoverable command-specific help, usage examples, and exit-code
  documentation.

## [0.1.0] - 2026-08-08

### Added

- Initial public release with lossless Obsidian Tasks parsing, fence-bounded
  sorting, quick capture and completion, fuzzy task addressing, workspace and
  list resolution, non-blocking Git synchronization, and terminal rendering.

[Unreleased]: https://github.com/ramtinJ95/tuido/compare/v0.5.0...HEAD
[0.5.0]: https://github.com/ramtinJ95/tuido/compare/v0.4.0...v0.5.0
[0.4.0]: https://github.com/ramtinJ95/tuido/compare/v0.3.2...v0.4.0
[0.3.2]: https://github.com/ramtinJ95/tuido/compare/v0.3.1...v0.3.2
[0.3.1]: https://github.com/ramtinJ95/tuido/compare/v0.3.0...v0.3.1
[0.3.0]: https://github.com/ramtinJ95/tuido/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/ramtinJ95/tuido/compare/v0.1.1...v0.2.0
[0.1.1]: https://github.com/ramtinJ95/tuido/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/ramtinJ95/tuido/releases/tag/v0.1.0
