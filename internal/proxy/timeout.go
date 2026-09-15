package proxy

import (
	"context"
	"io"
	"net"
	"time"
)

type clientConnectionKey struct{}

func clientConnection(ctx context.Context) net.Conn {
	connection, _ := ctx.Value(clientConnectionKey{}).(net.Conn)
	return connection
}

type idleTimeoutConn struct {
	net.Conn
	timeout time.Duration
}

func withIdleTimeout(connection net.Conn, timeout time.Duration) net.Conn {
	return &idleTimeoutConn{Conn: connection, timeout: timeout}
}

func (c *idleTimeoutConn) Read(buffer []byte) (int, error) {
	if err := c.SetReadDeadline(time.Now().Add(c.timeout)); err != nil {
		return 0, err
	}
	return c.Conn.Read(buffer)
}

func (c *idleTimeoutConn) Write(buffer []byte) (int, error) {
	if err := c.SetWriteDeadline(time.Now().Add(c.timeout)); err != nil {
		return 0, err
	}
	return c.Conn.Write(buffer)
}

type idleTimeoutReadCloser struct {
	io.ReadCloser
	connection net.Conn
	timeout    time.Duration
}

func (r *idleTimeoutReadCloser) Read(buffer []byte) (int, error) {
	if err := r.connection.SetReadDeadline(time.Now().Add(r.timeout)); err != nil {
		return 0, err
	}
	count, err := r.ReadCloser.Read(buffer)
	if err != nil {
		_ = r.connection.SetReadDeadline(time.Time{})
	}
	return count, err
}

func (r *idleTimeoutReadCloser) Close() error {
	_ = r.connection.SetReadDeadline(time.Time{})
	return r.ReadCloser.Close()
}

type idleTimeoutWriter struct {
	io.Writer
	connection net.Conn
	timeout    time.Duration
}

func (w *idleTimeoutWriter) Write(buffer []byte) (int, error) {
	if err := w.connection.SetWriteDeadline(time.Now().Add(w.timeout)); err != nil {
		return 0, err
	}
	return w.Writer.Write(buffer)
}
