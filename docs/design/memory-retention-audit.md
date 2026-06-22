# Memory retention audit

This is the issue #501 audit of long-lived daemon and projection retention
surfaces after the runtime diagnostics and loaded-session fixtures were added.

## 1. Evidence

The count-only mailbox state path was measured with:

```sh
go test ./internal/projection -run '^$' -bench BenchmarkProjectMailboxState_LoadedSession10000MailboxEvents500ReadArchives -benchmem -benchtime=1x -count=3
```

Fixture shape:

- 10,000 mailbox events
- 500 read archives
- 128 daemon-submit responses
- 8 status snapshots
- 512 byte message bodies

Before the streaming projection change:

| run | ns/op       | B/op       | allocs/op |
| --- | ----------: | ---------: | --------: |
| 1   | 217,748,480 | 59,269,016 | 394,488   |
| 2   | 205,472,858 | 59,268,888 | 394,487   |
| 3   | 207,912,273 | 59,268,888 | 394,487   |

After the streaming projection change:

| run | ns/op       | B/op       | allocs/op |
| --- | ----------: | ---------: | --------: |
| 1   | 220,743,996 | 52,880,088 | 375,747   |
| 2   | 198,839,537 | 52,878,088 | 375,720   |
| 3   | 209,489,590 | 52,878,120 | 375,720   |

The after result reduces per-projection allocation from about 59.27 MB/op to
52.88 MB/op and allocation count from about 394.5k allocs/op to 375.7k
allocs/op. Runtime is noise-level equivalent for the 1x fixture.

## 2. Audited surfaces

| Surface                                          | Conclusion                                                                                                                                                                                                                                         | Evidence                                                                                                                                                           |
| ------------------------------------------------ | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `journal.Replay` returning all raw events        | Changed. `journal.ReplayEach` streams validated events through a callback; `Replay` keeps the old slice-returning API by appending streamed events.                                                                                                | `internal/journal/journal.go`; `TestReplayEach_StreamsValidatedEventsAndStopsOnCallbackError`; existing replay validation tests still pass.                        |
| `MailboxEventPayload.Content` in replay payloads | Bounded by use case. Full content is still required by `ProjectMailboxProjection` and `SyncMailboxProjection` to reconstruct mailbox files from the journal. The count-only path no longer unmarshals content into a long-lived payload value.     | `internal/projection/mailbox_projection.go` still owns file reconstruction; `ProjectMailboxState` now decodes only `message_id`, `to`, and `path`.                 |
| `ProjectMailboxState` full replay state          | Changed. It streams journal records and keeps only maps needed for unread counts. It no longer retains the full event slice or payload content for count projection.                                                                               | Benchmark above; `TestProjectMailboxState_UsesMailboxMetadataWithoutContent`; existing mailbox state tests pass.                                                   |
| Runtime node maps                                | Bounded by scan snapshots. `nodes` is replaced from discovery, `knownNodes` is pruned against the fresh snapshot, and `claimedPanes` is pruned against live pane IDs.                                                                              | `internal/daemon/runtime.go`; runtime tests cover known-node and claimed-pane cleanup behavior.                                                                    |
| Runtime watcher directory map                    | Changed. `watchedDirs` now removes directories that are no longer desired by the fresh node snapshot and calls watcher `Remove` before deleting the map entry.                                                                                     | `internal/daemon/runtime.go`; `TestPruneWatchedDirsRemovesDirsForDisappearedNodes`.                                                                                |
| Active post delivery map                         | Bounded by path guard lifetime. Accepted deliveries call `finishPostEvent` on normal completion, retry completion, missing file, or goroutine defer.                                                                                               | `beginPostEvent`, `finishPostEvent`, and delivery defer in `internal/daemon/runtime.go`; `TestPostEventGuard_DedupesByPathUntilFinished`.                          |
| Active auto-ping map                             | Bounded by per-node goroutine lifetime. `beginAutoPing` suppresses duplicate in-flight pings and `finishAutoPing` deletes the key when the send path returns.                                                                                      | `internal/daemon/runtime.go`; auto-ping tests wait for active keys to clear.                                                                                       |
| Active daemon-submit map                         | Bounded by worker result handling. `activeDaemonSubmitKeys` is keyed by operation, capped by `daemonSubmitWorkerLimit`, and deleted in `handleDaemonSubmitResult`. Runtime diagnostics expose only counts and ages.                                | `internal/daemon/runtime.go`; daemon-submit runtime tests cover active, pending, claimed, late response, and saturation counts.                                    |
| Mailbox projection sync maps                     | Bounded by session directory and coalescing. Only one active sync per session runs; duplicate requests become a pending bit and both active and pending maps are cleared in `runMailboxProjectionSync`.                                            | `scheduleMailboxProjectionSync` and `runMailboxProjectionSync` in `internal/daemon/runtime.go`.                                                                    |
| Idle pane capture state                          | Changed. Pane state keeps hashes, timestamps, counts, trigger identity, marker count, and prefix hash/line count. It no longer retains captured pane text through `LastCompactionPrefix`; stale pane entries are deleted after `NodeStaleSeconds`. | `internal/idle/idle.go`; compaction-marker tests cover repeated marker behavior after replacing text with hash state; stale cleanup remains in `checkPaneCapture`. |
| Late daemon-submit responses                     | Bounded in memory and reported in diagnostics. Response files may remain on disk for inspection after client timeout, but daemon status scans keep only counts and oldest ages; responses are not accumulated in a daemon heap slice.              | `scanDaemonSubmitResponses` in `internal/daemon/runtime.go`; README timeout guidance points operators to `inspect-daemon-submit` and `get-status --debug`.         |

## 3. Operator surface

No command behavior or help text changed. Existing `get-status --debug`
diagnostics already expose bounded daemon cardinality and daemon-submit queue
health, and existing timeout guidance already points operators at late-response
inspection. The changes here reduce internal retention without adding new
operator-facing diagnostics.
