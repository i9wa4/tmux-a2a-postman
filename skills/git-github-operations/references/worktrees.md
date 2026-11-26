# Worktrees

Choose an issue before implementation where the repository workflow requires
one. Read its title, body and relevant discussion. For a new Issue branch,
compose a short English slug describing the intended change:
`issue-<number>-<slug>`, lowercase kebab case, at most five words and 50
characters, using letters, digits and hyphens and preferably starting with a
verb. Follow an explicit user name or repository convention. Reuse an already
verified matching branch without renaming it. A bare number is an exceptional
fallback when a meaningful valid slug cannot be formed; state that limit.

Check the repository root and the intended parent worktree path. Default to
`<repo-root>/.worktrees/<issue-or-pr-label>` where that preserves the Postman
directory hierarchy. Verify path, branch, upstream, cleanliness and exact
revision before reuse. Fetch the intended source, choose the Git base
deliberately, and create or reuse a linked worktree with native `git` and `gh`.
Keep the caller's checkout and native pane intact. Follow the repository's setup
and trust instructions; no worktree operation automatically starts tmux sessions
or resident agents.

For PR review, resolve the PR's repository and number, actual head repository,
owner, branch and exact head/base revisions before checkout. Use the existing PR
head; do not rename it by the Issue naming rule. Default new path:
`<repo-root>/.worktrees/pr-<number>-<safe-head-branch-label>`. Fetch from the
actual head repository, including forks, and verify the source remote URL and
upstream. A matching branch name or remote label alone proves no ownership.
Avoid repointing an unrelated remote or branch when labels collide.

Reuse an exact clean review checkout. Fast forward a verified matching clean
branch only when it is behind the intended head. Do not reset an ahead or
divergent branch, overwrite dirty work, or silently review another revision.
Keep separate PR identities and results for separate requests. Recheck fetched
head/base before review and give the reviewer those exact revisions and checkout
path.
