package launcher

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/mingzhi1/coden/internal/rpc/protocol"
	"github.com/mingzhi1/coden/internal/rpc/transport"

	"context"
)

// waitReady pings the sidecar until it responds, with exponential backoff.
func (s *Sidecar) waitReady(ctx context.Context) error {
	const maxAttempts = 10
	delay := 200 * time.Millisecond

	for i := 0; i < maxAttempts; i++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}

		if err := s.ping(); err == nil {
			return nil
		}

		// Exponential backoff: 200ms, 400ms, 800ms, ...
		delay = delay * 2
		if delay > 3*time.Second {
			delay = 3 * time.Second
		}
	}
	return fmt.Errorf("sidecar at %s did not respond after %d attempts", s.addr, maxAttempts)
}

// ping sends a JSON-RPC ping to the sidecar and checks for "pong".
func (s *Sidecar) ping() error {
	conn, err := net.DialTimeout("tcp", s.addr, 2*time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()

	codec := transport.NewCodec(conn)

	req, err := protocol.NewRequest(1, protocol.MethodPing, nil)
	if err != nil {
		return err
	}
	if err := codec.WriteMessage(req); err != nil {
		return err
	}

	raw, err := codec.ReadMessage()
	if err != nil {
		return err
	}

	var resp protocol.Response
	if err := json.Unmarshal(raw, &resp); err != nil {
		return err
	}
	if resp.Error != nil {
		return fmt.Errorf("ping error: %s", resp.Error.Message)
	}
	return nil
}

// findSidecarBinary looks for the server binary:
// 1. Next to the current executable
// 2. In system PATH
func findSidecarBinary(name string) string {
	// Try next to current executable.
	if exe, err := os.Executable(); err == nil {
		candidate := filepath.Join(filepath.Dir(exe), name)
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}

	// Try PATH.
	if p, err := exec.LookPath(name); err == nil {
		return p
	}

	return ""
}
