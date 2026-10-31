# Offline send transport fixture

This fixture replaces the `tmux-a2a-postman` executable in a disposable test
`PATH`. It never delegates to the installed binary or contacts a live session.
Set `FAKE_POSTMAN_TRACE` to a disposable JSONL path and
`FAKE_POSTMAN_SCENARIO` to `no-unread` or `own-unread` before invoking it.

`body.md` is the exact send byte fixture; `archive.md` is the complete reply
byte fixture. A test prompt must specify recipient
`reviewer`, require a reply, and include those literal bytes. The trace grader
checks the exact route and flags, stdin bytes, exactly one send, and the
post-send command sequence for both self-state branches, including a complete
archive read through the fixture `cat` shim when own unread mail is known.
Its `pass` result means only that the captured trace followed this contract.

`run_offline.py` adds a scripted, no-model dispatcher probe. It accepts only
the exact `reviewer`/`--reply-required` send with all bytes of `body.md`
(including the final newline), one bare `pop` in the own-unread branch, and an
immediate exact-path read of the immutable archive. The dispatcher selects the
fixture scripts by explicit identity without shell or `PATH` lookup. Its own
in-memory receipts record each request and the complete stdout delivered to
the scripted caller after EOF, with byte length and SHA256. A refusal before
dispatch invokes no fixture subprocess. If a response fails validation after
dispatch, the executed receipt remains, the denial records the cumulative
invocation count, and denial is terminal. Tests cover
both branches and route, flag, newline, alternate executable/reader, polling,
duplicate operation, archive alteration/symlink, and fallback probes.

This is dispatcher-level evidence only. The scripted caller shares the host
process and UID; this fixture does not isolate it from arbitrary host files,
processes, network, the live Postman binary/socket/session, or the grader. It
does not show model cue-to-skill selection, installed catalog or expanded
prompt/instruction loading, provider identity/spend controls, or safe Waza
`copilot-sdk` execution. P01-A and the model trial remain open.

Run the deterministic fixture tests with:

```sh
python3 -m unittest discover -s evals/postman-send-message/fixtures -p 'test_*.py'
python3 evals/postman-send-message/fixtures/run_offline.py --scenario no-unread
python3 evals/postman-send-message/fixtures/run_offline.py --scenario own-unread
```

Any future agent eval must place this executable ahead of the live binary in
an isolated process environment and feed the resulting trace to
`grade_trace.py`. Review the exact expanded prompt, inherited instructions,
tool policy, environment, and provider costs before any model-backed run.
