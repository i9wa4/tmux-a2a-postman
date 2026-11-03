# Stable Skill entry points

The binary provides thin entry points for transport, planning, coordination,
records and review and their complete Markdown HelpDocs. Each stable entry
directs the agent to the installed executable's help, so upgrading the binary
updates detailed procedures without repeatedly updating Skill text.

List entry names and their content identity without changing files:

```sh
tmux-a2a-postman skills list
```

List output is JSON with sorted `skills` names and `bundle_id`. Display one
complete SKILL.md, including its metadata and HelpDoc reference:

```sh
tmux-a2a-postman skills show postman-usage
```

Both commands are read-only and work offline, outside tmux and configuration.
Install an entry with GitHub CLI:

```sh
gh skill install i9wa4/tmux-a2a-postman postman-usage --agent codex --scope user
```

Use `--agent claude-code` for Claude Code and replace `postman-usage` with the
desired name from `skills list`. GitHub CLI handles the client's installation;
Postman supplies the entry and its detailed installed HelpDocs. Existing Nix
users can continue installing the checked repository `skills/` directories
through their declarative Skill source.

The authored entries are under `internal/helpdocs/entries/`; generated
repository copies are checked for drift. Detailed operating procedures have
one owner in the binary's HelpDocs. A missing installed topic is an explicit
incompatibility, not permission to fetch instructions for another version.
Client authentication, permissions and discovery remain with the client.
