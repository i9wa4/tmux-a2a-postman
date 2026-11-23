---
name: git-github-operations
license: MIT
description: |
  USE FOR: Git and GitHub operations involving branches, worktrees,
  commits, issues, pull requests, or sharing artifacts through Gists
  and issue comments.
  DO NOT USE FOR: Implementation strategy or substantive artifact review.
---

# Git and GitHub operations

Read the repository's own instructions first. Identify the issue or PR, exact
repository and relevant discussion, and the intended base before editing or
reviewing. Preserve the caller's checkout and native pane binding. Work in an
isolated, verified checkout whose directory hierarchy is suitable for the
configured Postman parent and child route. Git ancestry and directory
containment are separate facts.

Read [worktrees](references/worktrees.md) when selecting or refreshing a branch
or review checkout. Read [publication](references/publication.md) before a
commit, push, PR or issue comment. Read [sharing](references/sharing.md) when
publishing a file through a Gist or Issue.

Check status, branch, revision, upstream, target path and applicable repository
checks before changing or publishing. Never force reset or overwrite unrelated
or dirty work. Pass the exact reviewed head and base, worktree, requirements and
requested output to a substantive review. Use the review result within its
actual scope; a review does not publish or merge by itself.
