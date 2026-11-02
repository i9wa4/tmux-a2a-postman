# Stable Skill entry points

The installed binary contains all five thin entry points and its detailed
Markdown procedures. Install entries once; later binary upgrades update the
procedures reached by the same stable `help` topic. No repeated Skill update or
network installer is needed. If a binary lacks a topic, report incompatibility
instead of fetching instructions for an unrelated version.

List embedded names without changing files:

```sh
tmux-a2a-postman skills list
```

List output is JSON with `skills` (sorted names) and `bundle_id` (content hash).

Explicit first-time installation selects names, client and scope:

```sh
tmux-a2a-postman skills install --client claude --scope user postman-send-message
tmux-a2a-postman skills install --client codex --scope project --project /absolute/project postman-usage postman-recall
```

Claude uses `.claude/skills`, Codex uses `.agents/skills`, below the selected
home/project. Project scope requires its absolute directory. Existing custom,
incomplete or Nix-owned entries are conflicts; identical entries are unchanged.
Selected entries already present in Codex's legacy `.codex/skills` tree are
reported as conflicts before creating a second discovery tree; keep using the
existing declarative owner or explicitly export for review.
There is no force, update, uninstall or global instruction rewrite operation.

For an explicit offline export into an ordinary directory:

```sh
tmux-a2a-postman skills export --to /absolute/export postman-send-message postman-usage
```

Install/export output is JSON: `success`, `target`, and `actions`, with optional
failure `reason`. Each action has `name`, absolute `path`, and `outcome`:
`installed` or `already_available`. Failure exits nonzero. Existing content is
preserved; a partial I/O failure reports completed actions rather than implying
that they were rolled back. Invalid arguments fail before publishing entries.

Nix consumers use the checked repository `skills/` entries through declarative
installation; they do not run a post-activation network installer. Those entries
are deterministic derivatives of the embedded authored entries, checked for
drift. Details stay in binary HelpDocs. Client authentication, permissions and
normal Skill discovery remain the client's responsibility.
