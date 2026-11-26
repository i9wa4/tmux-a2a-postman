---
name: postman-send-message
license: MIT
description: |
  USE FOR: Sending or replying to Postman messages, including first
  contact, task delegation and forwarding a supplied body unchanged.
  DO NOT USE FOR: Mail intake, passive waiting or recalling past work.
---

# Send a Postman message

An existing console can make first contact without an installed Skill tree or a
prior Postman message. Check `tmux-a2a-postman version`, then read this embedded
Skill with `tmux-a2a-postman skill postman-send-message`;
`tmux-a2a-postman help send-heredoc` gives the exact CLI contract. No launcher,
daemon or initial prompt is required.

For a new request or an exact reply, read [sending](references/sending.md). To
interpret a committed delivery or uncertain notification, read
[outcomes](references/outcomes.md). For outstanding responses and passive
waiting, read [waiting](references/waiting.md). Read each selected reference
completely before using it. If this binary lacks a required reference, report
the mismatch rather than substituting another checkout or personal Skill tree.
