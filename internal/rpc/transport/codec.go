// Package transport provides stream codecs for the JSON-RPC layer.
// Supports stdio (newline-delimited JSON) and TCP transports.
package transport

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"sync"
)

// Codec reads and writes newline-delimited JSON messages over a stream.
// Thread-safe for concurrent writes; reads must be serialized by caller.
type Codec struct {
	scanner *bufio.Scanner
	writer  io.Writer
	closer  io.Closer
	wmu     sync.Mutex
}

// NewCodec wraps a ReadWriteCloser into a newline-delimited JSON codec.
func NewCodec(rwc io.ReadWriteCloser) *Codec {
	scanner := bufio.NewScanner(rwc)
	scanner.Buffer(make([]byte, 0, 1024*1024), 4*1024*1024)
	return &Codec{
		scanner: scanner,
		writer:  rwc,
		closer:  rwc,
	}
}

// ReadMessage reads one JSON message from the stream.
// Returns io.EOF when the stream is closed.
// Blank lines are skipped iteratively to avoid stack overflow
// on streams with many consecutive empty lines.
func (c *Codec) ReadMessage() (json.RawMessage, error) {
	for {
		if !c.scanner.Scan() {
			if err := c.scanner.Err(); err != nil {
				return nil, err
			}
			return nil, io.EOF
		}
		line := c.scanner.Bytes()
		if len(line) == 0 {
			continue // skip blank lines
		}
		msg := make(json.RawMessage, len(line))
		copy(msg, line)
		return msg, nil
	}
}

// WriteMessage writes one JSON value as a single line.
func (c *Codec) WriteMessage(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	data = append(data, '\n')
	_, err = c.writer.Write(data)
	return err
}

// Close closes the underlying stream.
func (c *Codec) Close() error {
	if c.closer != nil {
		return c.closer.Close()
	}
	return nil
}
