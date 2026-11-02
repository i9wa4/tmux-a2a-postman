# Server status validation — 2026-11-02

The current contract and writer ownership live in
[server-status](../design/server-status.md); this page records evidence, not a
second setup guide. Baseline source is main `2fab3078`; the candidate is the

## 1. source. Neither measurement establishes installed-fleet activation or AI

compliance.

### 1.1. Native regressions

Isolated native Go cases cover cadence, raw history including ordinary panes,
empty POP integration, exact process replacement, backwards clock baseline,
independent sockets and restart, `$0/$3/$9/$12` numbering/rename/gaps,
configuration conflicts independent of publisher, bounded partial recovery,
nonblocking busy storms, close-on-exec, stable lock inode and owner death during
all four phases, malformed/unsafe/incompatible cache, old/new atomic
publication, and injected pre-rename disk / post-rename durability failures.
Getter process tests install a tmux proxy that fails if called, remove
configuration, unset caller pane, change tool cwd, and compare every state
file/mtime unchanged. The saved bytes remain identical even after the owned test
server exits.

Existing real-terminal notification, exact POP/full-EOF/reply, approval,
rejection, external and recursive-team checks remain mandatory. Status consumers
now match native panes and derive declared local interface adjacency from saved
scope. Fixed actors drain the last authoritative operation-local self unread
count, not an operator cache that may legitimately lag after skipped refresh.

### 1.2. Linux process measurements

Five samples per group, two native sleep panes, 30 retained request/archive
pairs and four simultaneous POP processes. Timings below are median / p95 in
milliseconds; this small private-fixture sample describes this shared runner
only. Other development processes were running; these are descriptive
observations, not a controlled hardware-independent speedup claim.

| Operation                               | Baseline        | Candidate       |
| --------------------------------------- | --------------- | --------------- |
| POP, no history                         | 131.53 / 134.93 | 70.92 / 72.63   |
| get-status, no history                  | 45.03 / 58.34   | 3.26 / 4.51     |
| Four simultaneous POP, no history       | 512.48 / 643.37 | 242.40 / 283.06 |
| POP, 30 retained messages               | 112.97 / 118.78 | 78.65 / 83.00   |
| get-status, 30 retained messages        | 40.13 / 47.21   | 2.24 / 3.24     |
| Four simultaneous POP, retained history | 291.37 / 386.91 | 252.33 / 275.01 |
| Cached oneline                          | —               | 1.89 / 2.98     |
| Idle refresh (no captures due)          | —               | 8.49 / 10.03    |
| Due refresh (two panes)                 | —               | 15.77 / 17.50   |

For equivalent normal operations, total private state was 526 files / 833,152
bytes at baseline versus 252 files / 690,780 bytes for the candidate, with no
incomplete diagnostic logs in either run. The baseline retained 178 raw pane
observations / 62,818 bytes. The candidate retained 12 raw pane observations /
4,233 bytes (the forced due samples plus initial captures). Raw history remains
unbounded over time by explicit user policy; cadence reduces redundant captures
but does not provide a storage ceiling.

```sh
python3 scripts/validation/benchmark-invocations.py \
  --baseline /absolute/baseline --candidate /absolute/candidate \
  --samples 5 --history 30 --concurrency 4
```

The fixture explicitly ages only its own cache timestamps for repeatable due
measurements, outside timed sections. This is not a product force-refresh flag,
cache recovery recipe or behavior to put in status-bar wrappers. It starts and
cleans only its private tmux server, HOME and state. All native peers use the
same explicit workspace directory, independent of the caller's tmux defaults.

The `status-benchmark` flake check runs the candidate measurement (three
samples, ten retained messages) on both Linux and macOS CI and prints the
timing/storage JSON. It asserts contract/diagnostic completeness, not
hardware-dependent speed thresholds. macOS timings must be read from that job;
the Linux table is not a macOS claim. Required flake/build/CI status belongs to
the owning PR's exact head.
