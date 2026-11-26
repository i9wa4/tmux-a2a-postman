# skill — read a complete embedded Skill

```sh
tmux-a2a-postman skill <name> [reference]
```

The six canonical Skill packages and their Markdown references are embedded in
the same binary as the default team. This read does not inspect user
configuration, discover a tmux pane, access personal Skill roots or materialize
helpers. Use a name from the displayed catalog; a reference is the basename of
one Markdown file under that Skill's `references/` directory. Unknown names or
references fail without a fallback to another checkout.

The tracked `dist/skills/<name>/SKILL.md` entries provide installable discovery
metadata and a one-line instruction to read the binary's complete Skill. The
binary and distribution must come from the same revision. Use `resources` only
for an explicitly needed bundled helper file.
