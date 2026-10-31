# Contributing

## 1. Local Checks

After implementation work:

- Stage new files before running Nix checks because flakes only see
  git-tracked files
- Run `nix flake check`
- Run `nix build`
- Check that `README.md` and `skills/*/SKILL.md` do not mention removed
  commands, renamed flags, or deleted packages

Transport tests use isolated real tmux servers and private HOME/state. The
controller performs only the initial send or execute request; deterministic
agent consoles receive the actual pasted notification and `C-m`, then own POP,
complete-file reading and exact replies. No model or runtime API is required.

```sh
go test ./... -count=1
nix run .#notification-smoke -- --case all --output /tmp/postman-report.json
# Without Nix:
python3 scripts/validation/notification-smoke.py \
  --postman "$(command -v tmux-a2a-postman)" --agent fixture --terminal both
```

The default matrix has sixteen cases: plain line input and bracketed-paste
input, each covering rally, busy recipient, concurrent independent messages,
parent-to-child, child-to-parent, approval, rejection and blocked notification.
`TestNotificationDrivenTerminalAgents` runs it during `go test` and `nix build`.
Nix flake checks also build this package. Default checks require no AI client
or model server. `--case` selects a single case.

Go fault tests cover duplicate/concurrent same-message attempts, ambiguous
transport outcomes, pre-paste rejection, mailbox-lock independence,
wrong/uncommitted routes, and shell input rejection. No unknown attempt is
replayed.

Optional real Codex checks use a local Responses API fixture with fixed tool
calls. This replaces model decisions only; the actual Codex input editor and
its recipient tools handle the notification. It needs no account or API key:

```sh
python3 scripts/validation/notification-smoke.py --agent codex --case rally \
  --codex "$(command -v codex)" --postman "$(command -v tmux-a2a-postman)"
```

The private Codex test matches vde-layout's `--yolo --no-daemon --no-alt-screen`
launch. Production transport has no Codex dependency or API calls. `--mode live`
explicitly uses installed model authentication and does not fall back to mock.
Actual Claude model behavior is not asserted by terminal fixtures; they verify
the common terminal protocol also used by local consoles and Claude Code.

Reports include submitted terminal text, conversation/context identifiers,
archive digests, exact request/reply IDs and pane transcripts. Temporary
evidence stays at the printed path. The old-main `--case rally --baseline`
control must commit mail but produce no recipient actions. The blocked case
enters only fixture tmux copy mode; delivery remains unread. Tests never provide
receiver POP/reply actions from the controller.

Containers must reap orphan processes (for example Docker `--init`). Remove
only owned fixtures; never kill unrelated tmux servers. Stage new files before
Nix checks so the flake source includes the complete runtime tests.

Go version policy:

- Keep `go.mod` at major.minor (for example `go 1.26`)
- Keep `flake.nix` on the same major.minor (`pkgs.go_1_26`)
- Keep the `flake.nix` Go override on the latest stable same-minor patch release
  published by go.dev
- To update the Go patch override, run:

  ```sh
  nix run .#update-go-toolchain
  ```

- The command reads the current Go major.minor from the `go126` override in
  `flake.nix`, queries go.dev for the latest stable patch release for that same
  major.minor, and updates only the override version/hash in `flake.nix` when a
  newer patch exists.
- If the override is already current, the command exits 0 with
  `status=up_to_date` and leaves the repository unchanged.
- After the command updates `flake.nix`, run `nix flake check` and `nix build`
  before opening the update PR.
- Minor-version migrations still require manually updating the hard-coded
  `go126` / `go_1_26` names and then rerunning the alignment checks

When changing Go dependencies, `go.mod`, `go.sum`, Go major.minor versions, or
`vendorHash`:

- Run `go mod tidy`
- Run `nix build --option substitute false --print-build-logs`
- If Nix reports a `vendorHash` mismatch, copy the reported `got:` hash into
  `flake.nix` and rerun the build

## 2. Markdown Formatting

`markdown-formatter` covers all tracked Markdown files with its default
heading-numbering behavior enabled. Run `nix fmt` for treefmt-managed
formatting and let the pre-commit hook apply `mdfmt --write` to Markdown files.

The repository does not maintain separate root-doc or skill exceptions. Ignored
or generated files such as `.pre-commit-config.yaml` are not repository
Markdown policy surfaces.

## 3. Agent Skills

Validate publishable skill metadata with:

```sh
nix run '.#skill-check'
```

The local/CI skill check validates `skills/*/SKILL.md` frontmatter,
name-to-directory matching, license and description metadata, `USE FOR` /
`DO NOT USE FOR` discovery text, and then runs the GitHub skill publish
dry-run. `nix flake check` also runs the metadata validation through
pre-commit.

Do not use `gh skill publish --tag` in the tag-push release workflow. That
command creates a tag and GitHub Release itself. The repository release flow
uses the pushed `v*` tag plus GoReleaser, while `gh skill install` resolves
skills from the published repository tag or release.

## 4. Releases

See [RELEASING.md](RELEASING.md) for the release checklist and tag ruleset
expectations.

Canonical agent instructions live in [AGENTS.md](AGENTS.md).
