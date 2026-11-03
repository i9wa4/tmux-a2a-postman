# Local Markdown Artifacts — creation

## 1. Portable Creation

No Skill checkout, Nix, mkmd command on PATH or provider account is required.
Ordinary local Markdown is sufficient. To create a new record with the optional
bundled Bash helper from any working directory:

```sh
postman_resources=$(tmux-a2a-postman resources)
bash "$postman_resources/skills/postman-records/scripts/mkmd" --dir plans --label implement-feature
```

The helper requires Bash, Git (optional outside repositories), date, basename,
mkdir, mktemp, mv and rm. It prints one absolute new Markdown path; write the
record there under existing authority. It does not select or replace a canonical
index, evaluate task state or send messages. Filename sanitization is a
discovery hint; record full unsanitized branch, worktree and repository
provenance.

## 2. Location and Existing Records

New records default to
`${XDG_STATE_HOME:-$HOME/.local/state}/tmux-a2a-postman/%logbook`.
`POSTMAN_LOGBOOK_ROOT` overrides it; legacy `MKMD_BASE_DIR` is also accepted.
Roots must be absolute. The percent namespace cannot collide with a canonical
workspace directory. Existing known records, including legacy mkmd paths, stay
canonical; an upgrade or a new default never migrates them silently. Discover
trusted legacy roots when needed, not just the new root. Relative-path or
missing known-state errors require repair, not a replacement tracker.

The helper creates
`<root>/<owner>-<repo>/YYYY-MM-DD-<branch>/<dir>/<label>-<random>.md`. Use
single-component directory/label names; no separators or `..`. Content, full
identity and provenance decide ownership, not this display path.

## 3. Directory Labels

| Label           | Use when                                                     |
| --------------- | ------------------------------------------------------------ |
| `plans`         | The artifact is an execution plan or milestone tracker.      |
| `orchestration` | The artifact is canonical branch-scoped orchestration state. |
| `research`      | The work is investigation, synthesis, or decision notes.     |
| `reviews`       | The artifact records review findings or approvals.           |
| `draft`         | The artifact is temporary text for a handoff or message.     |
| `tmp`           | The artifact is scratch output with low retention value.     |

Prefer the narrowest label that fits. Use `tmp` only when the output can be
discarded without losing task state.
