package newtvproto

import (
	"slices"
	"time"
)

type RequestWindow struct {
	next        uint32
	limit       int
	retryAfter  time.Duration
	outstanding map[uint32]time.Time
}

func NewRequestWindow(next uint32, limit int, retryAfter time.Duration) *RequestWindow {
	return &RequestWindow{next: next, limit: limit, retryAfter: retryAfter, outstanding: make(map[uint32]time.Time)}
}

func (w *RequestWindow) Fill(now time.Time) []uint32 {
	if w.limit <= 0 {
		return nil
	}
	sequences := make([]uint32, 0, w.limit-len(w.outstanding))
	for len(w.outstanding) < w.limit {
		sequence := w.next
		w.next++
		w.outstanding[sequence] = now
		sequences = append(sequences, sequence)
	}
	return sequences
}

func (w *RequestWindow) FillBatch(now time.Time, minimum int) []uint32 {
	if w.limit-len(w.outstanding) < minimum {
		return nil
	}
	return w.Fill(now)
}

func (w *RequestWindow) Received(sequence uint32) bool {
	if _, exists := w.outstanding[sequence]; !exists {
		return false
	}
	delete(w.outstanding, sequence)
	return true
}

func (w *RequestWindow) Advance(next uint32) {
	for sequence := range w.outstanding {
		if sequence < next {
			delete(w.outstanding, sequence)
		}
	}
}

func (w *RequestWindow) Retries(now time.Time) []uint32 {
	var sequences []uint32
	for sequence, sent := range w.outstanding {
		if now.Sub(sent) < w.retryAfter {
			continue
		}
		w.outstanding[sequence] = now
		sequences = append(sequences, sequence)
	}
	slices.Sort(sequences)
	return sequences
}
