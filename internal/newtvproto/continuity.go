package newtvproto

import (
	"io"
)

type ContinuityWriter struct {
	dst       io.Writer
	next      map[uint16]byte
	mediaCC   map[uint16]byte
	seen      map[uint16]bool
	dropping  map[uint16]bool
	started   bool
	session   uint64
	marked    map[uint16]uint64
	offset    uint64
	offsetOK  bool
	lastPCR   uint64
	havePCR   bool
	lastPTS   map[uint16]uint64
	ptsStep   map[uint16]uint64
	video     map[uint16]byte
	waitKey   map[uint16]bool
	params    map[uint16]byte
	rawPTS    map[uint16]uint64
	resumePTS map[uint16]uint64
	waitPTS   map[uint16]bool
	resumeMax map[uint16]byte
	waitPMT   bool
}

func newPSIContinuityWriter(dst io.Writer) io.Writer {
	return NewContinuityWriter(dst)
}

// NewContinuityWriter preserves MPEG-TS continuity across upstream session changes.
func NewContinuityWriter(dst io.Writer) *ContinuityWriter {
	return &ContinuityWriter{
		dst: dst, next: make(map[uint16]byte), mediaCC: make(map[uint16]byte),
		seen: make(map[uint16]bool), dropping: make(map[uint16]bool), marked: make(map[uint16]uint64),
		offsetOK: true, lastPTS: make(map[uint16]uint64), ptsStep: make(map[uint16]uint64),
		video: make(map[uint16]byte), waitKey: make(map[uint16]bool), rawPTS: make(map[uint16]uint64),
		params:    make(map[uint16]byte),
		resumePTS: make(map[uint16]uint64), waitPTS: make(map[uint16]bool),
		resumeMax: make(map[uint16]byte),
	}
}

func (w *ContinuityWriter) BeginSession() {
	if w.started {
		w.session++
		w.offsetOK = false
		for pid := range w.video {
			w.waitKey[pid] = true
			w.params[pid] = 0
		}
		for pid, pts := range w.rawPTS {
			w.resumePTS[pid] = pts
			w.waitPTS[pid] = true
			w.resumeMax[pid] = 50
		}
		for pid := range w.seen {
			if pid > 31 && pid != 4096 {
				w.dropping[pid] = true
			}
		}
	}
	w.started = true
}

func (w *ContinuityWriter) Write(p []byte) (int, error) {
	if len(p)%TSPacketSize == 0 {
		for offset := 0; offset < len(p); offset += TSPacketSize {
			packet := p[offset : offset+TSPacketSize]
			pid := uint16(packet[1]&0x1f)<<8 | uint16(packet[2])
			w.readPMT(packet, pid)
			timelineReady := w.normalizeTimestamps(packet, pid)
			if w.session > 0 && w.marked[pid] != w.session && packet[3]&0x20 != 0 && packet[4] > 0 {
				packet[5] |= 0x80
				w.marked[pid] = w.session
			}
			hasPayload := packet[3]&0x10 != 0
			if !hasPayload {
				continue
			}
			if pid <= 31 || pid == 4096 {
				if next, ok := w.next[pid]; ok {
					packet[3] = packet[3]&0xf0 | next
				}
				w.next[pid] = (packet[3] + 1) & 0x0f
				continue
			}
			if w.waitPMT {
				for i := range packet {
					packet[i] = 0xff
				}
				packet[0], packet[1], packet[2], packet[3] = 0x47, 0x1f, 0xff, 0x10
				continue
			}
			counter := packet[3] & 0x0f
			startsPES := packet[1]&0x40 != 0
			if w.seen[pid] && counter != w.mediaCC[pid] {
				if !startsPES {
					w.dropping[pid] = true
				}
				if _, video := w.video[pid]; video {
					w.waitKey[pid] = true
					w.params[pid] = 0
				}
			}
			w.seen[pid] = true
			w.mediaCC[pid] = (counter + 1) & 0x0f
			if startsPES {
				w.dropping[pid] = false
			}
			if !timelineReady {
				w.dropping[pid] = true
			}
			if streamType, video := w.video[pid]; video && w.waitKey[pid] {
				parameterSets, randomAccess := videoNALs(packet, streamType)
				w.params[pid] |= parameterSets
				if timelineReady && randomAccess && w.params[pid] == requiredParameterSets(streamType) {
					w.waitKey[pid] = false
					w.dropping[pid] = false
				} else if timelineReady && parameterSets != 0 {
					w.dropping[pid] = false
				} else {
					w.dropping[pid] = true
				}
			}
			if w.dropping[pid] {
				for i := range packet {
					packet[i] = 0xff
				}
				packet[0], packet[1], packet[2], packet[3] = 0x47, 0x1f, 0xff, 0x10
				continue
			}
			if next, ok := w.next[pid]; ok {
				packet[3] = packet[3]&0xf0 | next
			}
			w.next[pid] = (packet[3] + 1) & 0x0f
		}
	}
	written := 0
	for written < len(p) {
		n, err := w.dst.Write(p[written:])
		written += n
		if err != nil {
			return written, err
		}
		if n == 0 {
			return written, io.ErrShortWrite
		}
	}
	return written, nil
}

func payloadOffset(packet []byte) (int, bool) {
	if packet[3]&0x10 == 0 {
		return 0, false
	}
	offset := 4
	if packet[3]&0x20 != 0 {
		offset += int(packet[4]) + 1
	}
	return offset, offset < len(packet)
}

func (w *ContinuityWriter) readPMT(packet []byte, pid uint16) {
	if pid != 4096 || packet[1]&0x40 == 0 {
		return
	}
	offset, ok := payloadOffset(packet)
	if !ok {
		return
	}
	offset += int(packet[offset]) + 1
	if offset+12 > len(packet) || packet[offset] != 2 {
		return
	}
	end := offset + 3 + int(packet[offset+1]&0x0f)<<8 + int(packet[offset+2]) - 4
	position := offset + 12 + int(packet[offset+10]&0x0f)<<8 + int(packet[offset+11])
	if end > len(packet) {
		end = len(packet)
	}
	for position+5 <= end {
		streamType := packet[position]
		elementaryPID := uint16(packet[position+1]&0x1f)<<8 | uint16(packet[position+2])
		if !w.seen[elementaryPID] {
			w.dropping[elementaryPID] = true
		}
		if streamType == 0x1b || streamType == 0x24 {
			_, known := w.video[elementaryPID]
			w.video[elementaryPID] = streamType
			if !known {
				w.waitKey[elementaryPID] = true
				w.params[elementaryPID] = 0
			}
		}
		position += 5 + int(packet[position+3]&0x0f)<<8 + int(packet[position+4])
	}
	w.waitPMT = false
}

func requiredParameterSets(streamType byte) byte {
	if streamType == 0x24 {
		return 7
	}
	return 3
}

func videoNALs(packet []byte, streamType byte) (byte, bool) {
	offset, ok := payloadOffset(packet)
	if !ok {
		return 0, false
	}
	var parameterSets byte
	var randomAccess bool
	for i := offset; i+4 <= len(packet); i++ {
		if packet[i] != 0 || packet[i+1] != 0 || packet[i+2] != 1 {
			continue
		}
		typeID := packet[i+3] & 0x1f
		if streamType == 0x1b {
			if typeID == 7 {
				parameterSets |= 1
			} else if typeID == 8 {
				parameterSets |= 2
			}
			randomAccess = randomAccess || typeID == 5
		}
		typeID = packet[i+3] >> 1 & 0x3f
		if streamType == 0x24 {
			if typeID >= 32 && typeID <= 34 {
				parameterSets |= 1 << (typeID - 32)
			}
			randomAccess = randomAccess || typeID == 19 || typeID == 20
		}
	}
	return parameterSets, randomAccess
}

const timestampMask uint64 = 1<<33 - 1

func (w *ContinuityWriter) normalizeTimestamps(packet []byte, pid uint16) bool {
	pcr, hasPCR := readPCR(packet)
	ptsOffset, pts, hasPTS := readPTS(packet)
	resuming := w.waitPTS[pid]
	if hasPTS && w.waitPTS[pid] {
		if !timestampAfter(pts, w.resumePTS[pid]) {
			if w.resumeMax[pid] > 0 {
				w.resumeMax[pid]--
				return false
			}
		}
		delete(w.waitPTS, pid)
		delete(w.resumeMax, pid)
	}
	if w.waitPTS[pid] {
		return false
	}
	if hasPTS {
		if previous, ok := w.rawPTS[pid]; ok && !resuming && timestampAfter(pts, previous) {
			if step := (pts - previous) & timestampMask; step <= 90_000 {
				w.ptsStep[pid] = step
			}
		}
		w.rawPTS[pid] = pts
	}
	if !w.offsetOK {
		switch {
		case hasPTS && w.lastPTS[pid] != 0:
			w.offset = (w.lastPTS[pid] + max(w.ptsStep[pid], 1) - pts) & timestampMask
			w.offsetOK = true
		case hasPCR && w.havePCR:
			w.offset = (w.lastPCR + 1 - pcr) & timestampMask
			w.offsetOK = true
		}
	}
	if !w.offsetOK {
		return true
	}
	if hasPCR {
		pcr = (pcr + w.offset) & timestampMask
		writePCR(packet, pcr)
		w.lastPCR, w.havePCR = pcr, true
	}
	if hasPTS {
		pts = (pts + w.offset) & timestampMask
		writeTimestamp(packet[ptsOffset:ptsOffset+5], pts)
		w.lastPTS[pid] = pts
		if packet[ptsOffset]&0xf0 == 0x30 && ptsOffset+10 <= len(packet) {
			dts := (readTimestamp(packet[ptsOffset+5:ptsOffset+10]) + w.offset) & timestampMask
			writeTimestamp(packet[ptsOffset+5:ptsOffset+10], dts)
		}
	}
	return true
}

func timestampAfter(value, previous uint64) bool {
	delta := (value - previous) & timestampMask
	return delta > 0 && delta < 1<<32
}

func readPCR(packet []byte) (uint64, bool) {
	if packet[3]&0x20 == 0 || packet[4] < 7 || packet[5]&0x10 == 0 {
		return 0, false
	}
	p := packet[6:12]
	return uint64(p[0])<<25 | uint64(p[1])<<17 | uint64(p[2])<<9 | uint64(p[3])<<1 | uint64(p[4]>>7), true
}

func writePCR(packet []byte, value uint64) {
	p := packet[6:12]
	p[0], p[1], p[2], p[3] = byte(value>>25), byte(value>>17), byte(value>>9), byte(value>>1)
	p[4] = byte(value&1)<<7 | p[4]&0x7f
}

func readPTS(packet []byte) (int, uint64, bool) {
	if packet[1]&0x40 == 0 || packet[3]&0x10 == 0 {
		return 0, 0, false
	}
	offset := 4
	if packet[3]&0x20 != 0 {
		offset += int(packet[4]) + 1
	}
	if offset+14 > len(packet) || packet[offset] != 0 || packet[offset+1] != 0 || packet[offset+2] != 1 || packet[offset+7]&0x80 == 0 {
		return 0, 0, false
	}
	ptsOffset := offset + 9
	return ptsOffset, readTimestamp(packet[ptsOffset : ptsOffset+5]), true
}

func readTimestamp(p []byte) uint64 {
	return uint64(p[0]>>1&7)<<30 | uint64(p[1])<<22 | uint64(p[2]>>1)<<15 | uint64(p[3])<<7 | uint64(p[4]>>1)
}

func writeTimestamp(p []byte, value uint64) {
	p[0] = p[0]&0xf0 | byte(value>>30&7)<<1 | 1
	p[1] = byte(value >> 22)
	p[2] = byte(value>>15&0x7f)<<1 | 1
	p[3] = byte(value >> 7)
	p[4] = byte(value&0x7f)<<1 | 1
}
