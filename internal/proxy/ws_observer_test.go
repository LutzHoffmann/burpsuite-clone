package proxy

import (
	"bytes"
	"encoding/binary"
	"reflect"
	"testing"
)

func observerFrame(first byte, masked bool, payload []byte) []byte {
	h := []byte{first, 0}
	switch {
	case len(payload) < 126:
		h[1] = byte(len(payload))
	case len(payload) <= 65535:
		h[1] = 126
		h = binary.BigEndian.AppendUint16(h, uint16(len(payload)))
	default:
		h[1] = 127
		h = binary.BigEndian.AppendUint64(h, uint64(len(payload)))
	}
	key := [4]byte{17, 31, 53, 97}
	if masked {
		h[1] |= 128
		h = append(h, key[:]...)
	}
	for i, b := range payload {
		if masked {
			b ^= key[i%4]
		}
		h = append(h, b)
	}
	return h
}

func TestWSObserverFragmentedControlsAndImmutableInput(t *testing.T) {
	for _, masked := range []bool{false, true} {
		for _, chunk := range []int{1, 3, 4096} {
			var got []wsObservedMessage
			o := newWSObserver(masked, false, 100, func(m wsObservedMessage) { got = append(got, m) })
			var wire []byte
			for _, f := range []struct {
				op   byte
				body string
			}{{1, "hel"}, {137, "p"}, {0, "lo"}, {138, "q"}, {128, "!"}, {130, "\x00\xff"}, {136, "\x03\xe8"}, {129, ""}} {
				wire = append(wire, observerFrame(f.op, masked, []byte(f.body))...)
			}
			original := bytes.Clone(wire)
			for pos := 0; pos < len(wire); pos += chunk {
				o.Feed(wire[pos:min(pos+chunk, len(wire))])
			}
			if o.Finish() || o.Finish() {
				t.Fatal("valid stream incomplete")
			}
			if !bytes.Equal(wire, original) {
				t.Fatal("mutated relay bytes")
			}
			wantTypes := []string{"ping", "pong", "text", "binary", "close", "text"}
			wantBodies := []string{"p", "q", "hello!", "\x00\xff", "\x03\xe8", ""}
			if len(got) != len(wantTypes) {
				t.Fatalf("records: %+v", got)
			}
			for i, m := range got {
				if m.Type != wantTypes[i] || string(m.Payload) != wantBodies[i] || m.Size != int64(len(wantBodies[i])) || !m.Complete || m.Truncated {
					t.Fatalf("record %d: %+v", i, m)
				}
			}
			o.Feed(observerFrame(129, masked, []byte("ignored")))
			if len(got) != len(wantTypes) {
				t.Fatal("Feed after Finish emitted")
			}
		}
	}
}

func TestWSObserverLengthsAndLimits(t *testing.T) {
	for _, size := range []int{0, 125, 126, 65535, 65536, 200000} {
		for _, limit := range []int64{-1, 0, 7, 200001} {
			var got []wsObservedMessage
			o := newWSObserver(true, false, limit, func(m wsObservedMessage) { got = append(got, m) })
			body := bytes.Repeat([]byte{0xab}, size)
			wire := observerFrame(130, true, body)
			for pos := 0; pos < len(wire); pos += 131 {
				o.Feed(wire[pos:min(pos+131, len(wire))])
			}
			if o.Finish() || len(got) != 1 {
				t.Fatalf("size %d: %+v", size, got)
			}
			m := got[0]
			retained := min(int64(size), max(limit, 0))
			if m.Size != int64(size) || int64(len(m.Payload)) != retained || m.Truncated != (retained < int64(size)) || !bytes.Equal(m.Payload, body[:retained]) {
				t.Fatalf("size=%d limit=%d record=%+v", size, limit, m)
			}
		}
	}
}

func TestWSObserverOpaqueExtensions(t *testing.T) {
	for _, first := range []byte{0x01, 0x41, 0x21, 0x11} {
		var got []wsObservedMessage
		o := newWSObserver(false, true, 10, func(m wsObservedMessage) { got = append(got, m) })
		o.Feed(observerFrame(first, false, []byte{255, 0}))
		o.Feed(observerFrame(128, false, []byte{42}))
		if o.Finish() || len(got) != 1 || got[0].Encoding != "opaque" || !bytes.Equal(got[0].Payload, []byte{255, 0, 42}) {
			t.Fatalf("opaque: %+v", got)
		}
	}
}

func TestWSObserverMalformedStopsDirection(t *testing.T) {
	for name, wire := range map[string][]byte{
		"mask mismatch":       {129, 128, 0, 0, 0, 0},
		"reserved opcode":     {131, 0},
		"unnegotiated RSV":    {193, 0},
		"orphan continuation": {128, 0},
		"fragmented control":  {9, 0},
		"large control":       {137, 126, 0, 126},
		"nonminimal16":        {129, 126, 0, 1},
		"nonminimal64":        {129, 127, 0, 0, 0, 0, 0, 0, 1, 0},
		"invalid64":           {129, 127, 128, 0, 0, 0, 0, 0, 0, 0},
		"one byte close":      {136, 1, 0},
	} {
		t.Run(name, func(t *testing.T) {
			var got []wsObservedMessage
			o := newWSObserver(false, false, 8, func(m wsObservedMessage) { got = append(got, m) })
			o.Feed(wire)
			o.Feed(observerFrame(129, false, []byte("ignored")))
			if !o.Finish() || len(got) != 0 {
				t.Fatalf("malformed not stopped: %+v", got)
			}
		})
	}
}

func TestWSObserverInterrupted(t *testing.T) {
	for _, wire := range [][]byte{{129}, {129, 126, 0}, {129, 4, 'a', 'b'}, observerFrame(1, false, []byte("ab")), append(observerFrame(1, false, []byte("ab")), 129, 0)} {
		var got []wsObservedMessage
		o := newWSObserver(false, false, 1, func(m wsObservedMessage) { got = append(got, m) })
		o.Feed(wire)
		if !o.Finish() || !o.Finish() {
			t.Fatal("missing incomplete flag")
		}
		if len(wire) > 3 {
			want := []wsObservedMessage{{Type: "text", Size: 2, Payload: []byte("a"), Truncated: true, Complete: false, Encoding: "identity"}}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("got %+v want %+v", got, want)
			}
		} else if len(got) != 0 {
			t.Fatalf("invented message: %+v", got)
		}
	}
}

func TestWSObserverHugeDeclaredLengthCountsOnlyObservedBytes(t *testing.T) {
	var got []wsObservedMessage
	o := newWSObserver(false, false, 2, func(m wsObservedMessage) { got = append(got, m) })
	o.Feed([]byte{130, 127, 127, 255, 255, 255, 255, 255, 255, 255, 1, 2, 3})
	if !o.Finish() || len(got) != 1 || got[0].Size != 3 || !bytes.Equal(got[0].Payload, []byte{1, 2}) || !got[0].Truncated || got[0].Complete {
		t.Fatalf("huge: %+v", got)
	}
}

func TestWSObserverStreamingRetentionBound(t *testing.T) {
	var got []wsObservedMessage
	o := newWSObserver(true, false, 17, func(m wsObservedMessage) { got = append(got, m) })
	body := bytes.Repeat([]byte("x"), 8192)
	o.Feed(observerFrame(2, true, body))
	continuation := observerFrame(0, true, body)
	for i := 0; i < 1024; i++ {
		o.Feed(continuation)
		if len(o.data.Payload) > 17 || cap(o.data.Payload) > 17 || len(got) != 0 {
			t.Fatal("fragment retention grew or emitted early")
		}
	}
	o.Feed(observerFrame(137, true, []byte("ping")))
	o.Feed(observerFrame(128, true, nil))
	if o.Finish() || len(got) != 2 || got[1].Size != 1025*8192 || string(got[1].Payload) != "xxxxxxxxxxxxxxxxx" || !got[1].Truncated {
		t.Fatalf("stream result: %+v", got)
	}
}

func TestWSObserverInterruptedControlAndMaskHeader(t *testing.T) {
	var got []wsObservedMessage
	o := newWSObserver(false, false, 4, func(m wsObservedMessage) { got = append(got, m) })
	o.Feed(observerFrame(1, false, []byte("data")))
	o.Feed([]byte{137, 3, 'p'})
	if !o.Finish() || len(got) != 2 || got[0].Type != "ping" || got[0].Size != 1 || got[0].Complete || got[1].Type != "text" || got[1].Complete {
		t.Fatalf("interrupted records: %+v", got)
	}
	o = newWSObserver(true, false, 4, nil)
	o.Feed([]byte{129, 128, 1, 2})
	if !o.Finish() {
		t.Fatal("partial masking key accepted")
	}
	o = newWSObserver(true, false, 4, nil)
	o.Feed(observerFrame(129, false, nil))
	if !o.Finish() {
		t.Fatal("unmasked client frame accepted")
	}
	o = newWSObserver(false, false, 0, nil)
	o.Feed(observerFrame(129, false, []byte("no callback")))
	if o.Finish() {
		t.Fatal("nil callback rejected")
	}
}

func FuzzWSObserverChunkBoundaries(f *testing.F) {
	f.Add(observerFrame(129, true, []byte("hello")), true, false)
	f.Add(append(observerFrame(1, false, []byte("a")), observerFrame(128, false, []byte("b"))...), false, false)
	f.Add([]byte{130, 127, 127, 255, 255, 255, 255, 255, 255, 255, 1}, false, true)
	f.Fuzz(func(t *testing.T, wire []byte, masked, extended bool) {
		original := bytes.Clone(wire)
		parse := func(chunk int) ([]wsObservedMessage, bool) {
			var got []wsObservedMessage
			o := newWSObserver(masked, extended, 17, func(m wsObservedMessage) {
				if len(m.Payload) > 17 || cap(m.Payload) > 17 || m.Size < int64(len(m.Payload)) || m.Truncated != (m.Size > int64(len(m.Payload))) {
					t.Fatalf("invalid bounded record: %+v", m)
				}
				got = append(got, m)
			})
			for pos := 0; pos < len(wire); pos += chunk {
				o.Feed(wire[pos:min(pos+chunk, len(wire))])
			}
			return got, o.Finish()
		}
		a, badA := parse(max(1, len(wire)))
		b, badB := parse(1)
		if !reflect.DeepEqual(a, b) || badA != badB {
			t.Fatalf("read-boundary dependent result: %+v/%v versus %+v/%v", a, badA, b, badB)
		}
		if !bytes.Equal(wire, original) {
			t.Fatal("mutated input")
		}
	})
}
