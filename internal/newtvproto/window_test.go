package newtvproto

import (
	"slices"
	"testing"
	"time"
)

func TestRequestWindowFillReceiveRetry(t *testing.T) {
	now := time.Unix(100, 0)
	w := NewRequestWindow(7, 3, time.Second)
	if got := w.Fill(now); !slices.Equal(got, []uint32{7, 8, 9}) {
		t.Fatalf("initial fill=%v", got)
	}
	if !w.Received(8) || w.Received(8) {
		t.Fatal("receive did not remove exactly one outstanding sequence")
	}
	if got := w.Fill(now); !slices.Equal(got, []uint32{10}) {
		t.Fatalf("refill=%v", got)
	}
	if got := w.Retries(now.Add(999 * time.Millisecond)); len(got) != 0 {
		t.Fatalf("early retries=%v", got)
	}
	got := w.Retries(now.Add(time.Second))
	if !slices.Equal(got, []uint32{7, 9, 10}) {
		t.Fatalf("retries=%v", got)
	}
	if got := w.Retries(now.Add(1500 * time.Millisecond)); len(got) != 0 {
		t.Fatalf("duplicate retries=%v", got)
	}
}

func TestRequestWindowAdvanceDropsExpiredGaps(t *testing.T) {
	w := NewRequestWindow(7, 3, time.Second)
	w.Fill(time.Now())
	w.Advance(9)
	if got := w.Fill(time.Now()); len(got) != 2 || got[0] != 10 || got[1] != 11 {
		t.Fatalf("fill=%v", got)
	}
}
