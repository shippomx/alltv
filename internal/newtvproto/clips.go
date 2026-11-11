package newtvproto

type dataClip struct {
	StreamID uint32
	Sequence uint32
}

type clipPlan struct {
	items []dataClip
	index map[uint64]uint32
}

func newClipPlan(clips []ClipInfo, streamID, sequence uint32) *clipPlan {
	p := &clipPlan{index: make(map[uint64]uint32)}
	started := false
	for _, clip := range clips {
		start := uint32(0)
		if !started {
			if clip.StreamID != streamID {
				continue
			}
			start, started = sequence, true
		}
		for current := start; current < clip.Count; current++ {
			ordinal := uint32(len(p.items))
			p.items = append(p.items, dataClip{clip.StreamID, current})
			p.index[uint64(clip.StreamID)<<32|uint64(current)] = ordinal
		}
	}
	return p
}

func (p *clipPlan) append(clips []ClipInfo) {
	for _, clip := range clips {
		for sequence := uint32(0); sequence < clip.Count; sequence++ {
			key := uint64(clip.StreamID)<<32 | uint64(sequence)
			if _, exists := p.index[key]; exists {
				continue
			}
			ordinal := uint32(len(p.items))
			p.items = append(p.items, dataClip{clip.StreamID, sequence})
			p.index[key] = ordinal
		}
	}
}

func (p *clipPlan) get(ordinal uint32) (dataClip, bool) {
	if int(ordinal) >= len(p.items) {
		return dataClip{}, false
	}
	return p.items[ordinal], true
}

func (p *clipPlan) ordinal(streamID, sequence uint32) (uint32, bool) {
	ordinal, ok := p.index[uint64(streamID)<<32|uint64(sequence)]
	return ordinal, ok
}

func (p *clipPlan) remaining(ordinal uint32) int {
	if int(ordinal) >= len(p.items) {
		return 0
	}
	return len(p.items) - int(ordinal)
}
