# Contributing

Follow the [design principles](../design/project-design-philosophy.md) when
changing code, configuration, instructions or packaged assets.

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
[cloud development runbook](cloud-tmux-debugging.md).

README is the product entry point;
[quickstart](../../help/postman-usage/references/quickstart.md),
[configuration reference](../../help/postman-md.md)
and [CLI specification](../../help/cli-spec.md) own current user contracts. Keep
developer check commands, fixture matrices and evidence limits here or in the
linked development/validation documents. Command HelpDocs live in `help/*.md`.
Each workflow is a normal Skill package at `help/<name>/`: its `SKILL.md` owns
metadata and workflow entry instructions, `references/` owns full procedures,
`scripts/` owns optional helpers and `evals/` owns evaluation fixtures. The
binary embeds those authored manuals and helpers directly.
`skills/<name>/SKILL.md` is only the generated installable discovery entry; no
references, helpers or evals are copied there. `config/` owns bundled
configuration assets. `docs/` holds design, research, development, validation
and license records. Test-only configurations live in `testdata/config/`.
License notices ship with the explicit offline resource bundle. `CLAUDE.md`
points to the one repository instruction owner, AGENTS; runtime `postman.md` is
a separate user configuration surface.

Validate maintained example configurations and their first exchange with
`go test ./internal/config ./internal/cli ./internal/app -run 'TestDocumented'`.
These checks use the real parser, installed catalog metadata and isolated native
mail flow. Mermaid presentation rendering is a separate check; explanatory
README arrows are not valid routing configuration. See the dated
[documentation validation note](../validation/982-documentation.md).

Historical tracked task notes are indexed in the
[retained evidence map](../validation/740-retired-task-artifacts.md).
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
[recursive trial guide](../validation/recursive-sessions-trial-runbook.md) for
an explicit optional consumer-policy check. CLI commands and their flags do not
change.

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
constant-rate interleaving; increasing retry cooldown must recover. Complete
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

## 2. Markdown checks

Match dotfiles' Markdown tools and their responsibilities:

- `rumdl` checks Markdown structure and formats with treefmt. Both the
  linter and formatter disable MD041 and use the same MD013 settings:
  reflow prose, leave code blocks and headings unwrapped.
- `markdown-formatter` applies its default heading numbering to all Markdown
  in commit and Nix checks. Its pinned revision matches dotfiles.
- Vale and Harper provide English prose feedback in `nix develop`. As in
  dotfiles, these remain advisory until vocabulary and a clean baseline are
  established. Review findings without rewriting commands, identifiers,
  paths or Japanese prose to satisfy English advice.

Run `nix fmt` for treefmt-managed formatting. The commit hook applies
`mdfmt --write`. Both required checks cover every tracked Markdown file,
including configuration, Skill entries/references, design and test fixtures;
there are no root-doc or Skill exclusions.

Run the supplementary tools against the same complete tracked Markdown set:

```sh
nix develop
git ls-files -z '*.md' | xargs -0 vale --config config/vale.ini
git ls-files -z '*.md' | xargs -0 harper-cli lint --format compact
```

Use `harper-ls --stdio` for optional editor feedback. `config/vale.ini` uses
Vale's built-in style, matching dotfiles' global configuration without relying
on a developer's HOME. Ignored generated files such as
`.pre-commit-config.yaml` are not policy surfaces.

## 3. Agent Skills

### 3.1. Edit, generate and read

Keep user-facing specifications in `help/` even when they are long or detailed.
For example, `help/postman-md.md` owns configuration fields and constraints
(`help postman-md`), while `help/postman-config-auditor/` owns the audit
workflow and refers to that specification. `config/postman.md` is the actual
builtin configuration data. Do not copy the format specification into an
installed Skill or confuse it with the default configuration. Contributor design
and research documents remain under `docs/`.

Edit the canonical `help/<name>/SKILL.md`, never its generated counterpart. Use
the same `<name>` for the directory, frontmatter `name`, installed Skill and
help topic. Keep the entry's `Read when | ref` table consistent with the
complete Markdown files under `references/`; read matching rows in order. A row
marked Always is mandatory. CLI usage remains `help <command>` or
`<command> --help`. Workflow and reference reads are:

```sh
tmux-a2a-postman help postman-usage
tmux-a2a-postman help postman-usage mailbox
```

The shared YAML header also owns the one-line discovery instruction:

```yaml
metadata:
  postman-skill-body: >-
    Read and follow `tmux-a2a-postman help postman-usage`.
```

Generate after changing a source header or adding a workflow, then stage both
source and generated entries before Nix checks:

```sh
nix run .#generate-skills
git add help skills
nix run .#generate-skills -- --check
nix run .#skill-check
```

Generation copies the original YAML header without reserialization and uses
only `metadata.postman-skill-body` as the distribution body. That field must be
one nonempty line; the generator does not invent command names or prose. The
tracked output makes ordinary `gh skill install` and declarative consumers work
without a generator at installation time. Remove the generated directory when
retiring a workflow; unexpected distribution files fail validation so stale
references or helpers cannot ship accidentally.

The commit hook regenerates changed entries automatically. Read-only `--check`,
`skill-check` and the Nix checks reject missing or edited generated entries.
`skill-check` checks metadata in both trees, Waza readiness and the GitHub skill
publish dry-run. Waza checks specification compliance, links and the existing
500-token entry budget; references have no entry-token budget. The `skill-waza`
hook checks changed packages in both trees. Offline Nix builders skip network
link checks only.

All Markdown, including canonical manuals, generated entries, references,
configuration and fixtures, is covered by rumdl and markdown-formatter. Run
`nix fmt` after generation. Vale and Harper remain advisory over the same set.
See the tool commands above; do not exclude generated Markdown from formatting.

### 3.2. Distribution

A normal binary build directly embeds the canonical HelpDocs. The same release
contains the committed thin `skills/` tree. Updating the binary updates
operating procedures, while installed entries retain only their discovery
metadata and one help instruction. `resources` explicitly materializes optional
helpers at `help/<name>/scripts/` in the content-identified bundle. No live
client directory or user configuration is modified by a build or validation.

Do not use `gh skill publish --tag` in the tag-push release workflow. That
command creates a tag and GitHub Release itself. The repository release flow
uses the pushed `v*` tag plus GoReleaser, while `gh skill install` resolves
skills from the published repository tag or release.

### 3.3. Deterministic trigger expectations

Every `help/<name>/SKILL.md` needs a corresponding
`help/<name>/evals/trigger/eval.yaml` and positive/negative YAML tasks under
that suite's `tasks/` directory. CI enforces this through `nix flake check`; the
local hook responds to both skill and trigger-fixture changes.

```sh
nix run .#skill-trigger-check
# In nix develop, inspect metadata without starting an evaluator:
python3 scripts/validation/validate-skill-triggers.py --preflight-only
python3 scripts/validation/test-skill-triggers.py
```

The repository provisions Waza CLI v0.39.1 with fixed release hashes for Linux
x86_64/aarch64 and macOS aarch64. All suites explicitly select its `mock`
executor and `offline-mock` model label, one serial trial and bounded timeouts.
Waza's update check is disabled. No model SDK, credentials or model API is
needed. Both manual and trigger suites explicitly target their canonical HelpDoc
Skill using `config.skill_directories`, relative to the suite. Trigger graders
point to `help/<name>/SKILL.md`. The selected release's trigger grader scores
local skill keywords and USE FOR phrases, including the skill body. It does not
simulate real-model routing or prove task success/comprehension. Overlapping
vocabulary can produce heuristic false positives; use concrete responsibility
boundaries and inspect feedback rather than flipping an authored expectation to
pass.

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

Existing `help/<name>/evals/eval.yaml` and `help/<name>/evals/tasks/` remain
manual behavioral suites. The automated gate selects only
`help/*/evals/trigger/eval.yaml` and never implicitly executes or rewrites the
behavioral lane. Skill metadata and publishing dry-runs remain separate required
checks.

## 4. Invocation timing evidence

Use the [diagnostic schema and procedure](../../help/invocation-diagnostics.md)
to compare equivalent builds, configuration and workloads. The optional real-CLI
benchmark owns its tmux socket, home, configuration and state; it needs no model
service. Recorder/lock/failure tests run in Go, reader tests run in the
`invocation-analysis` flake check, and process tests cover ordinary/empty POP,
redaction, concurrent files, read-only commands, crash during approval, and log
failure preserving a claim and the child exit code. Diagnostic failure never
relaxes required execution audits.

## 5. Releases

See [docs/development/releasing.md](releasing.md) for the release checklist and
tag ruleset expectations.

Current contract documents are distinct from dated validation reports and
historical designs. The [agent-environment research snapshot][agent-research]
compares wider workflows and proposes experiments; it is not a shipped task
platform or an installation prerequisite. DeepWiki is supplementary, not the
source of runtime contracts or test evidence.

[agent-research]: ../research/891-agent-environment-comparison.md

Canonical agent instructions live in [AGENTS.md](../../AGENTS.md).
