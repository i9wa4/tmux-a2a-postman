# Contributing

## 1. Local Checks

After implementation work:

- Stage new files before running Nix checks because flakes only see
  git-tracked files
- Run `nix flake check`
- Run `nix build`
- Check that `README.md` and `skills/*/SKILL.md` do not mention removed
  commands, renamed flags, or deleted packages

Transport tests use isolated real tmux servers, temporary HOME/state, and the
Bash/Readline adapter shipped in the CLI. Run them without model credentials:

```sh
go test ./... -count=1
go test . -run 'TestNotification' -v -count=1
```

The notification tests perform only the initial send from the controller.
The recipient hook owns POP, full-file reading and exact reply. The disabled
adapter case proves no receipt or reply is fabricated. Containers must reap
orphan processes (for example Docker `--init`), so tmux cleanup can verify
server exit. Remove only owned fixtures; never kill unrelated tmux servers.

The **real Codex** suite uses a local Responses API fixture that returns fixed
tool calls, so there are no credentials, paid model calls, or model reasoning
requirements. Actual Codex accepts native notices and executes the recipient
helper in the same conversation. The controller never POPs or replies.

```sh
nix run .#codex-notification-smoke -- --case all --output /tmp/postman-report.json
nix run .#codex-notification-smoke -- --case concurrent
# Or use installed tools without Nix:
python3 scripts/validation/codex-notification-smoke.py \
  --postman "$(command -v tmux-a2a-postman)" --codex "$(command -v codex)" --case all
```

Cases: `rally`, `busy`, `concurrent`, `parent-child`, `child-parent`,
`approval`, `rejected`, `disabled`. The default runs all; `nix flake check`
includes the same suite. Checks assert original thread/context, full-file
digests, exact replies, unique claims, bounded send, busy queueing and preserved
unsubmitted drafts. Go fault tests cover duplicate/concurrent same-message
notices, wrong runtime identity, disconnect, mismatched receipts, timeout and
short-lived helper reaping.

The private fixture matches vde-layout's `--yolo --no-daemon --no-alt-screen`
Codex launch. It creates its own HOME, CODEX_HOME, tmux socket, state and model
endpoint. Its known tool commands run in that fixture. Production Codex policy
is not changed by registration. Reports include runtime versions, thread and
mail IDs, claims, hashes, model-fixture trace and pane transcripts; temporary
evidence remains at the printed path. Only owned tmux servers are killed.

`--mode live` explicitly uses the installed Codex authentication selector and
chosen `--model` with the same isolated launch; it makes real model calls and
fails if authentication/model/tool execution fails. There is no fallback from
live to mock. Default mock mode never reads or copies authentication. To compare
old main, build its binary separately and run `--case rally --baseline` against
it: durable delivery must remain unread for more than the native queue watcher
interval, with no recipient action. The disabled case tests the same negative
control on the new binary by removing only fixture registration.

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
