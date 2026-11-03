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

For AI-led environment preparation, hypothesis selection, pane/log inspection
and iterative debugging in a cloud environment, follow the
[cloud development runbook](docs/development/cloud-tmux-debugging.md).

README is the product entry point; [quickstart](docs/quickstart.md),
[configuration reference](internal/helpdocs/topics/postman-md.md)
and [CLI specification](docs/cli-spec.md) own current user contracts. Keep
developer check commands, fixture matrices and evidence limits here or in the
linked development/validation documents. Canonical operating instructions live
in `internal/helpdocs/topics/` and are embedded in the binary.
`internal/helpdocs/entries/` owns the thin Skills;
`go generate ./internal/helpdocs` refreshes their checked public `skills/`
copies and the readable documentation/helper mirrors. Go tests reject drift.
Required filesystem helpers and license notices ship in the same embedded
resource bundle; no checkout is needed to use installed help or procedures.
`CLAUDE.md` points to the one repository instruction owner, AGENTS; runtime
`postman.md` is a separate user configuration surface.

Validate maintained example configurations and their first exchange with
`go test ./internal/config ./internal/cli ./internal/app -run 'TestDocumented'`.
These checks use the real parser, installed catalog metadata and isolated native
mail flow. Mermaid presentation rendering is a separate check; explanatory
README arrows are not valid routing configuration. See the dated
[documentation validation note](docs/validation/982-documentation.md).

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

The default matrix has thirty cases: plain editor input and
bracketed-paste input, each covering rally, busy recipient, concurrent
independent messages, parent-to-child, child-to-parent, approval, rejection,
blocked notification, an always-absorbed Enter, a first-Enter newline recovered
by a spaced retry, an eight-request approval burst, and external
done/blocked/exact-decision round trips. `TestNotificationDrivenTerminalAgents`
runs it during `go test` and `nix build`. Nix flake checks also build this
package. Default checks require no AI client or model server. `--case` selects a
single case.

Sends retain a 15-second subprocess deadline and record elapsed time. The busy
case withholds recipient processing until the sender returns, then verifies the
full round trip. This proves sending does not wait for a response without adding
a shorter wall-clock threshold that can reject healthy concurrent sends on a
loaded CI runner. The notification heuristic is recorded separately; exact
claims/replies and complete-file digests remain mandatory.

Recursive team validation is also owned here:

```sh
nix run .#recursive-smoke
nix run .#recursive-smoke -- --case chain
```

The `recursive-teams` flake check runs the same development fixture: a root,
four-level chain with restart and stale replies, branching six-level tree with
independent simultaneous requests, rejection and a missing child. Real terminal
input drives every recipient's POP/full read/reply and exact execution decision.
The default policy, Skill metadata and test artifacts are Postman-owned; no
dotfiles checkout, installed Skill, client launcher or Logbook program is
needed. These are fixed test actors, not a product task runtime or proof of
model review. See the
[recursive trial guide](docs/recursive-sessions-trial-runbook.md) for an
explicit optional consumer-policy check. CLI commands and their flags do not
change.

The recursive check also runs the builtin root exchange with existing consoles
and each of the four presets in the default `postman.md`: plain `codex` and
`claude` (eight roles), plus vde-layout `preset-p` and `preset-w` (seven roles).
Private stubs replace both client binaries, including their declared arguments;
no model API, authentication or permission configuration is exercised.

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
Unavailable or uncertain hints can coexist with successful batch processing;
terminal line-diff observations never replace exact recipient evidence. The
absorbed-Enter negative control checks three spaced attempts per message, one
paste per ID, no claims and no persistent gate blocking the next sender. The
concurrent and approval-burst editors also absorb the first Enter and presses
less than 500ms apart. The concurrent senders start 300ms apart, reproducing
constant-rate interleaving; observed retry settlement must recover. Unit
negative controls also cover newline-driven reflow across many rows, viewport
prefix loss and blank redraws exceeding the old line-only threshold. Complete
notification texts, claims and effects remain required. The newline-retry case
checks actual submission on the second Enter and all four
full-read/correlated-reply steps, in plain and bracketed editors.

The default terminal actor makes a substantial redraw after a genuine submitted
buffer; `minimal-redraw` retains the one-row echo of a minimal line editor.
Both must complete receiver-owned full reads and exact replies. The latter may
report an uncertain hint and receive bounded extra Enter. No fixture renders a
submission or queues a helper merely because the screen changed. Terminal work
publishes queued/completed buffer counts after actual helper exit. The
controller waits for that completion barrier before final diagnostic refresh;
a full-read proof alone may precede a later genuine hint's empty POP. The
controller never supplies that POP or treats partial status as complete.

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

### 1.1. GitHub Actions dependency locks

Both workflows are onboarded to `.github/workflows/actions.lock`. Keep existing
full-SHA action pins and regenerate the lockfile after changing dependencies:

```sh
nix run '.#gh-actions-lock' -- --no-narrow
nix run '.#gh-actions-lock' -- --verify-local
nix run '.#gh-actions-lock' -- --verify
```

`--no-narrow` keeps literal SHA references matched to lockfile keys. Update SHA
pins first, then regenerate; never hand-edit the generated lockfile. The offline
coverage check runs in commit/Nix hooks. PR CI also checks upstream provenance
using its existing read-only token; local network verification uses existing gh
authentication. All commands use the same Nix-pinned v0.4.0 tool.

The existing Dependabot `github-actions` entry updates onboarded workflow pins
and matching lockfile entries in the same PR, without extra configuration.
Reusable workflow calls and container images still need their own SHA/digest
pins. This does not change action versions, release permissions or publication.
See [GitHub's lockfile contract][actions-lock-docs].

[actions-lock-docs]: https://docs.github.com/en/actions/concepts/security/workflow-dependency-lockfiles

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

`postman-team-manager` also has offline decision scenarios in
`scripts/validation/test-skill-team-manager.py`, enforced by the
`skill-team-manager` flake check. They exercise its pure advisory reference:
exact outstanding inquiries, uncertainty, long work, missing result reports,
blockers, direction checks and sufficient-evidence stop conditions. They do
not run a model, infer ownership, send mail or prove instruction compliance.
The skill documents the matching evidence procedure and actual status fields.

Existing `evals/<name>/eval.yaml` and `evals/<name>/tasks/` remain manual
behavioral suites. The automated gate selects only `evals/*/trigger/eval.yaml`
and never implicitly executes or rewrites the behavioral lane. Skill metadata
and publishing dry-runs remain separate required checks.

## 4. Invocation timing evidence

Use the [diagnostic schema and procedure](docs/invocation-diagnostics.md) to
compare equivalent builds, configuration and workloads. The optional real-CLI
benchmark owns its tmux socket, home, configuration and state; it needs no
model service. Recorder/lock/failure tests run in Go, reader tests run in the
`invocation-analysis` flake check, and process tests cover ordinary/empty POP,
redaction, concurrent files, read-only commands, crash during approval, and
log failure preserving a claim and the child exit code. Diagnostic failure
never relaxes required execution audits.

## 5. Releases

See [RELEASING.md](RELEASING.md) for the release checklist and tag ruleset
expectations.

Current contract documents are distinct from dated validation reports and
historical designs. The [agent-environment research snapshot][agent-research]
compares wider workflows and proposes experiments; it is not a shipped task
platform or an installation prerequisite. DeepWiki is supplementary, not the
source of runtime contracts or test evidence.

[agent-research]: docs/research/891-agent-environment-comparison.md

Canonical agent instructions live in [AGENTS.md](AGENTS.md).

The builtin standard-team case runs with no user configuration or installed
personal Skills. It reuses the same deterministic terminal driver:

```sh
nix run .#recursive-smoke -- --builtin --case root
```

`recursive-teams` checks it alongside the explicit-config recursive matrix.
The actors' private state/scope files are fixture bookkeeping; this proves
mail/editor mechanics and complete archived policy delivery, not autonomous
role compliance or substantive model review. Canonical workflow ownership and
migration boundaries are recorded in
[the standard-team inventory](docs/design/standard-team-migration.md).
