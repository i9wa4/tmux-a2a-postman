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
milliseconds; this small private-fixture sample describes this runner only.

| Operation                               | Baseline        | Candidate       |
| --------------------------------------- | --------------- | --------------- |
| POP, no history                         | 116.96 / 121.41 | 68.20 / 72.43   |
| get-status, no history                  | 39.58 / 49.20   | 2.15 / 3.28     |
| Four simultaneous POP, no history       | 317.82 / 411.86 | 226.69 / 243.40 |
| POP, 30 retained messages               | 117.60 / 128.12 | 73.55 / 77.20   |
| get-status, 30 retained messages        | 43.66 / 46.62   | 2.80 / 3.56     |
| Four simultaneous POP, retained history | 326.41 / 413.50 | 242.47 / 285.66 |
| Cached oneline                          | —               | 2.92 / 3.15     |
| Idle refresh (no captures due)          | —               | 10.95 / 11.32   |
| Due refresh (two panes)                 | —               | 15.30 / 17.95   |

For equivalent normal operations, total private state was 534 files / 836,581
bytes at baseline versus 252 files / 642,174 bytes for the candidate, with no
incomplete diagnostic logs in either run. The candidate retained 12 raw pane
observations / 4,234 bytes (the forced due samples plus initial captures). Raw
history remains unbounded over time by explicit user policy; cadence reduces
redundant captures but does not provide a storage ceiling.

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
