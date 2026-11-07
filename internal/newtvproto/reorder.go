package newtvproto

type ReorderBuffer struct {
	next    uint32
	limit   int
	pending map[uint32]PlaybackFrame
}

func NewReorderBuffer(next uint32, limit int) *ReorderBuffer {
	return &ReorderBuffer{next: next, limit: limit, pending: make(map[uint32]PlaybackFrame)}
}

// Push drops duplicates and returns every newly contiguous payload in sequence order.
func (b *ReorderBuffer) Push(frame PlaybackFrame) ([][]byte, bool) {
	if frame.Sequence < b.next {
		return nil, false
	}
	if _, exists := b.pending[frame.Sequence]; exists {
		return nil, false
	}
	if b.limit <= 0 {
		return nil, false
	}
	var ready [][]byte
	if len(b.pending) >= b.limit {
		ready = b.SkipGap()
	}
	if frame.Sequence < b.next {
		return ready, false
	}
	b.pending[frame.Sequence] = frame
	return b.drain(ready), true
}

func (b *ReorderBuffer) SkipGap() [][]byte {
	var earliest uint32
	found := false
	for sequence := range b.pending {
		if sequence > b.next && (!found || sequence < earliest) {
			earliest, found = sequence, true
		}
	}
	if !found {
		return nil
	}
	b.next = earliest
	return b.drain(nil)
}

func (b *ReorderBuffer) drain(ready [][]byte) [][]byte {
	for {
		frame, exists := b.pending[b.next]
		if !exists {
			return ready
		}
		delete(b.pending, b.next)
		ready = append(ready, frame.Payload)
		b.next++
	}
}

func (b *ReorderBuffer) Next() uint32 { return b.next }
