# Six-Skill migration evidence for #988 and #1089

This map describes the source move at the current branch. The comparison base is
`b1a978d`. Repository paths are relative to this repository; `dotfiles/` names
refer to the paired consumer project under
[dotfiles#492](https://github.com/i9wa4/dotfiles/issues/492). This source move
does not activate a client or complete the separate live-agent studies.

## 1. Ownership and transitive resources

| Earlier source                                             | Dependencies and useful content                                                                          | Owner and disposition                                                                                                                                           | Evidence                                                                |
| ---------------------------------------------------------- | -------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------- |
| `dotfiles/skills/stay-lazy`                                | Short principles and approved description                                                                | `skills/stay-lazy`; retained without a reference tree                                                                                                           | Source and thin distribution entry                                      |
| `dotfiles/skills/dev-platform-workflow`                    | Git, GitHub, worktrees, Issue/PR handling, development principles                                        | `skills/git-github-operations` and `skills/development-workflow`; Jira/Confluence and private tokens remain downstream                                          | Git and development Skill bodies and references                         |
| `dotfiles/skills/logbook`                                  | Canonical records, recall and `mkmd`; Gist verifier and Issue mailbox adapter with their contract suites | `skills/postman-records` owns records; `skills/git-github-operations` owns sharing; private destination config stays outside the package                        | Both Skills, `scripts/`, `resources` manifest and local contract suites |
| `help/postman-send-message`                                | First contact, exact replies, delivery outcomes and passive waiting                                      | `skills/postman-send-message`; references moved, first-send CLI details remain in help                                                                          | Skill and `help/send-heredoc.md`                                        |
| `help/postman-review`                                      | Perspective definitions, review flow and finding annotations                                             | `skills/subagent-review`; six complete perspectives plus one shared report reference. Review authority and role routing stay in `config/postman.md`             | Skill, seven references and default policy                              |
| `help/postman-records` and `help/postman-recall`           | Local record identity, creation, selection, writing, retention, own mail recall, `mkmd`, `select_day.py` | `skills/postman-records`; merged into records and recall references with two scripts; duplicate entries removed                                                 | Skill, references, resource manifest                                    |
| `help/postman-plan`                                        | General planning and investigation                                                                       | `skills/development-workflow`; role-specific task preservation stays in `config/postman.md`                                                                     | Development Skill and policy                                            |
| `help/postman-orchestrate` and `help/postman-team-manager` | Hierarchy, delegation, completion, recovery and management                                               | Team authority and role duties in `config/postman.md`; durable decisions in `skills/postman-records`; generic completion proof in `skills/development-workflow` | Policy, records and development Skills                                  |
| `help/postman-config-auditor`                              | Config topology and binding audit                                                                        | `help/config.md`, `help/postman-md.md`, mechanical validation and default policy; separate Skill removed                                                        | CLI help and config tests                                               |
| `help/postman-usage`                                       | Mailbox, status, session flow, external channel and diagnostics                                          | CLI help, `help/quickstart.md` and `help/invocation-diagnostics`; separate Skill removed                                                                        | Help topics and process tests                                           |
| `dotfiles/skills/subagent-review`                          | Generic coordinator/reviewer pattern                                                                     | `skills/subagent-review`; approved case and perspective contract supersedes duplicate downstream text                                                           | Skill and seven references                                              |

The seven retained review references are `report.md` and
`reviewer-{security,architecture,historian,code,qa,data}.md`. The generic
review lookup, ledger, packet, routing and scenario manuals have no independent
owner; applicable constraints reside in the Skill, default policy and CLI help.
The Gist and Issue delivery scripts are materialized by `tmux-a2a-postman
resources` with no Postman record prerequisite. The Issue creation destination
continues to be read from a machine-local config file; it is absent from the
binary and distribution.

## 2. Reading burden

These counts use whitespace-separated words and bytes for explicitly selected
files at `b1a978d` and the current branch. Both first-send sets include
`help/send-heredoc.md`, so relabeling a help read as a Skill read cannot create
a false reduction. The record-resume path includes old recall and record
selection procedures and the new merged record/recall references.

| Path                                                             | Before                             | After                             | Limit of comparison                                     |
| ---------------------------------------------------------------- | ---------------------------------: | --------------------------------: | ------------------------------------------------------- |
| First send: entry, sending, outcomes and CLI help                | 4 files, 1,208 words, 8,618 bytes  | 4 files, 1,166 words, 8,132 bytes | Selected reading path only; no model-use claim          |
| Record resume: record and recall entries and selected references | 5 files, 2,800 words, 21,763 bytes | 3 files, 1,148 words, 8,266 bytes | Selected reading path only; current context may suffice |

The measurement counts authored text, not model tokens, elapsed time or
comprehension. It does not establish live trigger selection or review quality.

## 3. Verification boundary

Six source Skills and six generated thin entries are checked by the same
generator and metadata/trigger/Waza gates. A process test reads complete
embedded Skills and selected references from an unrelated cwd with broken custom
config. The sharing helper suites use mocked transport and do not post remotely.
Runtime mail protocol code is unchanged in this migration; default and custom
policy tests and isolated tmux suites remain the compatibility gate. Postman
repo-local builds coexist with the dotfiles-managed older binary in parent
sessions. The installed `git-b1a978d` binary parsed the new `config/postman.md`
with read-only `launch-agents --list --config`, returning `dev`, `preset-p` and
`preset-w`; this is a config parse check, not a live cross-version communication
test. A failure of cross-session notification is diagnosed from its actual
result; internal session work and exact mail records remain available. Rollback
is to keep or restore the prior pinned dotfiles binary and config; no
installation or activation is part of this branch.
