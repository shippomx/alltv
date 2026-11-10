package newtvproto

import (
	"io"
	"time"
)

type ContinuityWriter struct {
	dst            io.Writer
	next           map[uint16]byte
	mediaCC        map[uint16]byte
	seen           map[uint16]bool
	dropping       map[uint16]bool
	started        bool
	session        uint64
	marked         map[uint16]uint64
	offset         uint64
	offsetOK       bool
	lastPCR        uint64
	havePCR        bool
	lastPTS        map[uint16]uint64
	emittedPTS     map[uint16]uint64
	emittedDTS     map[uint16]uint64
	emittedAt      map[uint16]time.Time
	emittedPCR     uint64
	haveEmittedPCR bool
	emittedPCRAt   time.Time
	ptsStep        map[uint16]uint64
	video          map[uint16]byte
	waitKey        map[uint16]bool
	params         map[uint16]byte
	knownParams    map[uint16]bool
	rawPTS         map[uint16]uint64
	resumePTS      map[uint16]uint64
	resumeOut      map[uint16]uint64
	resumeStep     map[uint16]uint64
	waitPTS        map[uint16]bool
	resumeMax      map[uint16]byte
	waitPMT        bool
	waitVideo      bool
	heldPES        map[uint16][]byte
	heldParams     map[uint16][]byte
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
		emittedPTS: make(map[uint16]uint64),
		emittedDTS: make(map[uint16]uint64),
		emittedAt:  make(map[uint16]time.Time),
		video:      make(map[uint16]byte), waitKey: make(map[uint16]bool), rawPTS: make(map[uint16]uint64),
		params: make(map[uint16]byte), knownParams: make(map[uint16]bool),
		resumePTS: make(map[uint16]uint64), waitPTS: make(map[uint16]bool),
		resumeOut:  make(map[uint16]uint64),
		resumeStep: make(map[uint16]uint64),
		resumeMax:  make(map[uint16]byte),
		heldPES:    make(map[uint16][]byte),
		heldParams: make(map[uint16][]byte),
	}
}

func continuityWriter(dst io.Writer) *ContinuityWriter {
	if writer, ok := dst.(*ContinuityWriter); ok {
		return writer
	}
	return NewContinuityWriter(dst)
}

func (w *ContinuityWriter) BeginSession() {
	if w.started {
		w.beginDiscontinuity()
	}
	w.started = true
}

func (w *ContinuityWriter) beginDiscontinuity() {
	w.session++
	w.offsetOK = false
	for pid := range w.video {
		w.waitKey[pid] = true
		if w.knownParams[pid] {
			w.params[pid] = requiredParameterSets(w.video[pid])
		} else {
			w.params[pid] = 0
		}
		delete(w.heldPES, pid)
		delete(w.heldParams, pid)
	}
	w.waitVideo = len(w.video) > 0
	for pid, pts := range w.rawPTS {
		w.resumePTS[pid] = pts
		w.resumeOut[pid] = w.emittedPTS[pid]
		w.resumeStep[pid] = w.ptsStep[pid]
		w.waitPTS[pid] = true
		w.resumeMax[pid] = 50
	}
	for pid := range w.seen {
		if pid > 31 && pid != 4096 {
			w.dropping[pid] = true
		}
	}
}

func (w *ContinuityWriter) Write(p []byte) (int, error) {
	var replay []byte
	output := make([]byte, 0, len(p))
	if len(p)%TSPacketSize == 0 {
		for offset := 0; offset < len(p); offset += TSPacketSize {
			packet := p[offset : offset+TSPacketSize]
			pid := uint16(packet[1]&0x1f)<<8 | uint16(packet[2])
			w.readPMT(packet, pid)
			if w.session > 0 && w.marked[pid] != w.session && packet[3]&0x20 != 0 && packet[4] > 0 {
				packet[5] |= 0x80
				w.marked[pid] = w.session
			}
			hasPayload := packet[3]&0x10 != 0
			if !hasPayload {
				if w.waitVideo {
					if _, video := w.video[pid]; video {
						for i := range packet {
							packet[i] = 0xff
						}
						packet[0], packet[1], packet[2], packet[3] = 0x47, 0x1f, 0xff, 0x10
						output = append(output, packet...)
						continue
					}
				}
				w.normalizeTimestamps(packet, pid)
				w.recordEmitted(packet, pid)
				output = append(output, packet...)
				continue
			}
			if pid <= 31 || pid == 4096 {
				w.normalizeTimestamps(packet, pid)
				w.recordEmitted(packet, pid)
				if next, ok := w.next[pid]; ok {
					packet[3] = packet[3]&0xf0 | next
				}
				w.next[pid] = (packet[3] + 1) & 0x0f
				output = append(output, packet...)
				continue
			}
			if w.waitPMT {
				for i := range packet {
					packet[i] = 0xff
				}
				packet[0], packet[1], packet[2], packet[3] = 0x47, 0x1f, 0xff, 0x10
				output = append(output, packet...)
				continue
			}
			counter := packet[3] & 0x0f
			startsPES := packet[1]&0x40 != 0
			if w.seen[pid] && counter != w.mediaCC[pid] {
				if !startsPES {
					w.dropping[pid] = true
				}
				if _, video := w.video[pid]; video {
					if !w.waitKey[pid] {
						w.beginDiscontinuity()
					}
					w.waitKey[pid] = true
					if !w.knownParams[pid] {
						w.params[pid] = 0
					}
					delete(w.heldPES, pid)
					delete(w.heldParams, pid)
				}
			}
			if _, video := w.video[pid]; video && !w.waitKey[pid] && w.timelineJump(packet, pid) {
				w.beginDiscontinuity()
			}
			_, video := w.video[pid]
			if video && w.waitKey[pid] {
				delete(w.waitPTS, pid)
				delete(w.resumeMax, pid)
			}
			timelineReady := !w.waitVideo || video
			if timelineReady {
				timelineReady = w.normalizeTimestamps(packet, pid)
			}
			w.seen[pid] = true
			w.mediaCC[pid] = (counter + 1) & 0x0f
			if startsPES {
				w.dropping[pid] = false
			}
			if !timelineReady {
				w.dropping[pid] = true
			}
			if w.waitVideo {
				if _, video := w.video[pid]; !video {
					w.dropping[pid] = true
				}
			}
			if streamType, video := w.video[pid]; video && w.waitKey[pid] {
				if startsPES {
					previousParams, previousRandomAccess := videoPESNALs(w.heldPES[pid], streamType)
					w.params[pid] |= previousParams
					if timelineReady && previousRandomAccess && w.params[pid] == requiredParameterSets(streamType) {
						w.waitKey[pid] = false
						w.waitVideo = false
						w.knownParams[pid] = true
						replay = append(replay, w.heldParams[pid]...)
						replay = append(replay, w.heldPES[pid]...)
						replay = append(replay, packet...)
						w.rebaseReplay(replay, pid)
						delete(w.heldPES, pid)
						delete(w.heldParams, pid)
					} else if previousParams != 0 {
						w.heldParams[pid] = append(w.heldParams[pid], w.heldPES[pid]...)
					}
					w.heldPES[pid] = w.heldPES[pid][:0]
					if !w.knownParams[pid] {
						w.params[pid], _ = videoPESNALs(w.heldParams[pid], streamType)
					}
				}
				if len(replay) == 0 && timelineReady && (startsPES || len(w.heldPES[pid]) > 0) {
					w.heldPES[pid] = append(w.heldPES[pid], packet...)
				}
				w.dropping[pid] = true
			}
			if w.dropping[pid] {
				if len(replay) > 0 {
					for offset := 0; offset < len(replay); offset += TSPacketSize {
						held := replay[offset : offset+TSPacketSize]
						heldPID := uint16(held[1]&0x1f)<<8 | uint16(held[2])
						if next, ok := w.next[heldPID]; ok {
							held[3] = held[3]&0xf0 | next
						}
						w.next[heldPID] = (held[3] + 1) & 0x0f
						w.recordEmitted(held, heldPID)
					}
					output = append(output, replay...)
					replay = nil
					w.dropping[pid] = false
					continue
				}
				for i := range packet {
					packet[i] = 0xff
				}
				packet[0], packet[1], packet[2], packet[3] = 0x47, 0x1f, 0xff, 0x10
				output = append(output, packet...)
				continue
			}
			if next, ok := w.next[pid]; ok {
				packet[3] = packet[3]&0xf0 | next
			}
			w.next[pid] = (packet[3] + 1) & 0x0f
			w.recordEmitted(packet, pid)
			output = append(output, packet...)
		}
	} else {
		output = append(output, p...)
	}
	if err := writeFull(w.dst, output); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (w *ContinuityWriter) timelineJump(packet []byte, pid uint16) bool {
	dts, ok := readDecodeTimestamp(packet)
	previous := w.emittedDTS[pid]
	if !ok || previous == 0 || !w.offsetOK {
		return false
	}
	adjusted := (dts + w.offset) & timestampMask
	forward := (adjusted - previous) & timestampMask
	backward := (previous - adjusted) & timestampMask
	return (forward > 4_500 && forward < 1<<32) || (backward > 0 && backward < 1<<32)
}

func (w *ContinuityWriter) recordEmitted(packet []byte, pid uint16) {
	if pcr, ok := readPCR(packet); ok {
		w.emittedPCR, w.haveEmittedPCR = pcr, true
		w.emittedPCRAt = time.Now()
	}
	if _, pts, ok := readPTS(packet); ok {
		w.emittedPTS[pid] = pts
		w.emittedAt[pid] = time.Now()
	}
	if dts, ok := readDecodeTimestamp(packet); ok {
		w.emittedDTS[pid] = dts
	}
}

func (w *ContinuityWriter) writeHeartbeat() error {
	if !w.waitVideo || !w.haveEmittedPCR || time.Since(w.emittedPCRAt) < 40*time.Millisecond {
		return nil
	}
	var pid uint16
	for candidate := range w.video {
		pid = candidate
		break
	}
	if pid == 0 {
		return nil
	}
	packet := make([]byte, TSPacketSize)
	for i := range packet {
		packet[i] = 0xff
	}
	packet[0], packet[1], packet[2] = 0x47, byte(pid>>8)&0x1f, byte(pid)
	packet[3], packet[4], packet[5] = 0x20|(w.next[pid]-1)&0x0f, 183, 0x10
	if w.marked[pid] != w.session {
		packet[5] |= 0x80
		w.marked[pid] = w.session
	}
	now := time.Now()
	pcr := (w.emittedPCR + uint64(now.Sub(w.emittedPCRAt))*90_000/uint64(time.Second)) & timestampMask
	writePCR(packet, pcr)
	if err := writeFull(w.dst, packet); err != nil {
		return err
	}
	w.emittedPCR, w.emittedPCRAt = pcr, now
	return nil
}

func (w *ContinuityWriter) rebaseReplay(packets []byte, videoPID uint16) {
	target, ok := w.resumeOut[videoPID]
	if !ok || target == 0 {
		return
	}
	var first uint64
	for offset := 0; offset+TSPacketSize <= len(packets); offset += TSPacketSize {
		packet := packets[offset : offset+TSPacketSize]
		pid := uint16(packet[1]&0x1f)<<8 | uint16(packet[2])
		if pid == videoPID {
			_, first, ok = readPTS(packet)
			if ok {
				break
			}
		}
	}
	if !ok {
		return
	}
	correction := (w.resumeTarget(videoPID, target, w.resumeStep[videoPID]) - first) & timestampMask
	w.offset = (w.offset + correction) & timestampMask
	for offset := 0; offset+TSPacketSize <= len(packets); offset += TSPacketSize {
		packet := packets[offset : offset+TSPacketSize]
		pid := uint16(packet[1]&0x1f)<<8 | uint16(packet[2])
		if pcr, hasPCR := readPCR(packet); hasPCR {
			pcr = (pcr + correction) & timestampMask
			writePCR(packet, pcr)
			w.lastPCR, w.havePCR = pcr, true
		}
		if ptsOffset, pts, hasPTS := readPTS(packet); hasPTS {
			pts = (pts + correction) & timestampMask
			writeTimestamp(packet[ptsOffset:ptsOffset+5], pts)
			w.lastPTS[pid] = pts
			w.emittedPTS[pid] = pts
			if packet[ptsOffset]&0xf0 == 0x30 && ptsOffset+10 <= len(packet) {
				dts := (readTimestamp(packet[ptsOffset+5:ptsOffset+10]) + correction) & timestampMask
				writeTimestamp(packet[ptsOffset+5:ptsOffset+10], dts)
			}
		}
	}
	delete(w.resumeOut, videoPID)
	delete(w.resumeStep, videoPID)
}

func (w *ContinuityWriter) resumeTarget(pid uint16, target, step uint64) uint64 {
	if elapsed := time.Since(w.emittedAt[pid]); elapsed > 0 {
		step = max(step, uint64(elapsed)*90_000/uint64(time.Second))
	}
	return (target + max(step, 1)) & timestampMask
}

func writeFull(dst io.Writer, p []byte) error {
	for len(p) > 0 {
		n, err := dst.Write(p)
		p = p[n:]
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
	}
	return nil
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
			previous, known := w.video[elementaryPID]
			w.video[elementaryPID] = streamType
			if known && previous != streamType {
				delete(w.knownParams, elementaryPID)
				w.beginDiscontinuity()
			}
			if !known {
				w.waitKey[elementaryPID] = true
				w.params[elementaryPID] = 0
				w.waitVideo = true
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
	return videoNALData(packet[offset:], streamType)
}

func videoPESNALs(packets []byte, streamType byte) (byte, bool) {
	data := make([]byte, 0, len(packets))
	for offset := 0; offset+TSPacketSize <= len(packets); offset += TSPacketSize {
		packet := packets[offset : offset+TSPacketSize]
		payload, ok := payloadOffset(packet)
		if ok {
			data = append(data, packet[payload:]...)
		}
	}
	return videoNALData(data, streamType)
}

func videoNALData(data []byte, streamType byte) (byte, bool) {
	var parameterSets byte
	var randomAccess bool
	for i := 0; i+4 <= len(data); i++ {
		if data[i] != 0 || data[i+1] != 0 || data[i+2] != 1 {
			continue
		}
		typeID := data[i+3] & 0x1f
		if streamType == 0x1b {
			if typeID == 7 {
				parameterSets |= 1
			} else if typeID == 8 {
				parameterSets |= 2
			}
			randomAccess = randomAccess || typeID == 5
		}
		typeID = data[i+3] >> 1 & 0x3f
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
	_, video := w.video[pid]
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
			if step := (pts - previous) & timestampMask; step <= 9_000 {
				w.ptsStep[pid] = step
			}
		}
		w.rawPTS[pid] = pts
	}
	if !w.offsetOK {
		switch {
		case hasPTS && w.emittedPTS[pid] != 0:
			w.offset = (w.resumeTarget(pid, w.emittedPTS[pid], w.ptsStep[pid]) - pts) & timestampMask
			w.offsetOK = true
		case hasPCR && w.haveEmittedPCR:
			w.offset = (w.emittedPCR + 1 - pcr) & timestampMask
			w.offsetOK = true
		}
	}
	if !w.offsetOK {
		return true
	}
	normalizedPTS := (pts + w.offset) & timestampMask
	if hasPTS && w.emittedPTS[pid] != 0 {
		adjusted := normalizedPTS
		forward := (adjusted - w.emittedPTS[pid]) & timestampMask
		backward := (w.emittedPTS[pid] - adjusted) & timestampMask
		if video && ((forward > 4_500 && forward < 1<<32) || (backward > 4_500 && backward < 1<<32)) {
			w.offset = (w.emittedPTS[pid] + max(w.ptsStep[pid], 1) - pts) & timestampMask
			normalizedPTS = (pts + w.offset) & timestampMask
		} else if !video && backward > 0 && backward < 1<<32 {
			normalizedPTS = (w.emittedPTS[pid] + max(w.ptsStep[pid], 1)) & timestampMask
		}
	}
	if hasPCR {
		pcr = (pcr + w.offset) & timestampMask
		writePCR(packet, pcr)
		w.lastPCR, w.havePCR = pcr, true
	}
	if hasPTS {
		writeTimestamp(packet[ptsOffset:ptsOffset+5], normalizedPTS)
		w.lastPTS[pid] = normalizedPTS
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

func readDecodeTimestamp(packet []byte) (uint64, bool) {
	ptsOffset, pts, ok := readPTS(packet)
	if !ok {
		return 0, false
	}
	if packet[ptsOffset]&0xf0 == 0x30 && ptsOffset+10 <= len(packet) {
		return readTimestamp(packet[ptsOffset+5 : ptsOffset+10]), true
	}
	return pts, true
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
