# GitHub publication

Before commit or push, inspect the scoped diff, status, base, branch and
upstream. Run the repository's required checks on the version being published.
Preserve unrelated changes. Commit only the intended files with a message
describing the change; do not bypass hooks. Push to the verified source
repository and same branch, using an explicit refspec when needed. Confirm the
remote head after push. A successful local commit or earlier CI run does not
prove the pushed head.

For a PR, verify repository, issue link, head and base, draft state and exact
diff. Describe the user-visible change, why it was made, tests and remaining
limits. Put multiline content in a body file when using `gh`; verify the
published PR content. Read the actual check runs and review state for that exact
head before merging under existing authorization. Do not infer release, tag or
machine activation from a merged PR.

For issues, read the body and relevant comments before changing scope.
Distinguish an agreed decision from an older or superseded suggestion. When
posting a result, state the tested revision, concrete evidence and remaining
work. Use repository-relative paths or stable links in public text; omit private
machine paths, tokens and local state.
