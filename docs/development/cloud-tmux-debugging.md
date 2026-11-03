# AI-led cloud development and tmux investigation

Use this runbook when developing Postman in Codex Cloud or a similar Linux
environment. Existing regression fixtures are automated. The AI still chooses
a hypothesis, prepares the environment, selects a probe, inspects evidence and
decides what to change next. This procedure preserves those decisions; it does
not make the whole investigation one fixed command or a new CI system.

[AGENTS](../../AGENTS.md) owns development requirements.
[Contributing](contributing.md) owns the full test matrix, flags and
limitations. Use their current versions rather than copying an old session's
tool paths, container names or checkout state.

## 1. Establish the question and the candidate

1. Read the issue, comments, related changes and current contract. Record the
   reported symptom and one falsifiable hypothesis. For example: a session
   created in HOME might incorrectly select the HOME mailbox even though its
   agent panes now run in different repositories.
2. Work in an issue-owned checkout. Inspect its branch, status and upstream;
   preserve existing changes. Record the source commit and the binary actually
   being exercised. A rebuilt source tree does not replace an older installed
   binary or a downstream Nix pin automatically.
3. Define the observable failure and success before probing. Separate mailbox
   publication, notification submission, recipient POP, full archive reading,
   exact response and substantive task acceptance. Screen movement alone proves
   none of the later steps.
4. Keep a short local evidence record: hypothesis, exact command/environment,
   observed facts, uncertainty and next probe. Keep temporary transcripts and
   machine-specific paths out of public issue/PR text.

## 2. Prepare tools without silently skipping native tests

Check the actual Go, tmux, Python and shell tools. Some native Go tests call
`t.Skip` when a dependency is absent. A green `go test` is insufficient if the
intended native case did not run.

The flake's default and `ci` development shells provide the selected Go and
Python tooling, but do **not** themselves include tmux. Enter a shell with the
native tools from this checkout's pinned nixpkgs:

```sh
nix shell --inputs-from . \
  nixpkgs#tmux nixpkgs#bash nixpkgs#zsh nixpkgs#direnv \
  --command nix develop .#ci

go version
tmux -V
python3 --version
```

Use verbose targeted tests and inspect RUN/PASS/SKIP. For the workspace example:

```sh
go test ./internal/workspace \
  -run '^TestHomeCreatedSessionsUseAgentPaneDirectoriesWithoutBootPreparation$' \
  -count=1 -v
```

This pinned-shell command and the native test were exercised in the reference
cloud environment on 2026-11-02. The full Nix package/check paths separately
provide their native test dependencies. Do not infer those dependencies from
the host's preinstalled tools.

`nix develop` installs actual repository Git hooks; `nix develop .#ci` does not.
Install hooks for the owned worktree before committing. Linked worktrees share
Git hooks/configuration, so serialize hook setup and use each worktree's own
generated configuration. Run hooks in the environment containing their Nix
interpreters; a container's `/nix/store` paths may not exist on the host.

### 2.1. When the cloud host cannot run Nix builds

Diagnose a namespace, certificate or download failure before changing product
code. Follow the current environment's network/Docker instructions and inspect
its supported policy/readiness information. Preserve its proxy configuration
and credentials; do not print the environment or credential files.

The reference session used an owned `nixos/nix:2.34.3` container with Docker
`--init` and `--privileged`. Adapt that backend to the environment rather than
assuming every cloud supports it:

- Use the managed local Docker daemon. Check endpoint/context selectors so a
  saved Docker context cannot redirect commands elsewhere. Preserve registry
  configuration and the platform's reachable proxy defaults.
- Mount the common repository **and** linked worktrees at consistent absolute
  paths. A worktree-only mount can leave its `.git` pointer inaccessible.
  Set an explicit workdir for every build, test and Git command.
- Supply the environment's CA trust to the inner process. Containers do not
  inherit the host's certificate store; Nix uses `NIX_SSL_CERT_FILE`. Use a
  read-only combined CA bundle where appropriate and keep TLS verification.
- Use an init/subreaper. tmux and interrupted CLI fixtures can leave orphaned
  descendants in a container whose PID 1 does not reap them.
- Keep ownership compatible with the task user. Root-run formatters or hook
  installation can make source files or shared `.git/config` unreadable to
  later host Git/Go commands. Restore only the owned paths to the actual task
  UID/GID; do not use world-writable permissions or bypass hooks.

After preparing an owned builder, invoke the same repository commands there:

```sh
docker exec --workdir "$postman_worktree" "$postman_builder" \
  nix flake check --print-build-logs
docker exec --workdir "$postman_worktree" "$postman_builder" \
  nix build --print-build-logs
```

Here the two variables identify the worktree and builder established by the
current investigation. They are not global configuration or a required naming
convention. Remove only this investigation's own containers and fixtures.

## 3. Choose a small probe, then inspect the actual evidence

Prefer an existing deterministic scenario that tests the hypothesis:

```sh
nix run .#notification-smoke -- --case rally \
  --output /tmp/postman-rally.json
nix run .#notification-smoke -- --case absorbed-enter
nix run .#notification-smoke -- --case concurrent
nix run .#notification-smoke -- --case approval-burst
```

Run the relevant case, not every example above on every iteration. Parent/child,
rejection and external-channel scenarios are listed in Contributing and the
driver's `--help`. The app builds the candidate and provisions real tmux plus
the deterministic terminal agents; it needs no model account.

If the existing scenario cannot express the hypothesis, make an owned
experimental fixture using the existing helpers/driver as a starting point.
Adjust one condition at a time: native pane cwd versus session creation path,
tool subprocess cwd, recipient readiness, copy mode, concurrent senders or input
editor behavior. Preserve the input/output evidence and own the cleanup. An
interactive investigation may use a temporary pause or extra capture in that
fixture; production sessions are not its test subjects.

| Observation                                    | Next investigation                                                                                                                                         |
| ---------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Failure before a Go test or agent starts       | Inspect Nix/network/CA/tool availability and process reaping first.                                                                                        |
| Wrong workspace or unavailable identity        | Compare the owned native pane/session evidence, configuration and binary revision; changing the CLI subprocess cwd is not a workspace repair.              |
| Mail committed, hint uncertain                 | Inspect that message's notification attempt record and before/after whole-pane captures; do not resend a committed body.                                   |
| Text present in the input editor               | Check submission/consumption evidence. An Enter can be absorbed as newline; moving text is not proof of acceptance.                                        |
| Notification accepted, no exact final response | Follow recipient-owned claim/read/proof files and exact IDs to the first missing step. Do not call POP or supply its response from the controller.         |
| Concurrent requests or approvals fail          | Correlate every independent ID and decision, lock/admission evidence and actual execution marker. Never use one successful request as proof for the batch. |
| Mail protocol passes, result is wrong          | Investigate model behavior, delegation or acceptance criteria separately; a deterministic fake does not establish reasoning quality.                       |

The driver prints a per-case report path. Read the JSON and linked fixture
evidence, including captured screens, archive digests, claims, notification
records and spaced Enter timing. Historical input guards are ignored by current
transport and retained untouched. Inspect the failing step before
changing timeouts. Completed scenarios clean up their tmux server while
retaining reports; use saved captures afterward. During an owned live probe,
use its explicit socket and pane IDs, never the user's default tmux server.

Unknown delivery/input/execution outcomes remain unknown. Do not repair history,
delete old mail, fabricate an acknowledgment or replay an uncertain side effect
to make the experiment appear successful.

## 4. Optional real-client evidence

The deterministic terminal agents exercise actual tmux input, Enter handling and
recipient CLI work. The optional `--agent codex` path instead runs a real Codex
TUI with a local Responses server making fixed decisions. See Contributing for
its command and supported cases. This checks the real editor/tool path without
an account; it does not test the reasoning of a real model.

Record the actual client version when using that optional path. Do not infer
Claude Code behavior from a Codex result, or change generic transport to depend
on one client's screen layout. Authenticated `--mode live` is a separate,
explicitly selected experiment; it is not needed to validate the notification
protocol and is never an automatic fallback.

## 5. Turn a finding into a bounded change

Keep the smallest probe that demonstrates the defect. Where it represents a
stable product invariant, turn it into a focused regression that fails on the
old behavior and passes on the fix. Exploratory captures, environment recovery
and AI judgment can remain in this runbook/evidence record rather than becoming
another test framework. Keep recipient-owned POP, full reading and exact replies
mandatory when adjusting fixtures.

Stage new files so Nix sees them, then run the required checks:

```sh
nix flake check --print-build-logs
nix build --print-build-logs
```

Use Contributing for dependency-specific checks, formatting, lint, skills and
publication dry-runs. Verify the latest pushed head's Linux/macOS CI; an older
green run does not validate a follow-up commit. For downstream Nix consumers,
also check the actual pinned source and integration suite. A source PR, merged
upstream fix and installed binary are distinct states.

Stop the investigation when the hypothesis is resolved and the required checks
pass. Report the source/binary revisions, exercised fixture/client modes,
observable result, meaningful limitations and any remaining adoption step.
