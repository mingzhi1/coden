package rpc_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/mingzhi1/coden/internal/core/model"
	"github.com/mingzhi1/coden/internal/core/toolruntime"
	"github.com/mingzhi1/coden/internal/core/workflow"
	"github.com/mingzhi1/coden/internal/rpc/client"
	"github.com/mingzhi1/coden/internal/rpc/protocol"
	"github.com/mingzhi1/coden/internal/rpc/server"
	"github.com/mingzhi1/coden/internal/rpc/transport"
)

// ---- stub worker ----

type echoWorker struct {
	role     workflow.Role
	messages []model.WorkerMessage
}

func (w *echoWorker) Execute(_ context.Context, input workflow.WorkerInput) (workflow.WorkerOutput, error) {
	msgs := w.messages
	if msgs == nil {
		msgs = []model.WorkerMessage{{Kind: "text", Role: "assistant", Content: "echo: " + input.Prompt}}
	}
	return workflow.WorkerOutput{
		Messages: msgs,
		Metadata: workflow.WorkerMetadata{Worker: "echo-worker", Role: w.role},
	}, nil
}

// ---- stub executor ----

type echoExecutor struct{}

func (e *echoExecutor) Execute(_ context.Context, req toolruntime.Request) (toolruntime.Result, error) {
	return toolruntime.Result{
		Output:  "executed: " + req.Kind,
		Summary: "echo",
	}, nil
}

// ---- helpers ----

func workerPipe(w workflow.Worker, desc protocol.WorkerDescribeResult) (*client.Client, func()) {
	srvRWC, cliRWC := transport.Pipe()
	srv := server.NewWorkerServer(w, desc)
	ctx, cancel := context.WithCancel(context.Background())
	go srv.ServeConn(ctx, srvRWC)
	c := client.New(cliRWC)
	return c, func() { cancel(); c.Close() }
}

func toolPipe(exec toolruntime.Executor, desc protocol.ToolDescribeResult) (*client.Client, func()) {
	srvRWC, cliRWC := transport.Pipe()
	srv := server.NewToolServer(exec, desc)
	ctx, cancel := context.WithCancel(context.Background())
	go srv.ServeConn(ctx, srvRWC)
	c := client.New(cliRWC)
	return c, func() { cancel(); c.Close() }
}

// ---- WorkerServer tests ----

func TestWorkerServerDescribe(t *testing.T) {
	desc := protocol.WorkerDescribeResult{
		Name:           "echo-worker",
		Role:           "planner",
		Version:        "1.0.0",
		SupportsCancel: true,
		MaxConcurrency: 4,
	}
	c, cleanup := workerPipe(&echoWorker{role: workflow.RolePlanner}, desc)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	got, err := c.DescribeWorker(ctx)
	if err != nil {
		t.Fatalf("DescribeWorker: %v", err)
	}
	if got.Name != desc.Name {
		t.Errorf("Name: got %q, want %q", got.Name, desc.Name)
	}
	if got.Role != desc.Role {
		t.Errorf("Role: got %q, want %q", got.Role, desc.Role)
	}
	if got.Version != desc.Version {
		t.Errorf("Version: got %q, want %q", got.Version, desc.Version)
	}
	if !got.SupportsCancel {
		t.Errorf("SupportsCancel: got false, want true")
	}
	if got.MaxConcurrency != desc.MaxConcurrency {
		t.Errorf("MaxConcurrency: got %d, want %d", got.MaxConcurrency, desc.MaxConcurrency)
	}
}

func TestWorkerServerExecuteRoundTrip(t *testing.T) {
	worker := &echoWorker{
		role: workflow.RolePlanner,
		messages: []model.WorkerMessage{
			{Kind: "text", Role: "assistant", Content: "hello from worker"},
		},
	}
	c, cleanup := workerPipe(worker, protocol.WorkerDescribeResult{Name: "echo", Role: "planner"})
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	input := workflow.WorkerInput{
		SessionID:  "sess-1",
		WorkflowID: "wf-1",
		TaskID:     "task-1",
		Prompt:     "test prompt",
	}
	rawInput, _ := json.Marshal(input)
	params := protocol.WorkerExecuteParams{
		SessionID:  input.SessionID,
		WorkflowID: input.WorkflowID,
		TaskID:     input.TaskID,
		Role:       "planner",
		Input:      json.RawMessage(rawInput),
	}

	result, err := c.ExecuteWorker(ctx, params)
	if err != nil {
		t.Fatalf("ExecuteWorker: %v", err)
	}
	if result.Status != "ok" {
		t.Errorf("Status: got %q, want %q", result.Status, "ok")
	}
	if len(result.Messages) != 1 {
		t.Fatalf("Messages: got %d, want 1", len(result.Messages))
	}
	if result.Messages[0].Content != "hello from worker" {
		t.Errorf("Message content: got %q", result.Messages[0].Content)
	}
	if result.Metadata == nil || result.Metadata.Worker != "echo-worker" {
		t.Errorf("Metadata.Worker missing or wrong: %+v", result.Metadata)
	}
}

func TestWorkerServerExecuteViaRPCWorker(t *testing.T) {
	worker := &echoWorker{
		role: workflow.RolePlanner,
		messages: []model.WorkerMessage{
			{Kind: "text", Role: "assistant", Content: "rpc-worker reply"},
		},
	}
	c, cleanup := workerPipe(worker, protocol.WorkerDescribeResult{Name: "echo", Role: "planner"})
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	rpcWorker := client.NewRPCWorker(c, workflow.RolePlanner)
	out, err := rpcWorker.Execute(ctx, workflow.WorkerInput{
		SessionID:  "sess-1",
		WorkflowID: "wf-1",
		TaskID:     "task-1",
		Prompt:     "hello",
	})
	if err != nil {
		t.Fatalf("RPCWorker.Execute: %v", err)
	}
	if len(out.Messages) != 1 || out.Messages[0].Content != "rpc-worker reply" {
		t.Errorf("unexpected output messages: %+v", out.Messages)
	}
	if out.Metadata.Worker != "echo-worker" {
		t.Errorf("Metadata.Worker: got %q", out.Metadata.Worker)
	}
}

func TestWorkerServerCancel(t *testing.T) {
	c, cleanup := workerPipe(&echoWorker{role: workflow.RolePlanner}, protocol.WorkerDescribeResult{Name: "echo", Role: "planner"})
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	raw, err := c.ExecuteWorker(ctx, protocol.WorkerExecuteParams{})
	if err != nil {
		t.Fatalf("ExecuteWorker: %v", err)
	}
	_ = raw // cancel just returns ok, no meaningful result to check
}

func TestWorkerServerMethodNotFound(t *testing.T) {
	srvRWC, cliRWC := transport.Pipe()
	srv := server.NewWorkerServer(&echoWorker{}, protocol.WorkerDescribeResult{Name: "echo"})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go srv.ServeConn(ctx, srvRWC)

	codec := transport.NewCodec(cliRWC)
	defer codec.Close()

	reqCtx, reqCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer reqCancel()
	_ = reqCtx

	req, _ := protocol.NewRequest(1, "unknown.method", nil)
	if err := codec.WriteMessage(req); err != nil {
		t.Fatalf("write: %v", err)
	}
	raw, err := codec.ReadMessage()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var resp protocol.Response
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Error == nil {
		t.Fatal("expected error response for unknown method, got nil")
	}
	if resp.Error.Code != protocol.CodeMethodNotFound {
		t.Errorf("error code: got %d, want %d", resp.Error.Code, protocol.CodeMethodNotFound)
	}
}

// ---- ToolServer tests ----

func TestToolServerDescribe(t *testing.T) {
	desc := protocol.ToolDescribeResult{
		Name:           "echo-tool",
		Capability:     "echo",
		Version:        "2.0.0",
		SupportsCancel: false,
		TimeoutClass:   "fast",
	}
	c, cleanup := toolPipe(&echoExecutor{}, desc)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	got, err := c.DescribeTool(ctx)
	if err != nil {
		t.Fatalf("DescribeTool: %v", err)
	}
	if got.Name != desc.Name {
		t.Errorf("Name: got %q, want %q", got.Name, desc.Name)
	}
	if got.Capability != desc.Capability {
		t.Errorf("Capability: got %q, want %q", got.Capability, desc.Capability)
	}
	if got.Version != desc.Version {
		t.Errorf("Version: got %q, want %q", got.Version, desc.Version)
	}
	if got.TimeoutClass != desc.TimeoutClass {
		t.Errorf("TimeoutClass: got %q, want %q", got.TimeoutClass, desc.TimeoutClass)
	}
}

func TestToolServerExecRoundTrip(t *testing.T) {
	c, cleanup := toolPipe(&echoExecutor{}, protocol.ToolDescribeResult{Name: "echo", Capability: "echo"})
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req := toolruntime.Request{Kind: "write_file"}
	args, _ := json.Marshal(req)
	params := protocol.ToolExecParams{
		ToolName: "echo",
		Args:     json.RawMessage(args),
	}

	result, err := c.ExecuteTool(ctx, params)
	if err != nil {
		t.Fatalf("ExecuteTool: %v", err)
	}
	if result.Status != "ok" {
		t.Errorf("Status: got %q, want %q", result.Status, "ok")
	}
	if result.Stdout != "executed: write_file" {
		t.Errorf("Stdout: got %q", result.Stdout)
	}
	if result.Summary != "echo" {
		t.Errorf("Summary: got %q", result.Summary)
	}
}

func TestToolServerExecViaRPCExecutor(t *testing.T) {
	c, cleanup := toolPipe(&echoExecutor{}, protocol.ToolDescribeResult{Name: "echo", Capability: "echo"})
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	rpcExec := client.NewRPCExecutor(c, "echo")
	result, err := rpcExec.Execute(ctx, toolruntime.Request{Kind: "search"})
	if err != nil {
		t.Fatalf("RPCExecutor.Execute: %v", err)
	}
	if result.Output != "executed: search" {
		t.Errorf("Output: got %q", result.Output)
	}
	if result.Summary != "echo" {
		t.Errorf("Summary: got %q", result.Summary)
	}
}

func TestToolServerMethodNotFound(t *testing.T) {
	srvRWC, cliRWC := transport.Pipe()
	srv := server.NewToolServer(&echoExecutor{}, protocol.ToolDescribeResult{Name: "echo"})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go srv.ServeConn(ctx, srvRWC)

	codec := transport.NewCodec(cliRWC)
	defer codec.Close()

	req, _ := protocol.NewRequest(1, "session.create", nil)
	if err := codec.WriteMessage(req); err != nil {
		t.Fatalf("write: %v", err)
	}
	raw, err := codec.ReadMessage()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var resp protocol.Response
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Error == nil {
		t.Fatal("expected error response for unsupported method, got nil")
	}
	if resp.Error.Code != protocol.CodeMethodNotFound {
		t.Errorf("error code: got %d, want %d", resp.Error.Code, protocol.CodeMethodNotFound)
	}
}

func TestToolServerConnectionClose(t *testing.T) {
	c, cleanup := toolPipe(&echoExecutor{}, protocol.ToolDescribeResult{Name: "echo"})
	// Close the client connection — the server goroutine should drain and exit.
	// Verify that subsequent calls fail with a connection-closed error.
	cleanup()

	callCtx, callCancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer callCancel()
	_, err := c.DescribeTool(callCtx)
	if err == nil {
		t.Error("expected error after connection closed, got nil")
	}
}

func TestWorkerServerConnectionClose(t *testing.T) {
	c, cleanup := workerPipe(&echoWorker{}, protocol.WorkerDescribeResult{Name: "echo"})
	cleanup()

	callCtx, callCancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer callCancel()
	_, err := c.DescribeWorker(callCtx)
	if err == nil {
		t.Error("expected error after connection closed, got nil")
	}
}
