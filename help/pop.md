# pop — claim one own unread message

```sh
tmux-a2a-postman pop [--config <absolute-path>]
```

No message ID, capture toggle or other positional argument is accepted. JSON:
workspace, as_of, display_as_of, success, markdown_absolute_path, self; optional
reason. A claimed message also has display_created_at from its validated
archived created_at. Display fields use the machine-local timezone with a
numeric offset; canonical as_of, message ID and path remain UTC/unchanged. A
non-null archive path also includes this instruction with the actual path:

> Read the complete file by running `cat '<markdown_absolute_path>'` directly
> through EOF. Do not use `execute-heredoc` for this read.
A successful empty POP omits instruction because there is no file to read.
A successful empty POP has markdown_absolute_path=null. A claimed message has
an existing absolute read path and a durable matching .pop.json receipt.
The path remains valid after claim; failed stdout cannot resurrect delivery.

Read that complete Markdown through EOF, including its generated reply
instructions. The notification does not select the POP result.
No FIFO ordering is promised. POP does not close reply obligations. Both a
claim and a valid empty POP trigger a bounded snapshot of the caller session.
Missing diagnostics are null and never change the claim's success.

self.unread_count counts inbox messages remaining after this POP. When it is
known and positive, instruction names that count and asks you to fully read
and handle this archive, then POP the next message without another notification.
Continue only while the latest operation reports a positive count. Stop on
zero, unknown or an empty POP; do not poll an empty inbox.
