# Path Display Policy

tmux-a2a-postman has two path audiences:

- operators reading CLI output
- scripts and agents opening files

User-facing CLI output should prefer `~`-shortened paths for files under the
current user's home directory. This keeps normal output compact and avoids
showing the full local checkout or state-directory prefix when a home-relative
path is enough for humans.

Machine-readable absolute paths may still appear when the command contract
needs a path that scripts and agents can open without shell-specific expansion.
When a command exposes both forms, use separate fields:

- display path field: `markdown_path`, `path`, or another command-specific
  field documented as user-facing
- machine path field: an explicit absolute-path field such as
  `markdown_absolute_path`

For `pop`, `markdown_path` is the display-friendly archived message and body
path. When that value is shortened with `~`, `markdown_absolute_path` carries
the absolute file path for programmatic reads. `pop` never embeds
sender-authored body text inline; agents read `markdown_absolute_path` when it
is present, otherwise `markdown_path`.

Public and permanent GitHub surfaces still follow the stricter path hygiene
rule: use repo-relative paths or stable web URLs for project files, and do not
include concrete machine-local absolute paths there.
