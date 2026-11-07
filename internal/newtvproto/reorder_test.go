package newtvproto

import "testing"

func TestReorderBuffer(t *testing.T) {
	b := NewReorderBuffer(0, 8)
	for i, sequence := range []uint32{3, 2, 1, 1} {
		ready, accepted := b.Push(PlaybackFrame{Sequence: sequence, Payload: []byte{byte(sequence)}})
		if accepted != (i < 3) {
			t.Fatalf("sequence %d accepted=%v", sequence, accepted)
		}
		if len(ready) != 0 {
			t.Fatalf("sequence %d released data before gap closed", sequence)
		}
	}
	ready, accepted := b.Push(PlaybackFrame{Sequence: 0, Payload: []byte{0}})
	if !accepted || len(ready) != 4 || b.Next() != 4 {
		t.Fatalf("accepted=%v ready=%d next=%d", accepted, len(ready), b.Next())
	}
	for i, payload := range ready {
		if payload[0] != byte(i) {
			t.Fatalf("ready[%d]=%d", i, payload[0])
		}
	}
}

func TestReorderBufferLimit(t *testing.T) {
	b := NewReorderBuffer(0, 1)
	if _, ok := b.Push(PlaybackFrame{Sequence: 2}); !ok {
		t.Fatal("first pending frame rejected")
	}
	ready, ok := b.Push(PlaybackFrame{Sequence: 3})
	if !ok || len(ready) != 2 || b.Next() != 4 {
		t.Fatalf("accepted=%v ready=%d next=%d", ok, len(ready), b.Next())
	}
}

func TestReorderBufferCanSkipStalledGapBeforeFull(t *testing.T) {
	b := NewReorderBuffer(10, 10)
	b.Push(PlaybackFrame{Sequence: 12, Payload: []byte{12}})
	ready := b.SkipGap()
	if len(ready) != 1 || ready[0][0] != 12 || b.Next() != 13 {
		t.Fatalf("ready=%v next=%d", ready, b.Next())
	}
}
