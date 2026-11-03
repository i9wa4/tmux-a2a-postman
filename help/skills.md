# Skills and references

```sh
tmux-a2a-postman skills
tmux-a2a-postman skills postman-usage
tmux-a2a-postman skills postman-usage mailbox
```

With no arguments, print JSON containing the sorted `skills` names and
`bundle_id`. With a Skill name, print its complete `SKILL.md`: a short entry
and a table of references. With a second argument, print that complete named
reference. Names omit file extensions and directory paths.

Every Skill uses the same `Read when | ref` table. Read matching rows in table
order; a row marked Always is required. Run the command shown in the entry for
each selected reference and read its complete output before acting. Detailed
procedures have one authored home under `skills/<name>/references/`.

`help`, `help skills` and `skills --help` explain CLI usage. Skill entries and
workflows are read through `skills`, independently of CLI help. All these reads
work offline without configuration, tmux, extraction or file changes. A missing
entry or reference is an explicit version incompatibility; do not download a
procedure from a different version.

Install an ordinary Skill directory with GitHub CLI:

```sh
gh skill install i9wa4/tmux-a2a-postman postman-usage --agent codex --scope user
```

Use `--agent claude-code` for Claude Code and choose names from `skills`.
Nix consumers can install the same repository directories declaratively.
Installation and discovery belong to the consumer and client. Postman provides
no installer command. Updating the binary updates detailed references without
repeatedly updating installed entry text.
