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
Installation and updates belong to Nix or the consumer's existing Skill
manager. Repository consumers use the checked `skills/` directories; binary
consumers can supply the complete `skills show` output to their chosen setup.
Postman does not select a client directory or manage installed entries.

The authored entries are under `internal/helpdocs/entries/`; generated
repository copies are checked for drift. Detailed operating procedures have
one owner in the binary's HelpDocs. A missing installed topic is an explicit
incompatibility, not permission to fetch instructions for another version.
Client authentication, permissions and discovery remain with the client.
