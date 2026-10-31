# Issue 952: recipient smoke evidence

## 1. Setup and scope

Recorded on Linux, 2026-10-31, with real isolated tmux and interactive Bash.
Both recipient panes sourced `source <("$POSTMAN_BIN" notifier-script)` from
the rebuilt CLI and registered their native role/pane/PID bindings. The handler
was the shipped executable `integrations/bash/receipt-handler.py`, with
piped POP JSON stdin. No resident Postman process or paid model was used.

Command:

```sh
go test . -run '^TestNotificationShippedReceiptHandlerSmoke$' -v -count=1
```

The controller performed the initial send only. Bash's real Readline binding
invoked the handler, which POPed and read the full archive through EOF before
sending its exactly correlated digest receipt. The return key invoked the
sender's handler and POP. The test independently checked the full archive digest
and reply metadata using saved read files, without invoking recipient actions.

## 2. Observed evidence

| Fact                    | Observed evidence                                                                                              |
| ----------------------- | -------------------------------------------------------------------------------------------------------------- |
| Notification acceptance | Initial send returned `notice.status=accepted`                                                                 |
| Receiver POP/claim      | `m-f07f7279bbe2cd334e02d8e8879f8510` archived with POP receipt                                                 |
| Complete archive read   | SHA256 `770f30f212a715787d837a668172f63fba54731ba14c8ded20ba9c034ff70803` matched independent full-file digest |
| Exact response          | `m-98607a383233b180a8b0e1acfe2d3527` filled the original message/input-request pair                            |
| Return activation       | Sender's notification hook claimed that exact response                                                         |
| Result                  | Test PASS                                                                                                      |

`accepted` alone establishes only key enqueue. The archive/claim, full-read
digest and exact response establish the later facts separately. This smoke
proves the shipped Bash recipient integration; it does not claim a live paid
Codex model response. The supplied Codex handler uses the same hook and POP
contract, invoking the installed agent's noninteractive CLI.

## 3. Additional transport checks

Notification-driven tests also cover same-session requests/replies with a
pending composer command, direct parent/child interfaces in both directions,
and present-reviewer approval/rejection. Removing recipient registration leaves
mail durable and prevents the receiver from acting. Unit tests cover stale
binding, absent registration, committed-message verification, fresh bounded
notification context and transport failure without rollback.
