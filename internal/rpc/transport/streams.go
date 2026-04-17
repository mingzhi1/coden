package transport

import (
	"io"
	"net"
	"os"
	"time"
)

// StdioStream wraps os.Stdin/Stdout into a ReadWriteCloser.
type StdioStream struct {
	in  io.ReadCloser
	out io.WriteCloser
}

// Stdio returns a ReadWriteCloser backed by stdin/stdout.
func Stdio() io.ReadWriteCloser {
	return &StdioStream{in: os.Stdin, out: os.Stdout}
}

func (s *StdioStream) Read(p []byte) (int, error)  { return s.in.Read(p) }
func (s *StdioStream) Write(p []byte) (int, error) { return s.out.Write(p) }
func (s *StdioStream) Close() error {
	s.in.Close()
	return s.out.Close()
}

// ListenTCP starts a TCP listener and returns a function that
// accepts one connection. The caller is responsible for closing
// the listener when done.
func ListenTCP(addr string) (net.Listener, error) {
	return net.Listen("tcp", addr)
}

// DialTCP connects to a TCP address and returns the connection
// as a ReadWriteCloser suitable for NewCodec.
func DialTCP(addr string) (io.ReadWriteCloser, error) {
	return net.Dial("tcp", addr)
}

// DialTCPKeepalive dials a TCP connection and enables OS-level keepalive
// probes with the given period. Use this for long-lived RPC connections
// (subscription streams) to detect dead peers quickly (R-08).
func DialTCPKeepalive(addr string, period time.Duration) (io.ReadWriteCloser, error) {
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		return nil, err
	}
	return setTCPKeepalive(conn, period)
}

// AcceptKeepalive accepts one connection from the listener and enables
// OS-level TCP keepalive probes with the given period (R-08).
func AcceptKeepalive(ln net.Listener, period time.Duration) (io.ReadWriteCloser, error) {
	conn, err := ln.Accept()
	if err != nil {
		return nil, err
	}
	return setTCPKeepalive(conn, period)
}

func setTCPKeepalive(conn net.Conn, period time.Duration) (io.ReadWriteCloser, error) {
	tcp, ok := conn.(*net.TCPConn)
	if !ok {
		return conn.(io.ReadWriteCloser), nil
	}
	if err := tcp.SetKeepAlive(true); err != nil {
		conn.Close()
		return nil, err
	}
	if period > 0 {
		if err := tcp.SetKeepAlivePeriod(period); err != nil {
			conn.Close()
			return nil, err
		}
	}
	return tcp, nil
}

// Pipe creates a connected pair of ReadWriteClosers for testing.
// Data written to one end can be read from the other.
func Pipe() (io.ReadWriteCloser, io.ReadWriteCloser) {
	sr, cw := io.Pipe() // server reads, client writes
	cr, sw := io.Pipe() // client reads, server writes

	server := &pipeRWC{reader: sr, writer: sw}
	client := &pipeRWC{reader: cr, writer: cw}
	return server, client
}

type pipeRWC struct {
	reader *io.PipeReader
	writer *io.PipeWriter
}

func (p *pipeRWC) Read(b []byte) (int, error)  { return p.reader.Read(b) }
func (p *pipeRWC) Write(b []byte) (int, error) { return p.writer.Write(b) }
func (p *pipeRWC) Close() error {
	p.reader.Close()
	return p.writer.Close()
}
