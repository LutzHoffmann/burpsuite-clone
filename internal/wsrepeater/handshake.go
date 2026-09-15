package wsrepeater

import (
	"errors"
	"net"
)

const maxHandshakeBytes = 64 << 10

var errHandshakeTooLarge = errors.New("WebSocket handshake headers too large")

// handshakeConn caps HTTP response bytes before http.ReadResponse can allocate
// unbounded header strings. After the empty header line it is a transparent relay.
// The wrapper belongs outside TLS so the bound applies equally to ws and wss.
type handshakeConn struct {
	net.Conn
	remaining int
	lineEnded bool
	done      bool
}

func (c *handshakeConn) Read(p []byte) (int, error) {
	if c.done || len(p) == 0 {
		return c.Conn.Read(p)
	}
	if c.remaining == 0 {
		return 0, errHandshakeTooLarge
	}
	if len(p) > c.remaining {
		p = p[:c.remaining]
	}
	n, err := c.Conn.Read(p)
	c.remaining -= n
	for _, b := range p[:n] {
		if b == '\n' {
			if c.lineEnded {
				c.done = true
				break
			}
			c.lineEnded = true
		} else if b != '\r' {
			c.lineEnded = false
		}
	}
	return n, err
}
