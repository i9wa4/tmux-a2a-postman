# Invocation timing validation

Measured 2026-11-02 on the isolated Linux workspace using Go 1.26.10 and tmux
3.5a. Baseline is the published main revision
`ee8f698ff123d34ae5dac6003c9b781597d621ec` (#969). The candidate is this issue's
instrumentation. Both are ordinary `go build` binaries, with the same config,
message bodies, two sleeping native panes and temporary private storage.
Each group has 12 samples; retained history is 0 or 100 real sent/claimed
messages, and bursts launch four independent CLI processes. This is not an AI
latency/comprehension or successful-terminal-notification benchmark.

The committed benchmark script reproduces the setup and removes only its owned
fixtures. Full external process time includes startup, logging, capture,
summary/output and exit. Internal phase times use the separate documented
monotonic finalization boundary.

## 1. Quiet-run comparison

| Workload                                      | Baseline median / p95 ms | Candidate median / p95 ms | Median delta ms |
| --------------------------------------------- | ------------------------ | ------------------------- | --------------- |
| Empty POP, no retained mail                   | 93.70 / 99.16            | 106.15 / 120.13           | +12.45          |
| Empty POP, 100 retained messages              | 110.40 / 175.56          | 111.46 / 117.22           | +1.06           |
| Four-process POP burst, no retained mail      | 289.83 / 372.48          | 310.83 / 418.78           | +21.01          |
| Four-process POP burst, 100 retained messages | 325.31 / 369.27          | 322.18 / 368.09           | -3.13           |
| Read-only status, no retained mail            | 35.83 / 41.49            | 34.96 / 36.72             | -0.87           |
| Read-only status, 100 retained messages       | 54.16 / 66.15            | 48.81 / 53.86             | -5.36           |

Negative deltas are noise, not a claimed optimization. The candidate produced
321 separate invocation logs totaling 1,795,627 bytes (about 5.6 KiB per call),
with zero incomplete records and 321 successful operation outcomes. Capture
coverage was independently 286 complete / 35 partial; partial snapshots did not
undo mail. Read-only status created no logs. Retained diagnostic files were
not scanned or deleted by status or subsequent operations.

An earlier run alongside the full Go test suite produced 321 records totaling
1,766,502 bytes, also zero incomplete and all operation outcomes successful.
Its serial median deltas were +1.32 / +5.95 ms; its four-process retained-mail
p95 rose from 409.84 to 639.72 ms despite a median delta of +3.10 ms. This
outlier is retained as evidence of scheduling/contention variability, rather
than discarded to claim a universal tail bound.

For this implementation, the concrete review budget is **at most +20 ms
serial empty-POP median and +60 ms per four-process-burst median**, using the
same workload/config/build method on this fixture. Both measured runs meet
that budget. This is a local regression-review budget derived from observed
cost, not a product deadline, filesystem latency guarantee or a flaky CI wall
clock assertion. Always report p95, outcomes, incompletes and capture coverage;
12 samples cannot establish a reliable tail SLA. Reproduce with more samples
and matched host load before attributing a tail change to the recorder.

## 2. Correctness and isolation evidence

- Injected independent UTC/monotonic clocks cover backward wall changes,
  midnight crossing, nested spans and stable start-date partitioning.
- Concurrent goroutines preserve ordered complete JSONL, unique spans and
  bounded event omission. Separate real CLI processes create independent
  exclusive files; collisions cannot truncate old evidence.
- Private directories/files, symlink refusal and unverified-identity refusal
  are tested. A writer checks actual independent mailbox-lock admission on
  every diagnostic write, proving writes happen outside the mutation lock.
- Short writes and injected disk errors stop recording without retries.
  Metadata validation excludes arbitrary errors, bodies and unvalidated IDs.
- Real send, claimed POP and empty POP retain correlated IDs and truthful
  distinct mail/notice/capture results; all read-only command classes leave
  the saved-file tree unchanged.
- With a broken log destination, POP still claims once and an executed child
  still returns its exact exit status 7; stdout/stderr and required audit
  behavior remain intact.
- SIGKILL during an actual approval wait leaves a flushed phase start and no
  invented terminal event, and the unapproved source never runs. Reader tests
  preserve truncated tails/malformed histories and report incompleteness
  without repair or any permission to replay effects.

The automated test commands and the Nix analysis gate are documented in
[CONTRIBUTING.md](../../CONTRIBUTING.md). Model routing, live agent behavior and
cross-host clock synchronization are outside these measurements.
