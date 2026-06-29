# Daemon Soak And Pressure Validation

This runbook defines the acceptance envelope for week-scale daemon uptime under
parallel mailbox and daemon-submit activity. It is intentionally separate from
focused fixes such as memory retention, worker fairness, and RSS reporting.

## 1. Scenario

- Duration: run one daemon continuously for at least 168 hours. For development
  smoke tests, collect at least three samples over at least 10 minutes.
- Sessions: at least 4 tmux sessions, each with at least 4 active nodes.
- Traffic mix per session:
  - sustained `send-heredoc` and `pop` daemon-submit traffic;
  - periodic direct post reconciliation by leaving some non-owned session posts;
  - auto-PING enabled for idle nodes;
  - `get-status --debug` sampled every 10 minutes;
  - one heap and one goroutine `capture-profile` only when metrics indicate
    growth or stuck work.
- Pressure target: daemon-submit workers should be saturated at least once
  during the run without causing unbounded pending queues or late responses.

## 2. Evidence To Capture

Save each `get-status --debug` response as JSON. The committed validator checks
the `runtime_diagnostics` object from those samples:

```sh
tmux-a2a-postman get-status --debug > samples/status-$(date -u +%Y%m%dT%H%M%SZ).json
scripts/validation/daemon-soak-check.sh samples/status-*.json
```

Keep the raw files outside the repository unless they are redacted fixtures.
Do not commit private session names, message bodies, pane content, local
absolute paths, or production mailbox archives.

The samples must include:

- Go heap allocation, heap system bytes, object count, total allocation, GC
  count, and goroutine count;
- daemon session, node, watched-directory, claimed-pane, active post event,
  auto-PING, and active daemon-submit cardinalities;
- daemon-submit worker limit, active workers, active requests, pending request
  count and oldest age, claimed count and oldest age, late response count and
  oldest age, saturation count, and last saturation timestamp when present;
- RSS fields when the runtime supports them.

## 3. Default Thresholds

The default validator thresholds are intentionally conservative. Override them
only when the runbook for a specific machine class records the reason.

- At least 3 samples must be present.
- Final heap allocation must not grow by more than 25% from the first sample
  after warmup.
- Final `memory_sys_bytes` must not grow by more than 25% from the first
  sample after warmup.
- Final goroutine count must not grow by more than 20.
- Final late response count must be 0.
- Oldest late response age must be 0 seconds.
- Oldest pending request age must stay at or below 30 seconds.
- Oldest claimed request age must stay at or below 30 seconds.

Pass/fail interpretation:

- Memory growth above threshold is a retention failure unless the raw evidence
  shows a one-time warmup transition and later samples stabilize.
- Pending or claimed age above threshold is queue pressure that needs a
  scheduling or handler investigation.
- Any late response accumulation after the run is a cleanup or queue-health
  failure.
- Goroutine growth above threshold is a leak candidate and should be paired
  with an explicit goroutine profile.

## 4. CI-Safe Smoke Validation

The validator itself is CI-safe:

```sh
go test ./scripts/validation
```

The smoke test does not run a daemon for a week. It only verifies the parser and
threshold logic used against real soak samples.

## 5. Closure Criteria For #563

Before closing the week-scale issue, attach a summary with:

- run duration and host class;
- session and node counts;
- traffic generator description;
- sample count and cadence;
- validator command and result;
- first and final memory, goroutine, queue, saturation, and late-response
  values;
- links to follow-up issues for any RSS, memory-retention, fairness, queue
  delay, or stale-response gaps still outside this runbook.
