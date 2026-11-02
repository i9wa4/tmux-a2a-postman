# pop — claim one own unread message

```sh
tmux-a2a-postman pop [--config <absolute-path>]
```

No message ID, capture toggle or other positional argument is accepted.
JSON: workspace, as_of, success, markdown_absolute_path, self; optional reason.
A non-null archive path also includes instruction: read that entire Markdown
through EOF before processing, replying or deciding no action is needed.
No truncated view, summary, notification or metadata substitutes for the file.
A successful empty POP omits instruction because there is no file to read.
A successful empty POP has markdown_absolute_path=null. A claimed message has
an existing absolute read path and a durable matching .pop.json receipt.
The path remains valid after claim; failed stdout cannot resurrect delivery.

Read that complete Markdown through EOF, including its generated reply
instructions. The hinted notification ID does not select the POP result.
No FIFO ordering is promised. POP does not close reply obligations. Both a
claim and a valid empty POP trigger a bounded snapshot of the caller session.
Missing diagnostics are null and never change the claim's success.
