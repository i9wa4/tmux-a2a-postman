# Share files through Gists and Issues

An existing file can be shared without creating a Postman task record. Verify
the intended destination, repository or account, file name, exact content and
requested visibility before publication. Use the service's supported upload or
comment operation and inspect the resulting page or API response to confirm the
published file/body and destination. Keep source revision and meaningful
limitations with the result.

A secret or unlisted Gist is discoverable by anyone who has its URL; it is not
access-controlled. Do not put credentials, private machine paths or personal
destination settings in the public embedded Skill package. If authorization for
publication or destination is missing, prepare the exact artifact and ask
through the configured route. Preserve a failed or uncertain publication result;
do not blindly repeat a possibly committed operation.

For Gist publication and verification, read [Gist delivery](gist-delivery.md)
with `tmux-a2a-postman skill git-github-operations gist-delivery`. For Issue
creation and snapshot comments, read [Issue mailbox](issue-mailbox.md) with
`tmux-a2a-postman skill git-github-operations issue-mailbox`. The binary
materializes their scripts through `tmux-a2a-postman resources`; a source file
needs no task record.
