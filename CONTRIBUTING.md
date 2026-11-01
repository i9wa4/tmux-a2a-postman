# Contributing

## 1. Local Checks

After implementation work:

- Stage new files before running Nix checks because flakes only see
  git-tracked files
- Run `nix flake check`
- Run `nix build`
- Check that `README.md` and `skills/*/SKILL.md` do not mention removed
  commands, renamed flags, or deleted packages

The root `main.go` keeps the existing `go install`/`go build .` entrypoint.
CLI process handling and its unit/integration tests live in `internal/app/`;
reusable command, configuration and mail code remain in their existing
`internal/` packages. Design and verification documents live under `docs/`,
and deterministic terminal drivers under `scripts/validation/`.

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

The default matrix has twenty cases: plain editor input and bracketed-paste
input, each covering rally, busy recipient, concurrent independent messages,
parent-to-child, child-to-parent, approval, rejection, blocked notification,
an Enter absorbed as newline, and an eight-request approval burst.
`TestNotificationDrivenTerminalAgents` runs it
during `go test` and `nix build`. Nix flake checks also build this package.
Default checks require no AI client or model server. `--case` selects a single
case.

Generated test topologies include numbered `common_template` and
`message_footer` sections. Additional regression cases use a production-shaped
configuration with role prose, skill metadata and the full role graph for
rallies and approved/rejected commands. They also check final `get-status`
after the complete rally.

To test an existing configuration unchanged in private fixture panes:

```sh
nix run .#notification-smoke -- --case rally \
  --postman-md "$HOME/.config/tmux-a2a-postman/postman.md"
```

This option copies the source into the temporary fixture; it never edits the
original. Use `--case rally`, `approval` or `rejected`, with the configured
`orchestrator`/`worker` or `orchestrator`/`approver` roles respectively.

The burst receiver drains only known positive unread counts after its first
notification. It does not require one hint per message. The controller waits
on claim/completion progress with a bounded idle and batch deadline, since
concurrent requests have no FIFO guarantee. Every request still needs its
own full-read evidence, exact decision and exactly-once execution marker.
Unavailable hints can coexist with successful batch processing; uncertain
input remains a separate guarded outcome.

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
evidence stays at the printed path. The `--baseline` control is a historical
comparison against pre-notification main, not an acceptance test of the current
CLI. The blocked case enters only fixture tmux copy mode; delivery remains
unread. Tests never provide receiver POP/reply actions from the controller.

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

### 3.1. Deterministic trigger expectations

Every `skills/<name>/SKILL.md` needs a corresponding
`evals/<name>/trigger/eval.yaml` and positive/negative YAML tasks under that
suite's `tasks/` directory. CI enforces this through `nix flake check`;
the local hook responds to both skill and trigger-fixture changes.

```sh
nix run .#skill-trigger-check
# In nix develop, inspect metadata without starting an evaluator:
python3 scripts/validation/validate-skill-triggers.py --preflight-only
python3 scripts/validation/test-skill-triggers.py
```

The repository provisions Waza CLI v0.39.1 with fixed release hashes for
Linux x86_64/aarch64 and macOS aarch64. All suites explicitly select its
`mock` executor and `offline-mock` model label, one serial trial and bounded
timeouts. Waza's update check is disabled. No model SDK, credentials or model
API is needed. The selected release's trigger grader scores local skill
keywords and USE FOR phrases, including the skill body. It does not simulate
real-model routing or prove task success/comprehension. Overlapping vocabulary
can produce heuristic false positives; use concrete responsibility boundaries
and inspect feedback rather than flipping an authored expectation to pass.

The validator preflights **all** discovered trigger suites before starting any
executor. It rejects zero/missing/orphan coverage, empty tasks, implicit or
model-backed executors, missing/non-boolean expectations, unsupported graders,
incorrect skill references, unknown fields, duplicate YAML keys, aliases and
ambiguous/symlinked paths. Each task owns exactly one supported trigger grader;
suite graders/resources/hooks cannot introduce model work. A valid but wrong
expectation reaches the real grader and fails the command. Any failure stops
the check with the skill/case and a nonzero exit.

The negative controls exercise missing and zero discovery, a newly added
uncovered skill, malformed metadata, forbidden executors/graders and an
intentionally incorrect expectation under the actual evaluator. Fix the
reported metadata or intended routing case, then rerun the command. Adding
`postman-recall` also requires its own trigger suite; retired skill names do
not become coverage requirements.

Existing `evals/<name>/eval.yaml` and `evals/<name>/tasks/` remain manual
behavioral suites. The automated gate selects only `evals/*/trigger/eval.yaml`
and never implicitly executes or rewrites the behavioral lane. Skill metadata
and publishing dry-runs remain separate required checks.

## 4. Releases

See [RELEASING.md](RELEASING.md) for the release checklist and tag ruleset
expectations.

Canonical agent instructions live in [AGENTS.md](AGENTS.md).
