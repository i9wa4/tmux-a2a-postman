# Skills — embedded source and thin distribution

Seven canonical packages live under `skills/<name>/`: `stay-lazy`,
`development-workflow`, `git-github-operations`, `postman-send-message`,
`subagent-review`, `postman-records`, and `postman-friction-triage`. Each
package contains a complete
`SKILL.md` and any needed references or helper scripts. Read a full entry or
one Markdown reference from the matching binary:

```sh
tmux-a2a-postman skill postman-send-message
tmux-a2a-postman skill postman-records records
```

The binary embeds these packages. Reading them needs no tmux, configuration,
installed Skill tree, network, or extraction. Optional helper scripts use the
explicit `resources` bundle; see `help resources`.

`dist/skills/<name>/SKILL.md` contains the source frontmatter and the fixed
one-line instruction to read the binary's matching entry. These Git-tracked
files are the installable discovery entries for client Skill mechanisms. A
consumer may install them with its supported client or declarative package
method, alongside the **same revision** of the Postman binary. An installed
entry has no independent procedure body.

Contributors edit `skills/<name>/SKILL.md` and its needed resources, then run
`nix run .#generate-skills` and stage changed `dist/skills` files. The generator
copies frontmatter without rewriting it and checks the exact generated set with
`--check`. See
[CONTRIBUTING](../docs/development/contributing.md#3-agent-skills).

CLI contracts, configuration and communication specifications use
`tmux-a2a-postman help <topic>`. The complete built-in configuration is
`tmux-a2a-postman help config default`; a custom `postman.md` replaces that
configuration as one complete source.
