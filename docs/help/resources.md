# Offline helper resources

`tmux-a2a-postman resources` explicitly materializes and validates the current
binary's complete immutable helper set, then prints its absolute directory.
It needs no tmux, configuration, installed Skill tree or network access.

The cache is under the absolute user cache directory, in a Postman-owned
content-identified generation. Different builds coexist; no version label can
replace another generation. Existing corrupt, incomplete or symlinked sets are
errors and are preserved for diagnosis. No user file is overwritten or
downloaded. Help, status and ordinary startup do not perform extraction.

Use the returned directory independently of the task cwd. Python 3 is needed
only to use the optional Python reference helpers; Postman does not install it.

```sh
postman_resources=$(tmux-a2a-postman resources)
# Reference modules, not agents, schedulers or automatic task executors:
# "$postman_resources/recall/select_day.py"
# "$postman_resources/team-manager/next_step.py"
# Read-only diagnostic analyzer (requires Python 3):
python3 "$postman_resources/diagnostics/analyze-invocations.py" --help
# Complete optional preset example, available without a checkout:
# "$postman_resources/examples/launch/postman.md"
```

The bundled license notices are under `licenses/` in that directory.
Mutable work records and runtime mail remain separate from resources.
