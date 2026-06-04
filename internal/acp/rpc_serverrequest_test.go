package acp

import (
	"bufio"
	"encoding/json"
	"io"
	"testing"
	"time"
)

// newPipeConn wires a Conn to two pipes so a test can act as the ACP agent:
// agentWrite feeds bytes into the Conn's stdout (what the agent "sends"), and
// replyRead receives what the Conn writes to stdin (its replies).
func newPipeConn() (c *Conn, agentWrite *io.PipeWriter, replyRead *bufio.Scanner) {
	prAgent, pwAgent := io.Pipe()
	prReply, pwReply := io.Pipe()
	c = &Conn{
		Name:     "test",
		stdin:    pwReply,
		stdout:   bufio.NewScanner(prAgent),
		pending:  make(map[int64]chan json.RawMessage),
		NotifyCh: make(chan Notification, 64),
	}
	return c, pwAgent, bufio.NewScanner(prReply)
}

// A1: an agent-initiated request (id + method) must be answered, not dropped.
// session/request_permission is declined with a "cancelled" outcome so the agent
// is unblocked without granting tool permission.
func TestServerRequestPermissionIsDeclined(t *testing.T) {
	c, agentWrite, replyRead := newPipeConn()
	go c.readLoop()

	go func() {
		_, _ = agentWrite.Write([]byte(`{"jsonrpc":"2.0","id":7,"method":"session/request_permission","params":{}}` + "\n"))
	}()

	done := make(chan []byte, 1)
	go func() {
		if replyRead.Scan() {
			done <- replyRead.Bytes()
		}
	}()

	select {
	case line := <-done:
		var reply struct {
			ID     int64 `json:"id"`
			Result struct {
				Outcome struct {
					Outcome string `json:"outcome"`
				} `json:"outcome"`
			} `json:"result"`
		}
		if err := json.Unmarshal(line, &reply); err != nil {
			t.Fatalf("reply not valid JSON: %v (%s)", err, line)
		}
		if reply.ID != 7 {
			t.Fatalf("reply id = %d, want 7", reply.ID)
		}
		if reply.Result.Outcome.Outcome != "cancelled" {
			t.Fatalf("permission outcome = %q, want cancelled (%s)", reply.Result.Outcome.Outcome, line)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out: agent request was dropped, not answered (regression of A1)")
	}
	_ = agentWrite.Close()
}

// A1: unsupported agent requests (fs/*, terminal/*) get a definitive JSON-RPC
// error so the agent fails fast instead of hanging.
func TestServerRequestUnsupportedReturnsError(t *testing.T) {
	c, agentWrite, replyRead := newPipeConn()
	go c.readLoop()

	go func() {
		_, _ = agentWrite.Write([]byte(`{"jsonrpc":"2.0","id":9,"method":"fs/read_text_file","params":{}}` + "\n"))
	}()

	done := make(chan []byte, 1)
	go func() {
		if replyRead.Scan() {
			done <- replyRead.Bytes()
		}
	}()

	select {
	case line := <-done:
		var reply struct {
			ID    int64 `json:"id"`
			Error *struct {
				Code int `json:"code"`
			} `json:"error"`
		}
		if err := json.Unmarshal(line, &reply); err != nil {
			t.Fatalf("reply not valid JSON: %v (%s)", err, line)
		}
		if reply.ID != 9 || reply.Error == nil {
			t.Fatalf("expected error reply for id 9, got %s", line)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out: unsupported request was dropped (regression of A1)")
	}
	_ = agentWrite.Close()
}
