package proxy

import "encoding/binary"

type wsObservedMessage struct {
	Type                string
	Size                int64
	Payload             []byte
	Truncated, Complete bool
	Encoding            string
}

// wsObserver is direction-local and must be fed serially. It never owns or
// modifies relay buffers; emitted payloads are independent retained copies.
type wsObserver struct {
	masked, extended  bool
	limit             int64
	emit              func(wsObservedMessage)
	header            [14]byte
	have, need        int
	remaining, offset uint64
	key               [4]byte
	fin, control      bool
	data, ctrl        *wsObservedMessage
	bad, finished     bool
}

func newWSObserver(masked bool, extended bool, limit int64, emit func(wsObservedMessage)) *wsObserver {
	return &wsObserver{masked: masked, extended: extended, limit: max(0, limit), emit: emit, need: 2}
}

func (o *wsObserver) Feed(p []byte) {
	for len(p) > 0 && !o.bad && !o.finished {
		if o.remaining == 0 {
			n := min(o.need-o.have, len(p))
			copy(o.header[o.have:], p[:n])
			o.have += n
			p = p[n:]
			if o.have < o.need {
				continue
			}
			if o.need == 2 {
				switch o.header[1] & 127 {
				case 126:
					o.need += 2
				case 127:
					o.need += 8
				}
				if o.header[1]&128 != 0 {
					o.need += 4
				}
				if o.have < o.need {
					continue
				}
			}
			if !o.startFrame() {
				o.bad = true
				return
			}
			if o.remaining == 0 {
				o.endFrame()
				continue
			}
		}
		n := len(p)
		if uint64(n) > o.remaining {
			n = int(o.remaining)
		}
		m := o.data
		if o.control {
			m = o.ctrl
		}
		if int64(n) > int64(^uint64(0)>>1)-m.Size {
			o.bad = true
			return
		}
		o.retain(m, p[:n])
		m.Size += int64(n)
		m.Truncated = m.Size > int64(len(m.Payload))
		o.remaining -= uint64(n)
		o.offset += uint64(n)
		p = p[n:]
		if o.remaining == 0 {
			o.endFrame()
		}
	}
}

func (o *wsObserver) startFrame() bool {
	a, b := o.header[0], o.header[1]
	op := a & 15
	o.fin = a&128 != 0
	o.control = op&8 != 0
	if (b&128 != 0) != o.masked || a&112 != 0 && !o.extended {
		return false
	}
	if op != 0 && op != 1 && op != 2 && op != 8 && op != 9 && op != 10 {
		return false
	}
	length := uint64(b & 127)
	keyAt := 2
	switch length {
	case 126:
		length = uint64(binary.BigEndian.Uint16(o.header[2:4]))
		keyAt = 4
		if length < 126 {
			return false
		}
	case 127:
		length = binary.BigEndian.Uint64(o.header[2:10])
		keyAt = 10
		if length < 65536 || length>>63 != 0 {
			return false
		}
	}
	if o.control && (!o.fin || length > 125 || op == 8 && length == 1) {
		return false
	}
	encoding := "identity"
	// Unknown negotiated extensions may affect payloads even without RSV bits.
	// Retain their wire payloads, but never advertise them as decoded text.
	if o.extended {
		encoding = "opaque"
	}
	if o.control {
		typ := map[byte]string{8: "close", 9: "ping", 10: "pong"}[op]
		o.ctrl = &wsObservedMessage{Type: typ, Encoding: encoding}
	} else if op == 0 {
		if o.data == nil {
			return false
		}
	} else {
		if o.data != nil {
			return false
		}
		typ := "text"
		if op == 2 {
			typ = "binary"
		}
		o.data = &wsObservedMessage{Type: typ, Encoding: encoding}
	}
	if o.masked {
		copy(o.key[:], o.header[keyAt:keyAt+4])
	}
	o.remaining = length
	o.offset = 0
	return true
}

func (o *wsObserver) retain(m *wsObservedMessage, p []byte) {
	n := int64(len(p))
	n = min(n, o.limit-int64(len(m.Payload)))
	if n <= 0 {
		return
	}
	want := len(m.Payload) + int(n)
	if want > cap(m.Payload) {
		// Clamp growth to the configured retention bound, not the frame length.
		capacity := min(o.limit, max(int64(want), int64(cap(m.Payload))*2))
		capacity = min(capacity, int64(int(^uint(0)>>1)))
		grown := make([]byte, len(m.Payload), int(capacity))
		copy(grown, m.Payload)
		m.Payload = grown
	}
	start := len(m.Payload)
	m.Payload = append(m.Payload, p[:int(n)]...)
	if o.masked {
		for i := start; i < len(m.Payload); i++ {
			m.Payload[i] ^= o.key[(o.offset+uint64(i-start))%4]
		}
	}
}

func (o *wsObserver) endFrame() {
	if o.control {
		o.ctrl.Complete = true
		o.publish(o.ctrl)
		o.ctrl = nil
	} else if o.fin {
		o.data.Complete = true
		o.publish(o.data)
		o.data = nil
	}
	o.have = 0
	o.need = 2
}

func (o *wsObserver) publish(m *wsObservedMessage) {
	if o.emit != nil {
		o.emit(*m)
	}
}

// Finish flushes interrupted messages once. Size always counts received payload
// bytes, never unreceived bytes advertised by a frame header.
func (o *wsObserver) Finish() bool {
	if o.finished {
		return o.bad
	}
	o.finished = true
	o.bad = o.bad || o.have != 0 || o.remaining != 0 || o.data != nil || o.ctrl != nil
	if o.ctrl != nil {
		o.publish(o.ctrl)
		o.ctrl = nil
	}
	if o.data != nil {
		o.publish(o.data)
		o.data = nil
	}
	return o.bad
}
