package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mingzhi1/coden/internal/api"
	"github.com/mingzhi1/coden/internal/core/model"
	"github.com/mingzhi1/coden/internal/launcher"
	"github.com/mingzhi1/coden/internal/ui/tui"
)

type plainTestClient struct {
	subscribeEvents <-chan model.Event
	subscribeErr    error
	workflowID      string
	submitErr       error
	checkpoint      model.CheckpointResult
	checkpointErr   error
	createdSession  model.Session
	createErr       error
	sessions        []model.Session
	listSessionsErr error
	attachSessionID string
}

func (c *plainTestClient) Attach(_ context.Context, sessionID, _, _ string) error {
	c.attachSessionID = sessionID
	return nil
}
func (c plainTestClient) Detach(context.Context, string, string) error { return nil }
func (c plainTestClient) CreateSession(context.Context, string) (model.Session, error) {
	return c.createdSession, c.createErr
}
func (c plainTestClient) ListSessions(context.Context, int) ([]model.Session, error) {
	return c.sessions, c.listSessionsErr
}
func (c plainTestClient) Submit(context.Context, string, string) (string, error) {
	return c.workflowID, c.submitErr
}
func (c plainTestClient) CancelWorkflow(context.Context, string, string) error { return nil }
func (c plainTestClient) GetWorkflowRun(context.Context, string, string) (model.WorkflowRun, error) {
	return model.WorkflowRun{}, fmt.Errorf("not implemented")
}
func (c plainTestClient) ListWorkflowRuns(context.Context, string, int) ([]model.WorkflowRun, error) {
	return nil, fmt.Errorf("not implemented")
}
func (c plainTestClient) ListWorkflowRunObjects(context.Context, string, string) ([]model.Object, error) {
	return nil, fmt.Errorf("not implemented")
}
func (c plainTestClient) ReadWorkflowRunObject(context.Context, string, string, string) (json.RawMessage, error) {
	return nil, fmt.Errorf("not implemented")
}
func (c plainTestClient) ListMessages(context.Context, string, int) ([]model.Message, error) {
	return nil, fmt.Errorf("not implemented")
}
func (c plainTestClient) GetLatestIntent(context.Context, string) (model.IntentSpec, error) {
	return model.IntentSpec{}, fmt.Errorf("not implemented")
}
func (c plainTestClient) WorkspaceChanges(context.Context, string) ([]model.WorkspaceChangedPayload, error) {
	return nil, fmt.Errorf("not implemented")
}
func (c plainTestClient) WorkspaceRead(context.Context, string, string) ([]byte, error) {
	return nil, fmt.Errorf("not implemented")
}
func (c plainTestClient) WorkspaceWrite(context.Context, string, string, []byte) (string, error) {
	return "", fmt.Errorf("not implemented")
}
func (c plainTestClient) GetCheckpoint(context.Context, string, string) (model.CheckpointResult, error) {
	return c.checkpoint, c.checkpointErr
}
func (c plainTestClient) ListCheckpoints(context.Context, string, int) ([]model.CheckpointResult, error) {
	return nil, fmt.Errorf("not implemented")
}
func (c plainTestClient) GetWorkflowWorkers(context.Context, string, string) ([]model.WorkerState, error) {
	return nil, fmt.Errorf("not implemented")
}
func (c plainTestClient) Subscribe(context.Context, string) (<-chan model.Event, func(), error) {
	return c.subscribeEvents, func() {}, c.subscribeErr
}
func (c plainTestClient) SessionSnapshot(context.Context, string, int) (model.SessionSnapshot, error) {
	return model.SessionSnapshot{}, fmt.Errorf("not implemented")
}
func (c plainTestClient) SkipTask(context.Context, string, string) error {
	return fmt.Errorf("not implemented")
}
func (c plainTestClient) UndoTask(context.Context, string) (string, error) {
	return "", fmt.Errorf("not implemented")
}
func (c plainTestClient) RenameSession(_ context.Context, _, _ string) (model.Session, error) {
	return model.Session{}, fmt.Errorf("not implemented")
}

func TestNewLocalRPCClientCanSubmit(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	opts := launcher.DefaultOptions(moduleRoot(), t.TempDir())
	opts.Input = "loopback"
	opts.Planner = "loopback"
	opts.Coder = "loopback"
	opts.Acceptor = "loopback"
	opts.Executor = "loopback"

	root := t.TempDir()
	client, cleanup, err := newLocalRPCClient(ctx, root, testStateDBPath(root), opts, launcher.Default())
	if err != nil {
		t.Fatalf("newLocalRPCClient failed: %v", err)
	}
	defer cleanup()

	events, cancelSub, err := client.Subscribe(ctx, "test-session")
	if err != nil {
		t.Fatalf("subscribe failed: %v", err)
	}
	defer cancelSub()

	workflowID, err := client.Submit(ctx, "test-session", "hello from local rpc")
	if err != nil {
		t.Fatalf("submit failed: %v", err)
	}
	if workflowID == "" {
		t.Fatal("expected workflow id")
	}

	if err := waitForCheckpointEvent(events, workflowID); err != nil {
		t.Fatalf("wait for checkpoint event failed: %v", err)
	}

	got, err := waitForCheckpoint(ctx, client, "test-session", workflowID)
	if err != nil {
		t.Fatalf("get checkpoint failed: %v", err)
	}
	if got.WorkflowID != workflowID {
		t.Fatalf("unexpected workflow id: %q", got.WorkflowID)
	}
}

func TestNewLocalRPCClientCanQueryCheckpoints(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	opts := launcher.DefaultOptions(moduleRoot(), root)
	opts.Input = "loopback"
	opts.Planner = "loopback"
	opts.Coder = "loopback"
	opts.Acceptor = "loopback"
	opts.Executor = "loopback"

	client, cleanup, err := newLocalRPCClient(ctx, root, testStateDBPath(root), opts, launcher.Default())
	if err != nil {
		t.Fatalf("newLocalRPCClient failed: %v", err)
	}
	defer cleanup()

	events, cancelSub, err := client.Subscribe(ctx, "test-session")
	if err != nil {
		t.Fatalf("subscribe failed: %v", err)
	}
	defer cancelSub()

	workflowID, err := client.Submit(ctx, "test-session", "hello from local rpc")
	if err != nil {
		t.Fatalf("submit failed: %v", err)
	}

	if err := waitForCheckpointEvent(events, workflowID); err != nil {
		t.Fatalf("wait for checkpoint event failed: %v", err)
	}

	got, err := waitForCheckpoint(ctx, client, "test-session", workflowID)
	if err != nil {
		t.Fatalf("get checkpoint failed: %v", err)
	}
	if got.WorkflowID != workflowID {
		t.Fatalf("unexpected workflow id: %q", got.WorkflowID)
	}

	list, err := client.ListCheckpoints(ctx, "test-session", 10)
	if err != nil {
		t.Fatalf("list checkpoints failed: %v", err)
	}
	if len(list) == 0 {
		t.Fatal("expected persisted checkpoints")
	}
}

func TestNewLocalRPCClientWithProcessExecutorWritesWorkspaceFile(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	opts := launcher.DefaultOptions(moduleRoot(), root)
	opts.Input = "loopback"
	opts.Planner = "loopback"
	opts.Coder = "loopback"
	opts.Acceptor = "loopback"
	opts.Executor = "process"

	client, cleanup, err := newLocalRPCClient(ctx, root, testStateDBPath(root), opts, launcher.Default())
	if err != nil {
		t.Fatalf("newLocalRPCClient failed: %v", err)
	}
	defer cleanup()

	events, cancelSub, err := client.Subscribe(ctx, "test-session")
	if err != nil {
		t.Fatalf("subscribe failed: %v", err)
	}
	defer cancelSub()

	workflowID, err := client.Submit(ctx, "test-session", "write through process executor")
	if err != nil {
		t.Fatalf("submit failed: %v", err)
	}
	if err := waitForCheckpointEvent(events, workflowID); err != nil {
		t.Fatalf("wait for checkpoint event failed: %v", err)
	}

	changes, err := client.WorkspaceChanges(ctx, "test-session")
	if err != nil {
		t.Fatalf("workspace changes failed: %v", err)
	}
	if len(changes) == 0 {
		t.Fatal("expected workspace changes")
	}

	artifactPath := changes[len(changes)-1].Path
	if artifactPath == "" {
		t.Fatal("expected artifact path")
	}
	if !filepath.IsAbs(artifactPath) {
		artifactPath = filepath.Join(root, artifactPath)
	}
	if _, err := os.Stat(artifactPath); err != nil {
		t.Fatalf("expected artifact written to workspace: %v", err)
	}
}

func TestModuleRootExists(t *testing.T) {
	t.Parallel()

	root := moduleRoot()
	if root == "" {
		t.Fatal("expected module root")
	}
}

func TestDependencyOptions(t *testing.T) {
	t.Parallel()

	// Empty strings mean "use auto-detected defaults from DefaultOptions".
	opts := dependencyOptions("workspace", "", "", "", "", "")
	if opts.WorkspaceRoot != "workspace" {
		t.Fatalf("unexpected workspace root: %q", opts.WorkspaceRoot)
	}
	// Without API keys, DefaultOptions returns "loopback" for workers and "process" for executor.
	if opts.Input != "loopback" || opts.Planner != "loopback" || opts.Coder != "loopback" || opts.Acceptor != "loopback" || opts.Executor != "process" {
		t.Fatalf("unexpected auto-detected options: %+v", opts)
	}

	// Explicit overrides should take effect.
	opts2 := dependencyOptions("workspace", "llm", "llm", "process", "loopback", "loopback")
	if opts2.Input != "llm" || opts2.Planner != "llm" || opts2.Coder != "process" || opts2.Acceptor != "loopback" || opts2.Executor != "loopback" {
		t.Fatalf("unexpected overridden options: %+v", opts2)
	}

	if opts.ModuleRoot == "" {
		t.Fatal("expected module root")
	}
	if opts.AllowShell {
		t.Fatal("expected allow shell to default false")
	}
}

func TestRunServeReturnsListenError(t *testing.T) {
	t.Parallel()

	opts := launcher.DefaultOptions(moduleRoot(), t.TempDir())
	opts.Input = "loopback"
	opts.Planner = "loopback"
	opts.Coder = "loopback"
	opts.Acceptor = "loopback"
	opts.Executor = "loopback"

	root := t.TempDir()
	if err := runServe("127.0.0.1:bad", "", root, testStateDBPath(root), opts, launcher.Default()); err == nil {
		t.Fatal("expected listen error")
	}
}

func TestServeListenerStopsOnContextCancel(t *testing.T) {
	t.Parallel()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen failed: %v", err)
	}
	defer ln.Close()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- serveListener(ctx, ln, 0, func(rwc io.ReadWriteCloser) {
			_ = rwc.Close()
		})
	}()

	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("serveListener returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("serveListener did not exit after context cancel")
	}
}

func TestServeListenerWaitsForActiveConnectionsOnShutdown(t *testing.T) {
	t.Parallel()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen failed: %v", err)
	}
	defer ln.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	started := make(chan struct{}, 1)
	released := make(chan struct{})
	var served int32

	done := make(chan error, 1)
	go func() {
		done <- serveListener(ctx, ln, 0, func(rwc io.ReadWriteCloser) {
			defer rwc.Close()
			atomic.AddInt32(&served, 1)
			started <- struct{}{}
			<-released
		})
	}()

	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}
	defer conn.Close()

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("connection was not served")
	}

	cancel()

	select {
	case err := <-done:
		t.Fatalf("serveListener exited before active connection finished: %v", err)
	case <-time.After(100 * time.Millisecond):
	}

	close(released)

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("serveListener returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("serveListener did not wait for active connection shutdown")
	}

	if got := atomic.LoadInt32(&served); got != 1 {
		t.Fatalf("expected 1 served connection, got %d", got)
	}
}

func TestRunHelpShowsCurrentMvpLimitation(t *testing.T) {
	t.Parallel()

	fs := flag.NewFlagSet("coden", flag.ContinueOnError)
	output := &bytes.Buffer{}
	renderUsage(output, fs)
	text := output.String()
	for _, want := range []string{
		"Usage: coden",
		"Positional arguments",
		"Options:",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("help output missing %q\n%s", want, text)
		}
	}
}

func TestNewKernelCreatesWorkspaceRoot(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	parent := t.TempDir()
	workspaceRoot := filepath.Join(parent, "missing-workspace")
	if _, err := os.Stat(workspaceRoot); !os.IsNotExist(err) {
		t.Fatalf("expected workspace root to be absent before startup, got err=%v", err)
	}

	opts := launcher.DefaultOptions(moduleRoot(), workspaceRoot)
	opts.Input = "loopback"
	opts.Planner = "loopback"
	opts.Coder = "loopback"
	opts.Acceptor = "loopback"
	opts.Executor = "loopback"

	k, cleanup, err := newKernel(ctx, workspaceRoot, testStateDBPath(parent), opts, launcher.Default())
	if err != nil {
		t.Fatalf("newKernel failed: %v", err)
	}
	defer cleanup()
	_ = k

	info, err := os.Stat(workspaceRoot)
	if err != nil {
		t.Fatalf("expected workspace root to be created: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("expected workspace root to be a directory, got %+v", info.Mode())
	}
}

func TestRunPlainReturnsOnCheckpointUpdatedForWorkflow(t *testing.T) {
	events := make(chan model.Event, 2)
	events <- model.Event{
		Seq:       1,
		SessionID: "test-session",
		Topic:     model.EventCheckpointUpdated,
		Timestamp: time.Now(),
		Payload: model.EncodePayload(model.CheckpointUpdatedPayload{
			WorkflowID: "wf-1",
			Status:     "pass",
		}),
	}
	close(events)

	client := plainTestClient{
		subscribeEvents: events,
		workflowID:      "wf-1",
		checkpoint: model.CheckpointResult{
			WorkflowID:    "wf-1",
			SessionID:     "test-session",
			Status:        "pass",
			ArtifactPaths: []string{"artifacts/test.md"},
			Evidence:      []string{"ok"},
		},
	}

	restore := redirectStdout(t)
	defer restore()

	if err := runPlain(context.Background(), &client, "test-session", "hello"); err != nil {
		t.Fatalf("runPlain failed: %v", err)
	}
}

func TestRunPlainReturnsCanceledError(t *testing.T) {
	events := make(chan model.Event, 1)
	events <- model.Event{
		Seq:       1,
		SessionID: "test-session",
		Topic:     model.EventWorkflowCanceled,
		Timestamp: time.Now(),
		Payload: model.EncodePayload(model.WorkflowCanceledPayload{
			WorkflowID: "wf-1",
			Reason:     "canceled",
		}),
	}
	close(events)

	client := plainTestClient{
		subscribeEvents: events,
		workflowID:      "wf-1",
	}

	restore := redirectStdout(t)
	defer restore()

	if err := runPlain(context.Background(), &client, "test-session", "hello"); err == nil {
		t.Fatal("expected canceled error")
	}
}

func TestRunPlainTimesOutWithoutTerminalEvent(t *testing.T) {
	events := make(chan model.Event)
	client := plainTestClient{
		subscribeEvents: events,
		workflowID:      "wf-1",
	}

	restore := redirectStdout(t)
	defer restore()

	oldTimeout := plainIdleTimeout
	plainIdleTimeout = 20 * time.Millisecond
	defer func() { plainIdleTimeout = oldTimeout }()

	if err := runPlain(context.Background(), &client, "test-session", "hello"); err == nil {
		t.Fatal("expected timeout error")
	}
}

func TestRunPlainRequiresPrompt(t *testing.T) {
	client := plainTestClient{}

	if err := runPlain(context.Background(), &client, "test-session", ""); err == nil {
		t.Fatal("expected empty prompt error")
	}
}

func TestRunClientSessionListsSessions(t *testing.T) {
	t.Parallel()

	client := plainTestClient{
		sessions: []model.Session{
			{ID: "sess-1", ProjectID: "proj-1", ProjectRoot: "/tmp/proj-1", CreatedAt: time.Date(2026, 3, 27, 8, 0, 0, 0, time.UTC)},
		},
	}

	restore := redirectStdout(t)
	defer restore()

	if err := runClientSession(context.Background(), &client, "ignored", "", true, checkpointQuery{}, sessionQuery{List: true}, false, tui.RuntimeInfo{}); err != nil {
		t.Fatalf("runClientSession failed: %v", err)
	}
	if client.attachSessionID != "" {
		t.Fatalf("expected no attach for session list query, got %q", client.attachSessionID)
	}
}

func TestRunClientSessionNewSessionOverridesSessionID(t *testing.T) {
	t.Parallel()

	events := make(chan model.Event, 1)
	events <- model.Event{
		Seq:       1,
		SessionID: "sess-new",
		Topic:     model.EventCheckpointUpdated,
		Timestamp: time.Now(),
		Payload: model.EncodePayload(model.CheckpointUpdatedPayload{
			WorkflowID: "wf-1",
			Status:     "pass",
		}),
	}
	close(events)

	client := plainTestClient{
		subscribeEvents: events,
		workflowID:      "wf-1",
		createdSession: model.Session{
			ID: "sess-new",
		},
		checkpoint: model.CheckpointResult{
			WorkflowID:    "wf-1",
			SessionID:     "sess-new",
			Status:        "pass",
			ArtifactPaths: []string{"artifacts/test.md"},
			Evidence:      []string{"ok"},
		},
	}

	restore := redirectStdout(t)
	defer restore()

	if err := runClientSession(context.Background(), &client, "demo-session", "hello", true, checkpointQuery{}, sessionQuery{}, true, tui.RuntimeInfo{}); err != nil {
		t.Fatalf("runClientSession failed: %v", err)
	}
	if client.attachSessionID != "sess-new" {
		t.Fatalf("expected attach to created session, got %q", client.attachSessionID)
	}
}

func testStateDBPath(root string) string {
	return filepath.Join(root, ".coden", "main.sqlite")
}

func waitForCheckpoint(ctx context.Context, client api.ClientAPI, sessionID, workflowID string) (model.CheckpointResult, error) {
	deadline := time.Now().Add(10 * time.Second)
	for {
		result, err := client.GetCheckpoint(ctx, sessionID, workflowID)
		if err == nil {
			return result, nil
		}
		if time.Now().After(deadline) {
			return model.CheckpointResult{}, err
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func waitForCheckpointEvent(events <-chan model.Event, workflowID string) error {
	timeout := time.After(10 * time.Second)
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				return context.DeadlineExceeded
			}
			if ev.Topic != model.EventCheckpointUpdated {
				continue
			}
			payload, err := model.DecodePayload[model.CheckpointUpdatedPayload](ev)
			if err != nil {
				return err
			}
			if payload.WorkflowID == workflowID {
				return nil
			}
		case <-timeout:
			return context.DeadlineExceeded
		}
	}
}

func redirectStdout(t *testing.T) func() {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe failed: %v", err)
	}
	os.Stdout = w
	return func() {
		_ = w.Close()
		_ = r.Close()
		os.Stdout = old
	}
}
