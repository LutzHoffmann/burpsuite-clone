package proxy

import (
	"bufio"
	"crypto/sha1"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

type bufferedConnection struct {
	net.Conn
	reader *bufio.Reader
}

func (c *bufferedConnection) Read(p []byte) (int, error) { return c.reader.Read(p) }

func headerToken(h http.Header, name, token string) bool {
	for _, v := range h.Values(name) {
		for _, part := range strings.Split(v, ",") {
			if strings.EqualFold(strings.TrimSpace(part), token) {
				return true
			}
		}
	}
	return false
}

func validWSUpgrade(r *http.Request, response *http.Response) bool {
	key := r.Header.Get("Sec-WebSocket-Key")
	sum := sha1.Sum([]byte(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
	if !headerToken(response.Header, "Upgrade", "websocket") || !headerToken(response.Header, "Connection", "upgrade") || len(response.Header.Values("Sec-WebSocket-Accept")) != 1 || response.Header.Get("Sec-WebSocket-Accept") != base64.StdEncoding.EncodeToString(sum[:]) {
		return false
	}
	if protocol := response.Header.Get("Sec-WebSocket-Protocol"); protocol != "" {
		found := false
		for _, v := range r.Header.Values("Sec-WebSocket-Protocol") {
			for _, offered := range strings.Split(v, ",") {
				if strings.TrimSpace(offered) == protocol {
					found = true
				}
			}
		}
		if !found || len(response.Header.Values("Sec-WebSocket-Protocol")) != 1 {
			return false
		}
	}
	for _, v := range response.Header.Values("Sec-WebSocket-Extensions") {
		for _, extension := range strings.Split(v, ",") {
			name := strings.TrimSpace(strings.SplitN(extension, ";", 2)[0])
			found := false
			for _, offer := range r.Header.Values("Sec-WebSocket-Extensions") {
				for _, part := range strings.Split(offer, ",") {
					if strings.TrimSpace(strings.SplitN(part, ";", 2)[0]) == name {
						found = true
					}
				}
			}
			if name == "" || !found {
				return false
			}
		}
	}
	return true
}

func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	key, err := base64.StdEncoding.DecodeString(r.Header.Get("Sec-WebSocket-Key"))
	if r.Method != "GET" || !headerToken(r.Header, "Connection", "upgrade") || r.Header.Get("Sec-WebSocket-Version") != "13" || err != nil || len(key) != 16 || r.ContentLength > 0 || len(r.TransferEncoding) > 0 {
		http.Error(w, "invalid WebSocket handshake", 400)
		return
	}
	request := r.Clone(r.Context())
	request.URL.Scheme = strings.Replace(request.URL.Scheme, "ws", "http", 1)
	if request.URL.Scheme == "" {
		request.URL.Scheme = "http"
	}
	if request.URL.Host == "" {
		request.URL.Host = r.Host
	}
	prepared := prepareStreamingRequest(request, s.cfg.BodyLimitBytes, classifyWithRules(s.currentScopeRules(), request))
	prepared.forward.Header.Set("Connection", "Upgrade")
	prepared.forward.Header.Set("Upgrade", "websocket")
	prepared.headers = prepared.forward.Header.Clone()
	started := time.Now().UTC()
	response, err := s.cfg.Transport.RoundTrip(prepared.forward)
	if err != nil {
		s.saveRoundTripFailure(prepared, started, err)
		http.Error(w, "forward WebSocket handshake", 502)
		return
	}
	if response.StatusCode != 101 {
		s.writeHTTPResponse(w, r, prepared, response, started)
		return
	}
	upstream, ok := response.Body.(io.ReadWriteCloser)
	if !ok || !validWSUpgrade(prepared.forward, response) {
		_ = response.Body.Close()
		http.Error(w, "invalid upstream WebSocket upgrade", 502)
		return
	}
	defer upstream.Close()
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "WebSocket hijack unavailable", 500)
		return
	}
	client, buffered, err := hijacker.Hijack()
	if err != nil {
		return
	}
	defer client.Close()
	_ = client.SetWriteDeadline(time.Now().Add(s.cfg.StreamIdleTimeout))
	headers := response.Header.Clone()
	stripHopByHopHeaders(headers)
	headers.Set("Connection", "Upgrade")
	headers.Set("Upgrade", "websocket")
	headers.Del("Content-Length")
	if _, err = fmt.Fprint(buffered, "HTTP/1.1 101 Switching Protocols\r\n"); err == nil {
		err = headers.Write(buffered)
	}
	if err == nil {
		_, err = fmt.Fprint(buffered, "\r\n")
	}
	if err == nil {
		err = buffered.Flush()
	}
	if err != nil {
		return
	}
	_ = client.SetDeadline(time.Time{})
	capture := s.startWSCapture(request, prepared.decision.InScope)
	if capture != nil {
		defer capture.finish()
	}
	done := make(chan struct{}, 2)
	extended := response.Header.Get("Sec-WebSocket-Extensions") != ""
	go func() {
		s.copyWS(upstream, &idleTimeoutReadCloser{ReadCloser: &readerCloser{Reader: buffered.Reader}, connection: client, timeout: s.cfg.StreamIdleTimeout}, true, extended, capture)
		done <- struct{}{}
	}()
	go func() {
		s.copyWS(&idleTimeoutWriter{Writer: client, connection: client, timeout: s.cfg.StreamIdleTimeout}, upstream, false, extended, capture)
		done <- struct{}{}
	}()
	<-done
	_ = client.Close()
	_ = upstream.Close()
	<-done
}

type readerCloser struct{ io.Reader }

func (*readerCloser) Close() error { return nil }

func (s *Server) copyWS(dst io.Writer, src io.Reader, masked, extended bool, capture *wsCapture) {
	var observer *wsObserver
	if capture != nil {
		direction := "server-to-client"
		if masked {
			direction = "client-to-server"
		}
		observer = newWSObserver(masked, extended, s.cfg.BodyLimitBytes, func(message wsObservedMessage) { capture.emit(direction, message) })
		defer func() {
			if observer.Finish() {
				capture.incomplete.Store(true)
			}
		}()
	}
	buf := make([]byte, 32*1024)
	for {
		n, err := src.Read(buf)
		if n > 0 {
			written, writeErr := dst.Write(buf[:n])
			if observer != nil && written > 0 {
				observer.Feed(buf[:written])
			}
			if writeErr != nil || written != n {
				if capture != nil {
					capture.incomplete.Store(true)
				}
				return
			}
		}
		if err != nil {
			return
		}
	}
}
