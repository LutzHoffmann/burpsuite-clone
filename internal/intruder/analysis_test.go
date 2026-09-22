package intruder

import (
	"bytes"
	"math"
	"testing"
	"time"
)

func TestAnalyzeWithoutBaseline(t *testing.T) {
	a := Analyze(ResultCapture{Status: 200, Body: []byte("hello"), Size: 5}, nil)
	if a.BaselineAvailable || a.Similarity != 0 || !a.SimilarityPartial {
		t.Fatalf("analysis = %+v", a)
	}
}

func TestAnalyzeExactAndEmptyBodies(t *testing.T) {
	for _, body := range [][]byte{nil, []byte("same"), bytes.Repeat([]byte{0, 255, 1}, 100)} {
		capture := ResultCapture{Status: 200, MIMEType: "text/plain; charset=utf-8", Body: body, Size: int64(len(body)), Duration: 10 * time.Millisecond, BodyStored: true}
		a := Analyze(capture, &capture)
		if !a.BaselineAvailable || a.Similarity != 10000 || a.StatusDiff || a.MIMEDiff || a.LengthDelta != 0 || a.DurationDelta != 0 || a.SimilarityPartial {
			t.Fatalf("body %d: %+v", len(body), a)
		}
	}
}

func TestAnalyzeDifferencesAndPartialCaptures(t *testing.T) {
	base := ResultCapture{Status: 200, MIMEType: "text/plain; charset=utf-8", Body: []byte("abcd"), BodyStored: true, Size: 4, Duration: 5 * time.Millisecond}
	current := ResultCapture{Status: 500, MIMEType: "application/json; charset=utf-8", Body: []byte("wxyz"), BodyStored: true, Size: 9, Duration: 25 * time.Millisecond, Truncated: true}
	a := Analyze(current, &base)
	if !a.StatusDiff || !a.MIMEDiff || a.LengthDelta != 5 || a.DurationDelta != 20 || a.Similarity != 0 || !a.SimilarityPartial {
		t.Fatalf("analysis = %+v", a)
	}
	current = base
	current.MIMEType = "text/plain; charset=iso-8859-1"
	if a := Analyze(current, &base); a.MIMEDiff {
		t.Fatalf("media type parameters should be ignored: %+v", a)
	}
	current.BodyStored = false
	if a := Analyze(current, &base); !a.SimilarityPartial {
		t.Fatalf("omitted body should be partial: %+v", a)
	}
}

func TestAnalyzeBoundsLargeBinaryAndDeltas(t *testing.T) {
	body := bytes.Repeat([]byte{0, 255, 128, 1}, 1<<20)
	base := ResultCapture{Body: body, BodyStored: true, Size: math.MaxInt64, Duration: time.Duration(math.MaxInt64)}
	current := ResultCapture{Body: body, BodyStored: true, Size: 0, Duration: 0}
	a := Analyze(current, &base)
	if a.Similarity != 10000 || a.LengthDelta != -math.MaxInt64 || a.DurationDelta != -math.MaxInt64/int64(time.Millisecond) {
		t.Fatalf("analysis = %+v", a)
	}
	current.Body = append(body, 1)
	if a := Analyze(current, &base); !a.SimilarityPartial {
		t.Fatalf("oversized capture not marked partial: %+v", a)
	}
}
