# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/).

## [Unreleased]

### Added

- feat: load project-local config via upward CWD traversal with field-level merge (#121)
- feat: enrich pane-activity.json with lastChangeAt; dual-format reader (#123)
- `enter_count` field in `nodes` config to send multiple Enter keystrokes for Codex CLI nodes (#126)

### Fixed

- fix: use C-m instead of Enter in SendToPane to reliably submit in Codex CLI readline mode (#126-followup)
- fix: capture pane before sending idle reminder; send on capture failure (#125)
- fix: filter idle alerts to edge panes; fallback to all panes on list-panes failure (#124)
- fix: remove ChangeCount==0 stale early-return from GetPaneActivityStatus (#122)

## [v0.3.4] - 2026-02-19

### Fixed

- fix: use TMUX_PANE target in pane/session getters; inject --from in reply_command

## [v0.3.3] - 2026-02-19

### Fixed

- fix: suppress output in get-session-status-oneline when no edge panes match

## [v0.3.2] - 2026-02-19

### Added

- feat: 3-state pane status and edge filter in get-session-status-oneline

## [v0.3.1] - 2026-02-19

### Added

- feat: change session toggle key from space/enter to enter only
- feat: add gg (top) and G (bottom) navigation to session list
- feat: show legend only on Routing tab, not Events tab

### Fixed

- fix: get-session-status-oneline scans all active session directories
- fix: get-session-status-oneline returns empty output when no context active
- fix: filter events strictly by selected session; unify log.Printf to log.Println
- fix: apply edge filter to pane capture discovery in StartIdleCheck
- fix: prevent tab label layout shift by reserving space for active marker
- fix: session toggle no longer drops sessions from list
- fix: apply edge filter to startup discovery and remove sessions separator

## [v0.3.0] - 2026-02-19

### Added

- feat: replace A2A_NODE env var with tmux pane title for node discovery

### Fixed

- fix: filter events strictly by selected session; unify log.Printf to log.Println
- fix: apply edge filter to pane capture discovery in StartIdleCheck

## [v0.2.1] - 2026-02-17

### Changed

- chore: migrate to tag-driven release workflow

### Fixed

- fix: correct ldflags package path for version embedding

### Docs

- docs: simplify RELEASING.md documentation
- docs: update get-session-status-oneline README to match implementation

## [v0.2.0] - 2026-02-16

### Added

- feat: unify activity window to 300s across components
- feat: make activity window configurable
- feat: add get-session-status-oneline command

### Changed

- chore: migrate to tag-driven release workflow
- refactor: use idle.go for get-session-status-oneline

### Fixed

- fix: PING respects session enabled state (#119)
- fix: daemon alerts rate-limiting via ui_node messages (#118)
- fix: periodic discovery node map update + show ALL tmux sessions (#117)

### Docs

- docs: update README for #117 and #119 changes

## [v0.1.2] - 2026-02-11

### Fixed

- fix: resolve pane disappearance detection and config default issues
- fix: remove hardcoded "concierge" default and add validation

### Changed

- test: remove tests for deleted digest feature

### Docs

- docs: update README template variables from inbox_path to session_dir

## [v0.1.1] - 2026-02-11

### Added

- feat: add inbox unread count summary notification
- feat: implement hybrid idle detection with pane capture
- feat: add memory leak prevention and configurable node state thresholds
- feat: implement peterldowns-style version management
- feat: migrate to tag-based version management

### Changed

- refactor: move main.go to project root for single binary architecture
- refactor: remove digest feature completely

### Fixed

- fix: use prevPaneToNode for disappeared pane lookup
- fix: separate screen change from idle detection and allow unlimited panes
- fix: enable pane restart detection via prevPaneToNode mapping
- fix: resolve digest node lookup and set default template
- fix: address observer-a review findings
- fix: address observer-b IMPORTANT findings (6 issues) and update flake.nix
- fix: use NodeIdleSeconds for idle→stale threshold in TUI
- fix: use flake-based cd shell for reproducible builds
- fix: ensure idempotency in release workflow when tag exists
- fix: update Makefile and .goreleaser.yaml after main.go relocation

### Changed

- chore: simplify Makefile build target by removing unused ldflags

## [v0.1.0] - 2026-02-10

Initial release.

[Unreleased]: https://github.com/i9wa4/tmux-a2a-postman/compare/v0.3.4...HEAD
[v0.3.4]: https://github.com/i9wa4/tmux-a2a-postman/compare/v0.3.3...v0.3.4
[v0.3.3]: https://github.com/i9wa4/tmux-a2a-postman/compare/v0.3.2...v0.3.3
[v0.3.2]: https://github.com/i9wa4/tmux-a2a-postman/compare/v0.3.1...v0.3.2
[v0.3.1]: https://github.com/i9wa4/tmux-a2a-postman/compare/v0.3.0...v0.3.1
[v0.3.0]: https://github.com/i9wa4/tmux-a2a-postman/compare/v0.2.1...v0.3.0
[v0.2.1]: https://github.com/i9wa4/tmux-a2a-postman/compare/v0.2.0...v0.2.1
[v0.2.0]: https://github.com/i9wa4/tmux-a2a-postman/compare/v0.1.2...v0.2.0
[v0.1.2]: https://github.com/i9wa4/tmux-a2a-postman/compare/v0.1.1...v0.1.2
[v0.1.1]: https://github.com/i9wa4/tmux-a2a-postman/compare/v0.1.0...v0.1.1
[v0.1.0]: https://github.com/i9wa4/tmux-a2a-postman/releases/tag/v0.1.0
