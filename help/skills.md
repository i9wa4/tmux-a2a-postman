# Skills — thin workflow discovery

Install a thin Skill with GitHub CLI:

```sh
gh skill install i9wa4/tmux-a2a-postman postman-usage --agent codex --scope user
```

Use `--agent claude-code` for Claude Code. Nix consumers can install the same
`skills/<name>/` directories declaratively. Installation belongs to the
consumer.

Each installed Skill contains shared YAML metadata and one instruction to read
`tmux-a2a-postman help <name>`. Detailed procedures, references, helpers and
evaluations stay in the canonical `help/<name>/` package. Updating the binary
updates the detailed procedures without updating installed entry text.

```sh
tmux-a2a-postman help postman-usage
tmux-a2a-postman help postman-usage mailbox
```

The first command returns the complete `help/postman-usage/SKILL.md`, including
its uniform `Read when | ref` table. Read matching references completely in
table order; a row marked Always is required. The second command returns that
complete reference. Names omit directory paths and file extensions. `help` lists
the installed topics, including workflow names.

All reads work offline without configuration, tmux, extraction or file changes.
A missing topic or reference is an explicit version incompatibility; do not
download procedures from a different version. Optional helpers use the explicit
`resources` bundle at their canonical `help/<name>/scripts/` paths.

Contributors edit `help/<name>/SKILL.md`. Its `metadata.postman-skill-body`
supplies the one-line installable body. `nix run .#generate-skills` copies the
YAML header exactly and uses that value as the body of `skills/<name>/SKILL.md`.
Generated entries are tracked for normal GitHub CLI installation. See the
[maintenance guide](../docs/development/contributing.md#3-agent-skills) for
generation, validation and release procedures.

To inspect the complete builtin configuration directly, run
`tmux-a2a-postman help config default`. This reads the canonical
`config/postman.md`, independently of user configuration and without extraction.
