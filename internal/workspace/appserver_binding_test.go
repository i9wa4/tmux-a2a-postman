package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestAppServerReservationBindsRecipientAndResumesOwnAttempt(t *testing.T) {
	mail := mailFixture(t)
	ctx := context.Background()
	worker := mailRuntime("worker")
	peer := mailRuntime("peer")
	pid := strconv.Itoa(os.Getpid())
	worker.snapshot.Bindings[1].PanePID = pid
	peer.snapshot.Bindings[1].PanePID = pid
	peer.snapshot.Caller = peer.snapshot.Bindings[1]
	dir, err := os.MkdirTemp("/tmp", "pm937-app-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "app.sock")
	if err := mail.Store.RegisterAppServer(ctx, peer, socket); err != nil {
		t.Fatal(err)
	}
	msg, err := mail.Send(ctx, worker, "peer", "committed body", "", false)
	if err != nil {
		t.Fatal(err)
	}
	reservation, err := mail.Store.ReserveAppServerNotice(ctx, worker, msg)
	if err != nil || reservation.Socket != socket || reservation.Target != worker.snapshot.Bindings[1] {
		t.Fatalf("reservation failed or lost binding: %+v %v", reservation, err)
	}
	resumed, err := mail.Store.ReserveAppServerNotice(ctx, worker, msg)
	if err != nil || resumed != reservation {
		t.Fatalf("same-owner pre-RPC retry did not resume the attempt: %+v %v", resumed, err)
	}
	if err := mail.Store.MarkAppServerNoticeRPCStarted(ctx, worker, msg, reservation.AttemptID); err != nil {
		t.Fatal(err)
	}
	if _, err := mail.Store.ReserveAppServerNotice(ctx, worker, msg); !errors.Is(err, ErrBusy) {
		t.Fatalf("rpc_started attempt replayed: %v", err)
	}
	recordPath := filepath.Join(mail.Store.StateRoot, "repo", "runtime", "appserver-notices", msg.ID+".json")
	if _, err := os.Stat(recordPath); err != nil {
		t.Fatalf("durable reservation absent: %v", err)
	}
}

func TestAppServerRegistrationAndReservationRejectForeignOrStalePane(t *testing.T) {
	mail := mailFixture(t)
	ctx := context.Background()
	peer := mailRuntime("peer")
	dir, err := os.MkdirTemp("/tmp", "pm937-app-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "app.sock")
	peer.snapshot.Bindings[1].PanePID = strconv.Itoa(os.Getpid() + 1)
	peer.snapshot.Caller = peer.snapshot.Bindings[1]
	if err := mail.Store.RegisterAppServer(ctx, peer, socket); !errors.Is(err, ErrUnknown) {
		t.Fatalf("foreign process registered endpoint: %v", err)
	}
	peer.snapshot.Bindings[1].PanePID = strconv.Itoa(os.Getpid())
	peer.snapshot.Caller = peer.snapshot.Bindings[1]
	if err := mail.Store.RegisterAppServer(ctx, peer, socket); err != nil {
		t.Fatal(err)
	}
	worker := mailRuntime("worker")
	worker.snapshot.Bindings[1].PanePID = strconv.Itoa(os.Getpid())
	msg, err := mail.Send(ctx, worker, "peer", "body", "", false)
	if err != nil {
		t.Fatal(err)
	}
	stale := mailRuntime("worker")
	stale.snapshot.Bindings[1].PanePID = strconv.Itoa(os.Getpid())
	stale.snapshot.Bindings[1].Owner.Pane = "%9"
	if _, err := mail.Store.ReserveAppServerNotice(ctx, stale, msg); !errors.Is(err, ErrUnknown) {
		t.Fatalf("stale pane registration accepted: %v", err)
	}
}

func TestNotifyCommittedAppServerRetriesAfterAbsentSocket(t *testing.T) {
	mail := mailFixture(t)
	ctx := context.Background()
	worker, peer := mailRuntime("worker"), mailRuntime("peer")
	pid := strconv.Itoa(os.Getpid())
	worker.snapshot.Bindings[1].PanePID = pid
	peer.snapshot.Bindings[1].PanePID = pid
	peer.snapshot.Caller = peer.snapshot.Bindings[1]
	dir, err := os.MkdirTemp("/tmp", "pm937-app-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	if err := mail.Store.RegisterAppServer(ctx, peer, filepath.Join(dir, "absent.sock")); err != nil {
		t.Fatal(err)
	}
	msg, err := mail.Send(ctx, worker, "peer", "body", "", false)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(mail.Store.StateRoot, "repo", "runtime", "appserver-notices", msg.ID+".json")
	readJSONFile := func(path string, dst any) error {
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return json.Unmarshal(data, dst)
	}
	firstResult := mail.Store.NotifyCommittedAppServerAfterCommit(ctx, worker, msg)
	if firstResult.Status != "unavailable" {
		t.Fatalf("first pre-write failure: %+v", firstResult)
	}
	var first appServerNoticeReservation
	if err := readJSONFile(path, &first); err != nil || first.State != "dial_failed" {
		t.Fatalf("first attempt was not safe to retry: %+v %v", first, err)
	}
	secondResult := mail.Store.NotifyCommittedAppServerAfterCommit(ctx, worker, msg)
	if secondResult.Status != "unavailable" {
		t.Fatalf("same-message retry: %+v", secondResult)
	}
	var second appServerNoticeReservation
	if err := readJSONFile(path, &second); err != nil || second.State != "dial_failed" || second.AttemptID == first.AttemptID {
		t.Fatalf("retry did not create a fresh safe attempt: %+v %v", second, err)
	}
}

// A failed dial before any JSON-RPC frame is a proved non-delivery. A durable
// RPCStarted intent, even if the process later dies, is never blindly replayed.
func TestAppServerNoticeRetryOnlyAfterProvenPreRPCFailure(t *testing.T) {
	mail := mailFixture(t)
	ctx := context.Background()
	worker := mailRuntime("worker")
	peer := mailRuntime("peer")
	pid := strconv.Itoa(os.Getpid())
	worker.snapshot.Bindings[1].PanePID = pid
	peer.snapshot.Bindings[1].PanePID = pid
	peer.snapshot.Caller = peer.snapshot.Bindings[1]
	dir, err := os.MkdirTemp("/tmp", "pm937-app-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "absent.sock")
	if err := mail.Store.RegisterAppServer(ctx, peer, socket); err != nil {
		t.Fatal(err)
	}
	msg, err := mail.Send(ctx, worker, "peer", "body", "", false)
	if err != nil {
		t.Fatal(err)
	}
	first, err := mail.Store.ReserveAppServerNotice(ctx, worker, msg)
	if err != nil {
		t.Fatal(err)
	}
	dialCtx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	if conn, err := dialCodexSocket(dialCtx, socket, os.Getpid()); err == nil {
		conn.close()
		t.Fatal("absent socket accepted connection")
	}
	if err := mail.Store.MarkAppServerNoticeDialFailed(ctx, worker, msg, first.AttemptID); err != nil {
		t.Fatal(err)
	}
	second, err := mail.Store.ReserveAppServerNotice(ctx, worker, msg)
	if err != nil || second.AttemptID == first.AttemptID {
		t.Fatalf("proved pre-RPC failure did not permit new attempt: %+v %v", second, err)
	}
	if err := mail.Store.MarkAppServerNoticeRPCStarted(ctx, worker, msg, first.AttemptID); !errors.Is(err, ErrUnknown) {
		t.Fatalf("old attempt changed new reservation: %v", err)
	}
	if err := mail.Store.MarkAppServerNoticeRPCStarted(ctx, worker, msg, second.AttemptID); err != nil {
		t.Fatal(err)
	}
	if err := mail.Store.MarkAppServerNoticeDialFailed(ctx, worker, msg, second.AttemptID); !errors.Is(err, ErrUnknown) {
		t.Fatalf("ambiguous RPC attempt marked safe to retry: %v", err)
	}
	if _, err := mail.Store.ReserveAppServerNotice(ctx, worker, msg); !errors.Is(err, ErrBusy) {
		t.Fatalf("RPC-started notice replayed: %v", err)
	}
}

func TestAppServerNoticeCrashBoundary(t *testing.T) {
	mail := mailFixture(t)
	ctx := context.Background()
	worker := mailRuntime("worker")
	peer := mailRuntime("peer")
	pid := strconv.Itoa(os.Getpid())
	worker.snapshot.Bindings[1].PanePID = pid
	peer.snapshot.Bindings[1].PanePID = pid
	peer.snapshot.Caller = peer.snapshot.Bindings[1]
	dir, err := os.MkdirTemp("/tmp", "pm937-app-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	if err := mail.Store.RegisterAppServer(ctx, peer, filepath.Join(dir, "app.sock")); err != nil {
		t.Fatal(err)
	}
	msg, err := mail.Send(ctx, worker, "peer", "body", "", false)
	if err != nil {
		t.Fatal(err)
	}
	first, err := mail.Store.ReserveAppServerNotice(ctx, worker, msg)
	if err != nil {
		t.Fatal(err)
	}
	dead := exec.Command("true")
	if err := dead.Run(); err != nil {
		t.Fatal(err)
	}
	if processLiveness(dead.Process.Pid) != Dead {
		t.Skip("exited fixture PID not yet observably dead")
	}
	path := filepath.Join(mail.Store.StateRoot, "repo", "runtime", "appserver-notices", msg.ID+".json")
	setDeadOwner := func(record appServerNoticeReservation) {
		t.Helper()
		record.OwnerPID = dead.Process.Pid
		data, err := json.Marshal(record)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	setDeadOwner(first)
	second, err := mail.Store.ReserveAppServerNotice(ctx, worker, msg)
	if err != nil || second.AttemptID == first.AttemptID {
		t.Fatalf("dead pre-RPC owner not reclaimed: %+v %v", second, err)
	}
	if err := mail.Store.MarkAppServerNoticeRPCStarted(ctx, worker, msg, second.AttemptID); err != nil {
		t.Fatal(err)
	}
	second.State = "rpc_started"
	setDeadOwner(second)
	if _, err := mail.Store.ReserveAppServerNotice(ctx, worker, msg); !errors.Is(err, ErrBusy) {
		t.Fatalf("dead ambiguous RPC owner replayed: %v", err)
	}
}

// A killed writer may leave a malformed staging file, but never a malformed
// published message-ID record. A later reservation can safely publish anew.
func TestAppServerNoticePartialInitialWriteCrash(t *testing.T) {
	if rootPath := os.Getenv("PM937_NOTICE_CRASH_ROOT"); rootPath != "" {
		root, err := os.OpenRoot(rootPath)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = root.Close() }()
		name := os.Getenv("PM937_NOTICE_CRASH_NAME")
		err = publishNewAppServerNotice(root, name, appServerNoticeReservation{Version: 1}, func(root *os.Root, temp string, _ any) error {
			file, err := root.OpenFile(temp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
			if err != nil {
				return err
			}
			if _, err := file.Write([]byte("{")); err != nil {
				return err
			}
			if err := file.Sync(); err != nil {
				return err
			}
			process, err := os.FindProcess(os.Getpid())
			if err != nil {
				return err
			}
			return process.Kill()
		})
		t.Fatalf("crash fixture survived: %v", err)
	}
	mail := mailFixture(t)
	ctx := context.Background()
	worker := mailRuntime("worker")
	peer := mailRuntime("peer")
	pid := strconv.Itoa(os.Getpid())
	worker.snapshot.Bindings[1].PanePID = pid
	peer.snapshot.Bindings[1].PanePID = pid
	peer.snapshot.Caller = peer.snapshot.Bindings[1]
	dir, err := os.MkdirTemp("/tmp", "pm937-app-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	if err := mail.Store.RegisterAppServer(ctx, peer, filepath.Join(dir, "app.sock")); err != nil {
		t.Fatal(err)
	}
	msg, err := mail.Send(ctx, worker, "peer", "body", "", false)
	if err != nil {
		t.Fatal(err)
	}
	noticesDir := filepath.Join(mail.Store.StateRoot, "repo", "runtime", "appserver-notices")
	if err := os.Mkdir(noticesDir, 0o700); err != nil {
		t.Fatal(err)
	}
	name := msg.ID + ".json"
	child := exec.Command(os.Args[0], "-test.run=^TestAppServerNoticePartialInitialWriteCrash$")
	child.Env = append(os.Environ(), "PM937_NOTICE_CRASH_ROOT="+noticesDir, "PM937_NOTICE_CRASH_NAME="+name)
	if err := child.Run(); err == nil {
		t.Fatal("partial writer did not die")
	}
	if _, err := os.Lstat(filepath.Join(noticesDir, name)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("partial record published under final name: %v", err)
	}
	entries, err := os.ReadDir(noticesDir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("expected one interrupted staging file: %v %+v", err, entries)
	}
	data, err := os.ReadFile(filepath.Join(noticesDir, entries[0].Name()))
	if err != nil || string(data) != "{" {
		t.Fatalf("crash fixture did not leave partial staging data: %q %v", data, err)
	}
	record, err := mail.Store.ReserveAppServerNotice(ctx, worker, msg)
	if err != nil || record.MessageID != msg.ID {
		t.Fatalf("safe retry after partial unpublished write failed: %+v %v", record, err)
	}
	if err := mail.Store.MarkAppServerNoticeRPCStarted(ctx, worker, msg, record.AttemptID); err != nil {
		t.Fatal(err)
	}
	if _, err := mail.Store.ReserveAppServerNotice(ctx, worker, msg); !errors.Is(err, ErrBusy) {
		t.Fatalf("ambiguous RPC-started record replayed: %v", err)
	}
}

func TestPublishNewAppServerNoticeDoesNotReplaceExisting(t *testing.T) {
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	name := "notice.json"
	first := appServerNoticeReservation{Version: 1, AttemptID: "first", MessageID: "message", State: "rpc_started"}
	if err := publishNewAppServerNotice(root, name, first, writeJSON); err != nil {
		t.Fatal(err)
	}
	second := first
	second.AttemptID = "second"
	if err := publishNewAppServerNotice(root, name, second, writeJSON); !errors.Is(err, os.ErrExist) {
		t.Fatalf("second publication replaced existing attempt: %v", err)
	}
	var saved appServerNoticeReservation
	if err := readJSON(root, name, &saved); err != nil || saved != first {
		t.Fatalf("first attempt changed: %+v %v", saved, err)
	}
}

func TestAppServerNoticePostLinkCrashRecovery(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("two-link recovery unsupported")
	}
	if rootPath := os.Getenv("PM937_POSTLINK_ROOT"); rootPath != "" {
		root, err := os.OpenRoot(rootPath)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = root.Close() }()
		var record appServerNoticeReservation
		if err := json.Unmarshal([]byte(os.Getenv("PM937_POSTLINK_RECORD")), &record); err != nil {
			t.Fatal(err)
		}
		record.OwnerPID = os.Getpid()
		err = publishNewAppServerNoticeWithHooks(root, os.Getenv("PM937_POSTLINK_NAME"), record, writeJSON, func() error {
			p, err := os.FindProcess(os.Getpid())
			if err != nil {
				return err
			}
			return p.Kill()
		}, (*os.Root).Remove)
		t.Fatalf("post-link crash fixture survived: %v", err)
	}
	mail := mailFixture(t)
	ctx := context.Background()
	worker, peer := mailRuntime("worker"), mailRuntime("peer")
	pid := strconv.Itoa(os.Getpid())
	worker.snapshot.Bindings[1].PanePID = pid
	peer.snapshot.Bindings[1].PanePID = pid
	peer.snapshot.Caller = peer.snapshot.Bindings[1]
	socketDir, err := os.MkdirTemp("/tmp", "pm937-app-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(socketDir) })
	socket := filepath.Join(socketDir, "app.sock")
	if err := mail.Store.RegisterAppServer(ctx, peer, socket); err != nil {
		t.Fatal(err)
	}
	msg, err := mail.Send(ctx, worker, "peer", "body", "", false)
	if err != nil {
		t.Fatal(err)
	}
	noticesDir := filepath.Join(mail.Store.StateRoot, "repo", "runtime", "appserver-notices")
	if err := os.Mkdir(noticesDir, 0o700); err != nil {
		t.Fatal(err)
	}
	name := msg.ID + ".json"
	original := appServerNoticeReservation{
		Version: 1, AttemptID: "postlink-crash", MessageID: msg.ID,
		Target: worker.snapshot.Bindings[1], Socket: socket, State: "reserved",
	}
	payload, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	const priorCount = 1030
	for i := range priorCount {
		prior := original
		prior.MessageID = fmt.Sprintf("prior-%04d", i)
		prior.State = "rpc_started"
		priorData, err := json.Marshal(prior)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(noticesDir, prior.MessageID+".json"), priorData, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	child := exec.Command(os.Args[0], "-test.run=^TestAppServerNoticePostLinkCrashRecovery$")
	child.Env = append(os.Environ(), "PM937_POSTLINK_ROOT="+noticesDir,
		"PM937_POSTLINK_NAME="+name, "PM937_POSTLINK_RECORD="+string(payload))
	if err := child.Run(); err == nil {
		t.Fatal("post-link writer did not die")
	}
	finalInfo, err := os.Lstat(filepath.Join(noticesDir, name))
	if err != nil {
		t.Fatal(err)
	}
	if !finalInfo.Mode().IsRegular() || finalInfo.Mode().Perm() != 0o600 {
		t.Fatalf("published final is not a private regular file: %v", finalInfo.Mode())
	}
	rootBefore, err := os.OpenRoot(noticesDir)
	if err != nil {
		t.Fatal(err)
	}
	var premature appServerNoticeReservation
	strictErr := readJSON(rootBefore, name, &premature)
	_ = rootBefore.Close()
	if strictErr == nil {
		t.Fatal("two-link final passed strict reader before recovery")
	}
	entries, err := os.ReadDir(noticesDir)
	if err != nil || len(entries) != priorCount+2 {
		t.Fatalf("expected accumulated records plus final and staging links: %v count=%d", err, len(entries))
	}
	var stage string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".new-") {
			stage = e.Name()
		}
	}
	if stage == "" {
		t.Fatal("staging link not found")
	}
	stageInfo, err := os.Lstat(filepath.Join(noticesDir, stage))
	if err != nil || !os.SameFile(finalInfo, stageInfo) {
		t.Fatalf("stage is not final inode: %v", err)
	}
	if processLiveness(child.Process.Pid) != Dead {
		t.Skip("killed fixture PID not yet observably dead")
	}
	retry, err := mail.Store.ReserveAppServerNotice(ctx, worker, msg)
	if err != nil || retry.AttemptID == original.AttemptID {
		t.Fatalf("pre-RPC retry failed: %+v %v", retry, err)
	}
	root, err := os.OpenRoot(noticesDir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	var saved appServerNoticeReservation
	if err := readJSON(root, name, &saved); err != nil || saved != retry {
		t.Fatalf("strict read failed: %+v %v", saved, err)
	}
	entries, err = os.ReadDir(noticesDir)
	if err != nil || len(entries) != priorCount+1 {
		t.Fatalf("stage remained: %v count=%d", err, len(entries))
	}
	if err := mail.Store.MarkAppServerNoticeRPCStarted(ctx, worker, msg, retry.AttemptID); err != nil {
		t.Fatal(err)
	}
	if _, err := mail.Store.ReserveAppServerNotice(ctx, worker, msg); !errors.Is(err, ErrBusy) {
		t.Fatalf("RPC-started attempt replayed: %v", err)
	}
}

func TestPublishNewAppServerNoticeCleanupFailureRecovers(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("two-link recovery unsupported")
	}
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	record := appServerNoticeReservation{Version: 1, AttemptID: "cleanup-failure", MessageID: "message", State: "reserved"}
	failure := errors.New("injected cleanup failure")
	err = publishNewAppServerNoticeWithHooks(root, "notice.json", record, writeJSON, nil,
		func(*os.Root, string) error { return failure })
	if !errors.Is(err, failure) {
		t.Fatalf("cleanup failure ignored: %v", err)
	}
	var premature appServerNoticeReservation
	if err := readJSON(root, "notice.json", &premature); err == nil {
		t.Fatal("two-link final passed strict reader before recovery")
	}
	var saved appServerNoticeReservation
	if err := readAppServerNotice(root, "notice.json", &saved); err != nil || saved != record {
		t.Fatalf("failed cleanup not recovered: %+v %v", saved, err)
	}
	if err := readJSON(root, "notice.json", &saved); err != nil || saved != record {
		t.Fatalf("strict read failed: %+v %v", saved, err)
	}
}

// A public reservation can report a post-Link cleanup failure after its
// record was published. The same live owner must recover that attempt ID
// before any RPC write; rpc_started remains a no-replay boundary.
func TestAppServerNoticePostLinkFailurePublicRetryLiveOwner(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("two-link recovery unsupported")
	}
	mail := mailFixture(t)
	ctx := context.Background()
	worker, peer := mailRuntime("worker"), mailRuntime("peer")
	pid := strconv.Itoa(os.Getpid())
	worker.snapshot.Bindings[1].PanePID = pid
	peer.snapshot.Bindings[1].PanePID = pid
	peer.snapshot.Caller = peer.snapshot.Bindings[1]
	socketDir, err := os.MkdirTemp("/tmp", "pm937-app-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(socketDir) })
	if err := mail.Store.RegisterAppServer(ctx, peer, filepath.Join(socketDir, "app.sock")); err != nil {
		t.Fatal(err)
	}
	msg, err := mail.Send(ctx, worker, "peer", "body", "", false)
	if err != nil {
		t.Fatal(err)
	}
	failure := errors.New("injected post-Link staging removal failure")
	mail.Store.noticePublish = func(root *os.Root, name string, record appServerNoticeReservation) error {
		return publishNewAppServerNoticeWithHooks(root, name, record, writeJSON, nil,
			func(*os.Root, string) error { return failure })
	}
	if _, err := mail.Store.ReserveAppServerNotice(ctx, worker, msg); !errors.Is(err, failure) {
		t.Fatalf("public Reserve did not report injected failure: %v", err)
	}
	noticesDir := filepath.Join(mail.Store.StateRoot, "repo", "runtime", "appserver-notices")
	name := msg.ID + ".json"
	data, err := os.ReadFile(filepath.Join(noticesDir, name))
	if err != nil {
		t.Fatal(err)
	}
	var published appServerNoticeReservation
	if err := json.Unmarshal(data, &published); err != nil || published.OwnerPID != os.Getpid() {
		t.Fatalf("published reservation missing live owner: %+v %v", published, err)
	}
	mail.Store.noticePublish = nil
	resumed, err := mail.Store.ReserveAppServerNotice(ctx, worker, msg)
	if err != nil || resumed != published {
		t.Fatalf("public retry lost published attempt ID: %+v %v", resumed, err)
	}
	root, err := os.OpenRoot(noticesDir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	var strict appServerNoticeReservation
	if err := readJSON(root, name, &strict); err != nil || strict != resumed {
		t.Fatalf("retry did not restore strict single-link record: %+v %v", strict, err)
	}
	if err := mail.Store.MarkAppServerNoticeRPCStarted(ctx, worker, msg, resumed.AttemptID); err != nil {
		t.Fatal(err)
	}
	if _, err := mail.Store.ReserveAppServerNotice(ctx, worker, msg); !errors.Is(err, ErrBusy) {
		t.Fatalf("rpc_started attempt replayed: %v", err)
	}
}
