package proxy

import (
	"context"
	"errors"
	"log"
	"net"
	"net/http"
	"sync"
)

// Let net/http own the tunneled request lifecycle, including its background
// disconnect read and cancellation while a response is awaiting operator input.
func (s *Server) serveTunnel(connection net.Conn, host string) {
	listener := &tunnelListener{connection: connection, closed: make(chan struct{})}
	server := &http.Server{
		ReadHeaderTimeout: serverReadHeaderTimeout,
		IdleTimeout:       s.cfg.StreamIdleTimeout,
		ConnContext: func(ctx context.Context, conn net.Conn) context.Context {
			return context.WithValue(ctx, clientConnectionKey{}, conn)
		},
		ConnState: func(_ net.Conn, state http.ConnState) {
			if state == http.StateClosed || state == http.StateHijacked {
				_ = listener.Close()
			}
		},
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodConnect {
				http.Error(w, "nested CONNECT is unsupported", http.StatusMethodNotAllowed)
				return
			}
			r.URL.Scheme = "https"
			r.URL.Host = host
			s.handleHTTP(w, r)
		}),
	}
	if err := server.Serve(listener); err != nil && !errors.Is(err, net.ErrClosed) && !errors.Is(err, http.ErrServerClosed) {
		log.Printf("proxy: serve CONNECT tunnel: %v", err)
	}
}

type tunnelListener struct {
	connection net.Conn
	delivered  bool
	closed     chan struct{}
	once       sync.Once
}

func (l *tunnelListener) Accept() (net.Conn, error) {
	if !l.delivered {
		l.delivered = true
		return l.connection, nil
	}
	<-l.closed
	return nil, net.ErrClosed
}

func (l *tunnelListener) Close() error {
	l.once.Do(func() { close(l.closed) })
	return nil
}

func (l *tunnelListener) Addr() net.Addr { return l.connection.LocalAddr() }
