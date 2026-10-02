package intruder

import (
	"bytes"
	"mime"
	"strings"
	"time"
)

const analysisBodyLimit = 2 << 20
const analysisChunkBytes = 64

type ResultCapture struct {
	Status     int
	MIMEType   string
	Body       []byte
	BodyStored bool
	Truncated  bool
	Size       int64
	Duration   time.Duration
}

type Analysis struct {
	BaselineAvailable bool
	StatusDiff        bool
	MIMEDiff          bool
	LengthDelta       int64
	DurationDelta     int64
	Similarity        int
	SimilarityPartial bool
}

func Analyze(current ResultCapture, baseline *ResultCapture) Analysis {
	if baseline == nil {
		return Analysis{SimilarityPartial: true}
	}
	a := Analysis{
		BaselineAvailable: true,
		StatusDiff:        current.Status != baseline.Status,
		MIMEDiff:          mediaType(current.MIMEType) != mediaType(baseline.MIMEType),
		LengthDelta:       clampedDelta(current.Size, baseline.Size),
		DurationDelta:     clampedDelta(current.Duration.Milliseconds(), baseline.Duration.Milliseconds()),
		SimilarityPartial: !current.BodyStored || !baseline.BodyStored || current.Truncated || baseline.Truncated || len(current.Body) > analysisBodyLimit || len(baseline.Body) > analysisBodyLimit,
	}
	left := current.Body
	right := baseline.Body
	if len(left) > analysisBodyLimit {
		left = left[:analysisBodyLimit]
	}
	if len(right) > analysisBodyLimit {
		right = right[:analysisBodyLimit]
	}
	a.Similarity = chunkSimilarity(left, right)
	if !current.BodyStored || !baseline.BodyStored {
		a.Similarity = 0
	}
	return a
}

func mediaType(value string) string {
	parsed, _, err := mime.ParseMediaType(value)
	if err == nil {
		return parsed
	}
	return strings.ToLower(strings.TrimSpace(value))
}

func clampedDelta(value, baseline int64) int64 {
	if baseline < 0 && value > baseline && value > int64(^uint64(0)>>1)+baseline {
		return int64(^uint64(0) >> 1)
	}
	if baseline > 0 && value < baseline && value < -int64(^uint64(0)>>1)-1+baseline {
		return -int64(^uint64(0)>>1) - 1
	}
	return value - baseline
}

func chunkSimilarity(a, b []byte) int {
	if len(a) == 0 && len(b) == 0 {
		return 10000
	}
	maxLen := len(a)
	if len(b) > maxLen {
		maxLen = len(b)
	}
	chunks := (maxLen + analysisChunkBytes - 1) / analysisChunkBytes
	matching := 0
	for start := 0; start < maxLen; start += analysisChunkBytes {
		if start >= len(a) || start >= len(b) {
			continue
		}
		endA := start + analysisChunkBytes
		if endA > len(a) {
			endA = len(a)
		}
		endB := start + analysisChunkBytes
		if endB > len(b) {
			endB = len(b)
		}
		if bytes.Equal(a[start:endA], b[start:endB]) {
			matching++
		}
	}
	return matching * 10000 / chunks
}
