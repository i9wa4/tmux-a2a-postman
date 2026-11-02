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

Historical tracked task notes are indexed in the
[retained evidence map](docs/validation/740-retired-task-artifacts.md).
Record new public verification with its owning issue/PR or relevant document.

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

The default matrix has twenty-six cases: plain editor input and bracketed-paste
input, each covering rally, busy recipient, concurrent independent messages,
parent-to-child, child-to-parent, approval, rejection, blocked notification,
an Enter absorbed as newline, an eight-request approval burst, and external
done/blocked/exact-decision round trips.
`TestNotificationDrivenTerminalAgents` runs it
during `go test` and `nix build`. Nix flake checks also build this package.
Default checks require no AI client or model server. `--case` selects a single
case.

Sends retain a 15-second subprocess deadline and record elapsed time. The busy
case withholds recipient processing until the sender returns, then verifies
the full round trip. This proves sending does not wait for a response without
adding a shorter wall-clock threshold that can reject healthy concurrent sends
on a loaded CI runner. Notification acceptance, exact claims/replies and
complete-file digests remain mandatory.

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
Durable claim receipts and saved decisions count as progress before the final
tool proof exists. Burst idle admission allows at least 20 seconds for bounded
post-commit/dispatch phases; it still has a finite overall batch deadline and
requires every final proof and execution marker.
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

- Keep `go.mod` at the intended language major.minor (`go 1.26`).
- `goToolchain = pkgs.go_1_26` is the single selection for development,
  CI, the Nix package builder and GoReleaser's release shell. Release targets
  use that compiler with CGO disabled. Tools such as the linter may themselves
  be compiled by another Go version; that is not this program's compiler.
- Use the patch supplied by the pinned nixpkgs revision. Update normally with
  `nix flake update nixpkgs`, inspect the selected patch, then run the normal
  flake/build/skill and vulnerability checks. A lock update can change other
  Nix packages, so review and validate the complete result.
- `nix develop .#ci --command bash scripts/validation/check-go-toolchain.sh`
  checks the actual selected compiler against `go.mod`, with automatic
  toolchain downloading disabled for the probe. CI, pre-commit/Nix checks and
  the release workflow use the same check. Do not infer the compiler from an
  override-rewriting regex or the runner's preinstalled Go.
- Check current Go support and security at update time using the
  [release policy](https://go.dev/doc/devel/release#policy),
  [official stable downloads](https://go.dev/dl/?mode=json) and `govulncheck`
  against the actual selected build. The implementation-time check found
  Go 1.26.10 and 1.27.3 as the two current stable release lines; this repository
  intentionally retains language/compiler minor 1.26 and selects 1.26.10.
- For an urgent patch missing from the usual nixpkgs branch, first pin a
  reviewed public nixpkgs commit that supplies it. If none exists yet, use
  one temporary source override at `goToolchain`, with the official source
  hash, so all consumers stay aligned. Run tidy, the no-substitution package
  build, vulnerability checks and CI; remove the temporary override when
  nixpkgs supplies the patch. There is no override-coupled updater app.
- A minor migration updates `go.mod` and the `go_1_26` selection together,
  then validates development, CI, package and release builds.

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
