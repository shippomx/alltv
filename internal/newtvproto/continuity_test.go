package newtvproto

import (
	"bytes"
	"testing"
	"time"
)

func TestPSIContinuityWriterRepairsOnlyPATAndPMT(t *testing.T) {
	packets := make([]byte, 3*TSPacketSize)
	for i, pid := range []uint16{17, 4096, 256} {
		packet := packets[i*TSPacketSize : (i+1)*TSPacketSize]
		packet[0] = 0x47
		packet[1], packet[2], packet[3] = byte(pid>>8), byte(pid), 0x17
	}
	var output bytes.Buffer
	w := newPSIContinuityWriter(&output)
	if _, err := w.Write(packets); err != nil {
		t.Fatal(err)
	}
	second := append([]byte(nil), packets...)
	second[2*TSPacketSize+3] = 0x18
	if _, err := w.Write(second); err != nil {
		t.Fatal(err)
	}
	got := output.Bytes()[len(packets):]
	if got[3]&0x0f != 8 || got[TSPacketSize+3]&0x0f != 8 {
		t.Fatalf("PSI/PMT counters=%d/%d", got[3]&0x0f, got[TSPacketSize+3]&0x0f)
	}
	if got[2*TSPacketSize+3]&0x0f != 8 {
		t.Fatalf("video counter changed to %d", got[2*TSPacketSize+3]&0x0f)
	}
}

func TestContinuityWriterIsNotWrappedTwice(t *testing.T) {
	writer := NewContinuityWriter(&bytes.Buffer{})
	if continuityWriter(writer) != writer {
		t.Fatal("existing continuity writer was wrapped a second time")
	}
}

func TestPSIContinuityWriterDropsDamagedPESUntilNextStart(t *testing.T) {
	packet := func(counter byte, startsPES bool) []byte {
		p := make([]byte, TSPacketSize)
		p[0], p[1], p[2], p[3] = 0x47, 0x01, 0x00, 0x10|counter
		if startsPES {
			p[1] |= 0x40
		}
		return p
	}
	input := append(packet(1, true), packet(3, false)...)
	input = append(input, packet(4, false)...)
	input = append(input, packet(5, true)...)
	var output bytes.Buffer
	if _, err := newPSIContinuityWriter(&output).Write(input); err != nil {
		t.Fatal(err)
	}
	got := output.Bytes()
	if got[TSPacketSize+1] != 0x1f || got[2*TSPacketSize+1] != 0x1f {
		t.Fatalf("damaged PES was not replaced by null packets")
	}
	if got[3*TSPacketSize+1]&0x1f != 1 || got[3*TSPacketSize+1]&0x40 == 0 {
		t.Fatalf("next PES start was dropped")
	}
	if got[3*TSPacketSize+3]&0x0f != 2 {
		t.Fatalf("recovered media counter=%d", got[3*TSPacketSize+3]&0x0f)
	}
}

func TestContinuityWriterDoesNotLeakDamagedAudioPES(t *testing.T) {
	packet := func(counter byte, startsPES bool) []byte {
		p := make([]byte, TSPacketSize)
		p[0], p[1], p[2], p[3] = 0x47, 0x01, 0x01, 0x10|counter
		if startsPES {
			p[1] |= 0x40
		}
		return p
	}
	var output bytes.Buffer
	w := NewContinuityWriter(&output)
	w.video[256] = 0x1b
	input := append(packet(1, true), packet(3, false)...)
	input = append(input, packet(4, true)...)
	input = append(input, packet(5, true)...)
	if _, err := w.Write(input); err != nil {
		t.Fatal(err)
	}
	got := output.Bytes()
	if len(got) != 3*TSPacketSize || got[1] != 0x1f || got[TSPacketSize+1] != 0x1f {
		t.Fatalf("damaged audio PES escaped: packets=%d headers=%x", len(got)/TSPacketSize, got[:min(len(got), TSPacketSize+4)])
	}
	if pid := uint16(got[2*TSPacketSize+1]&0x1f)<<8 | uint16(got[2*TSPacketSize+2]); pid != 257 {
		t.Fatalf("next complete audio PES was not emitted: pid=%d", pid)
	}
}

func TestContinuityWriterDropsTruncatedAudioPESWithContinuousCounters(t *testing.T) {
	packet := func(counter byte) []byte {
		p := make([]byte, TSPacketSize)
		p[0], p[1], p[2], p[3] = 0x47, 0x41, 0x01, 0x10|counter
		copy(p[4:], []byte{0, 0, 1, 0xc0, 0x02, 0x00})
		return p
	}
	var output bytes.Buffer
	w := NewContinuityWriter(&output)
	w.video[256] = 0x1b
	input := append(packet(1), packet(2)...)
	if _, err := w.Write(input); err != nil {
		t.Fatal(err)
	}
	got := output.Bytes()
	if len(got) != TSPacketSize || got[1] != 0x1f {
		t.Fatalf("truncated audio PES escaped: packets=%d header=%x", len(got)/TSPacketSize, got[:min(len(got), 4)])
	}
}

func TestContinuityWriterSurvivesUpstreamWriterReset(t *testing.T) {
	packet := func(counter byte) []byte {
		p := make([]byte, TSPacketSize)
		p[0], p[2], p[3], p[4] = 0x47, 0, 0x30|counter, 1
		return p
	}
	var output bytes.Buffer
	continuous := NewContinuityWriter(&output)
	continuous.BeginSession()
	first := newPSIContinuityWriter(continuous)
	_, _ = first.Write(append(packet(14), packet(15)...))
	continuous.BeginSession()
	second := newPSIContinuityWriter(continuous)
	_, _ = second.Write(packet(7))
	got := output.Bytes()
	if counters := []byte{got[3] & 0x0f, got[TSPacketSize+3] & 0x0f, got[2*TSPacketSize+3] & 0x0f}; !bytes.Equal(counters, []byte{14, 15, 0}) {
		t.Fatalf("counters=%v", counters)
	}
	if got[2*TSPacketSize+5]&0x80 == 0 {
		t.Fatal("new upstream session lacks discontinuity indicator")
	}
}

func TestContinuityWriterRebasesTimestampsAcrossSession(t *testing.T) {
	packet := func(counter byte, pcr, pts uint64) []byte {
		p := make([]byte, TSPacketSize)
		p[0], p[1], p[2], p[3], p[4], p[5] = 0x47, 0x41, 0x00, 0x30|counter, 7, 0x10
		writePCR(p, pcr)
		copy(p[12:], []byte{0, 0, 1, 0xe0, 0, 0, 0x80, 0x80, 5})
		p[21] = 0x20
		writeTimestamp(p[21:26], pts)
		return p
	}
	var output bytes.Buffer
	w := NewContinuityWriter(&output)
	w.BeginSession()
	_, _ = w.Write(packet(0, 1000, 2000))
	_, _ = w.Write(packet(1, 2920, 3920))
	w.BeginSession()
	_, _ = w.Write(packet(2, 5000, 6000))
	third := output.Bytes()[2*TSPacketSize:]
	pcr, _ := readPCR(third)
	_, pts, _ := readPTS(third)
	if pcr != 4840 || pts != 5840 {
		t.Fatalf("pcr=%d pts=%d", pcr, pts)
	}
}

func TestContinuityWriterRebaseKeepsVideoDTSMonotonic(t *testing.T) {
	packet := func(counter byte, pts, dts uint64) []byte {
		p := make([]byte, TSPacketSize)
		p[0], p[1], p[2], p[3] = 0x47, 0x41, 0, 0x10|counter
		copy(p[4:], []byte{0, 0, 1, 0xe0, 0, 0, 0x80, 0xc0, 10})
		p[13], p[18] = 0x30, 0x10
		writeTimestamp(p[13:18], pts)
		writeTimestamp(p[18:23], dts)
		return p
	}
	packets := append(packet(0, 11_000, 8_000), packet(1, 12_000, 7_900)...)
	w := NewContinuityWriter(&bytes.Buffer{})
	w.resumeOut[256], w.resumeStep[256], w.emittedDTS[256] = 10_000, 1_800, 9_500
	w.rebaseReplay(packets, 256)
	first, ok := readDecodeTimestamp(packets[:TSPacketSize])
	if !ok || !timestampAfter(first, 9_500) {
		t.Fatalf("first rebased DTS=%d ok=%v, previous=9500", first, ok)
	}
	if dts, ok := readDecodeTimestamp(packets[TSPacketSize:]); !ok || !timestampAfter(dts, first) {
		t.Fatalf("rebased DTS=%d ok=%v, previous=9500", dts, ok)
	}
}

func TestContinuityWriterNormalizesVideoDTSRollback(t *testing.T) {
	packet := make([]byte, TSPacketSize)
	packet[0], packet[1], packet[2], packet[3] = 0x47, 0x41, 0, 0x10
	copy(packet[4:], []byte{0, 0, 1, 0xe0, 0, 0, 0x80, 0xc0, 10})
	packet[13], packet[18] = 0x30, 0x10
	writeTimestamp(packet[13:18], 11_000)
	writeTimestamp(packet[18:23], 8_000)
	w := NewContinuityWriter(&bytes.Buffer{})
	w.video[256], w.emittedDTS[256] = 0x1b, 9_500
	if !w.normalizeTimestamps(packet, 256) {
		t.Fatal("packet was unexpectedly suppressed")
	}
	if dts, ok := readDecodeTimestamp(packet); !ok || !timestampAfter(dts, 9_500) {
		t.Fatalf("normalized DTS=%d ok=%v, previous=9500", dts, ok)
	}
}

func TestContinuityWriterAdvancesRepeatedAudioPTS(t *testing.T) {
	packet := make([]byte, TSPacketSize)
	packet[0], packet[1], packet[2], packet[3] = 0x47, 0x41, 1, 0x10
	copy(packet[4:], []byte{0, 0, 1, 0xc0, 0, 0, 0x80, 0x80, 5})
	writeTimestamp(packet[13:18], 10_000)
	w := NewContinuityWriter(&bytes.Buffer{})
	w.emittedPTS[257], w.ptsStep[257] = 10_000, 1_920
	if !w.normalizeTimestamps(packet, 257) {
		t.Fatal("packet was unexpectedly suppressed")
	}
	if _, pts, ok := readPTS(packet); !ok || pts != 11_920 {
		t.Fatalf("normalized PTS=%d ok=%v", pts, ok)
	}
}

func TestContinuityWriterWaitsForVideoRandomAccessFrame(t *testing.T) {
	packet := func(counter, nal byte, pts uint64) []byte {
		p := make([]byte, TSPacketSize)
		p[0], p[1], p[2], p[3] = 0x47, 0x41, 0, 0x10|counter
		copy(p[4:], []byte{0, 0, 1, 0xe0, 0, 0, 0x80, 0x80, 5})
		p[13] = 0x20
		writeTimestamp(p[13:18], pts)
		copy(p[20:], []byte{0, 0, 1, nal})
		return p
	}
	var output bytes.Buffer
	w := NewContinuityWriter(&output)
	w.video[256] = 0x1b
	w.BeginSession()
	_, _ = w.Write(packet(0, 5, 2000))
	w.BeginSession()
	_, _ = w.Write(packet(1, 5, 1900))
	_, _ = w.Write(packet(2, 1, 2100))
	w.params[256] = 3
	w.knownParams[256] = true
	_, _ = w.Write(packet(3, 5, 2200))
	_, _ = w.Write(packet(4, 1, 2300))
	got := output.Bytes()
	if got[TSPacketSize+1] != 0x1f || got[2*TSPacketSize+1] != 0x1f {
		t.Fatal("replayed or dependent H.264 frame was not dropped")
	}
	resumed := got[len(got)-2*TSPacketSize:]
	if resumed[1]&0x1f != 1 || resumed[3]&0x0f != 1 {
		t.Fatal("H.264 IDR did not resume with continuous counter")
	}
}

func TestContinuityWriterWaitsForKeyframeAfterGapAtPESStart(t *testing.T) {
	packet := func(counter, nal byte) []byte {
		p := make([]byte, TSPacketSize)
		p[0], p[1], p[2], p[3] = 0x47, 0x41, 0, 0x10|counter
		copy(p[4:], []byte{0, 0, 1, 0xe0})
		copy(p[20:], []byte{0, 0, 1, nal})
		return p
	}
	var output bytes.Buffer
	w := NewContinuityWriter(&output)
	w.video[256] = 0x1b
	w.BeginSession()
	_, _ = w.Write(packet(0, 5))
	_, _ = w.Write(packet(2, 1))
	w.params[256] = 3
	w.knownParams[256] = true
	_, _ = w.Write(packet(3, 5))
	_, _ = w.Write(packet(4, 1))
	got := output.Bytes()
	if got[TSPacketSize+1] != 0x1f {
		t.Fatal("dependent frame after a PES-boundary gap was not dropped")
	}
	if got[len(got)-TSPacketSize+1]&0x1f != 1 {
		t.Fatal("IDR after a PES-boundary gap did not resume output")
	}
}

func TestContinuityWriterRebasesTimestampAfterTransportGap(t *testing.T) {
	packet := func(counter byte, pts uint64) []byte {
		p := make([]byte, TSPacketSize)
		p[0], p[1], p[2], p[3] = 0x47, 0x41, 0, 0x10|counter
		copy(p[4:], []byte{0, 0, 1, 0xe0, 0, 0, 0x80, 0x80, 5})
		writeTimestamp(p[13:18], pts)
		copy(p[20:], []byte{0, 0, 1, 5})
		return p
	}
	var output bytes.Buffer
	w := NewContinuityWriter(&output)
	w.video[256], w.knownParams[256], w.params[256] = 0x1b, true, 3
	w.BeginSession()
	_, _ = w.Write(packet(0, 10_000))
	for i := 0; i < 51; i++ {
		_, _ = w.Write(packet(byte(i+2)&0x0f, 2_000))
	}
	got := output.Bytes()
	_, firstPTS, _ := readPTS(got[:TSPacketSize])
	_, resumedPTS, _ := readPTS(got[len(got)-TSPacketSize:])
	if !timestampAfter(resumedPTS, firstPTS) {
		t.Fatalf("transport gap replayed an old timestamp: first=%d resumed=%d", firstPTS, resumedPTS)
	}
}

func TestContinuityWriterRemovesTimeSpentWaitingForIDR(t *testing.T) {
	packet := func(counter, nal byte, pts uint64) []byte {
		p := make([]byte, TSPacketSize)
		p[0], p[1], p[2], p[3] = 0x47, 0x41, 0, 0x10|counter
		copy(p[4:], []byte{0, 0, 1, 0xe0, 0, 0, 0x80, 0x80, 5})
		writeTimestamp(p[13:18], pts)
		copy(p[20:], []byte{0, 0, 1, nal})
		return p
	}
	var output bytes.Buffer
	w := NewContinuityWriter(&output)
	w.video[256], w.knownParams[256], w.params[256] = 0x1b, true, 3
	w.BeginSession()
	_, _ = w.Write(packet(0, 5, 10_000))
	_, _ = w.Write(packet(2, 1, 20_000))
	_, _ = w.Write(packet(3, 5, 25_000))
	_, _ = w.Write(packet(4, 1, 26_000))
	got := output.Bytes()
	_, firstPTS, _ := readPTS(got[:TSPacketSize])
	_, resumedPTS, _ := readPTS(got[len(got)-2*TSPacketSize:])
	if resumedPTS != firstPTS+1 {
		t.Fatalf("IDR wait leaked into output timeline: first=%d resumed=%d", firstPTS, resumedPTS)
	}
}

func TestContinuityWriterClampsSmallAudioTimestampRollback(t *testing.T) {
	packet := func(counter byte, pts uint64) []byte {
		p := make([]byte, TSPacketSize)
		p[0], p[1], p[2], p[3] = 0x47, 0x41, 1, 0x10|counter
		copy(p[4:], []byte{0, 0, 1, 0xc0, 0, 0, 0x80, 0x80, 5})
		writeTimestamp(p[13:18], pts)
		return p
	}
	var output bytes.Buffer
	w := NewContinuityWriter(&output)
	w.BeginSession()
	_, _ = w.Write(packet(0, 100_000))
	_, _ = w.Write(packet(1, 94_960))
	got := output.Bytes()
	_, first, _ := readPTS(got[:TSPacketSize])
	_, second, _ := readPTS(got[TSPacketSize:])
	if !timestampAfter(second, first) {
		t.Fatalf("audio timestamp moved backwards: first=%d second=%d", first, second)
	}
}

func TestContinuityWriterWaitsForIDRAfterUnsignaledTimestampJump(t *testing.T) {
	packet := func(counter, nal byte, pts uint64) []byte {
		p := make([]byte, TSPacketSize)
		p[0], p[1], p[2], p[3] = 0x47, 0x41, 0, 0x10|counter
		copy(p[4:], []byte{0, 0, 1, 0xe0, 0, 0, 0x80, 0x80, 5})
		writeTimestamp(p[13:18], pts)
		copy(p[20:], []byte{0, 0, 1, nal})
		return p
	}
	var output bytes.Buffer
	w := NewContinuityWriter(&output)
	w.video[256], w.knownParams[256], w.params[256] = 0x1b, true, 3
	w.BeginSession()
	_, _ = w.Write(packet(0, 5, 100_000))
	_, _ = w.Write(packet(1, 1, 98_200))
	_, _ = w.Write(packet(2, 5, 101_800))
	_, _ = w.Write(packet(3, 1, 103_600))
	got := output.Bytes()
	if got[TSPacketSize+1] != 0x1f {
		t.Fatal("dependent frame after an unsignaled jump was emitted")
	}
	_, firstPTS, _ := readPTS(got[:TSPacketSize])
	_, resumedPTS, _ := readPTS(got[len(got)-2*TSPacketSize:])
	if delta := (resumedPTS - firstPTS) & timestampMask; delta == 0 || delta > 45_000 {
		t.Fatalf("IDR did not resume on the emitted timeline: first=%d resumed=%d", firstPTS, resumedPTS)
	}
}

func TestContinuityWriterDoesNotResumeHEVCAtCRA(t *testing.T) {
	packet := func(counter, nalType byte) []byte {
		p := make([]byte, TSPacketSize)
		p[0], p[1], p[2], p[3] = 0x47, 0x41, 0, 0x10|counter
		copy(p[4:], []byte{0, 0, 1, 0xe0})
		copy(p[20:], []byte{0, 0, 1, nalType << 1})
		return p
	}
	var output bytes.Buffer
	w := NewContinuityWriter(&output)
	w.video[256] = 0x24
	w.BeginSession()
	_, _ = w.Write(packet(0, 19))
	w.BeginSession()
	_, _ = w.Write(packet(1, 21))
	w.params[256] = 7
	w.knownParams[256] = true
	_, _ = w.Write(packet(2, 19))
	_, _ = w.Write(packet(3, 1))
	got := output.Bytes()
	if got[TSPacketSize+1] != 0x1f {
		t.Fatal("HEVC CRA resumed without its prior reference pictures")
	}
	if got[len(got)-TSPacketSize+1]&0x1f != 1 {
		t.Fatal("HEVC IDR did not resume output")
	}
}

func TestContinuityWriterResumesWithCompleteHEVCRandomAccessPES(t *testing.T) {
	packet := func(counter, nalType byte, startsPES bool) []byte {
		p := make([]byte, TSPacketSize)
		p[0], p[1], p[2], p[3] = 0x47, 0x01, 0, 0x10|counter
		if startsPES {
			p[1] |= 0x40
			copy(p[4:], []byte{0, 0, 1, 0xe0})
		}
		copy(p[20:], []byte{0, 0, 1, nalType << 1})
		return p
	}
	var output bytes.Buffer
	w := NewContinuityWriter(&output)
	w.video[256], w.waitKey[256], w.dropping[256] = 0x24, true, true
	w.BeginSession()
	input := append(packet(0, 32, true), packet(1, 33, false)...)
	input = append(input, packet(2, 34, false)...)
	input = append(input, packet(3, 19, false)...)
	input = append(input, packet(4, 1, true)...)
	_, _ = w.Write(input)
	got := output.Bytes()
	if len(got) < 8*TSPacketSize {
		t.Fatalf("random-access PES was not replayed completely: bytes=%d", len(got))
	}
	replayed := got[len(got)-5*TSPacketSize:]
	for i := 0; i < 4; i++ {
		if replayed[i*TSPacketSize+1]&0x1f != 1 {
			t.Fatalf("replayed PES packet %d is missing", i)
		}
	}
	if replayed[1]&0x40 == 0 {
		t.Fatal("replayed random-access data did not begin at its PES boundary")
	}
	if nalType := replayed[4*TSPacketSize+23] >> 1 & 0x3f; nalType != 1 {
		t.Fatalf("packet after random-access PES was reordered before it: nal=%d", nalType)
	}
}

func TestContinuityWriterDropsMediaUntilPMTAndKeyframe(t *testing.T) {
	video := func(counter, nal byte) []byte {
		p := make([]byte, TSPacketSize)
		p[0], p[1], p[2], p[3] = 0x47, 0x41, 0, 0x10|counter
		copy(p[20:], []byte{0, 0, 1, nal})
		return p
	}
	pmt := make([]byte, TSPacketSize)
	pmt[0], pmt[1], pmt[2], pmt[3], pmt[4] = 0x47, 0x50, 0, 0x10, 0
	pmt[5], pmt[6], pmt[7] = 2, 0xb0, 18
	pmt[15], pmt[16] = 0xf0, 0
	pmt[17], pmt[18], pmt[19], pmt[20], pmt[21] = 0x1b, 0xe1, 0, 0xf0, 0
	var output bytes.Buffer
	w := NewContinuityWriter(&output)
	w.waitPMT = true
	w.BeginSession()
	_, _ = w.Write(video(0, 1))
	_, _ = w.Write(pmt)
	_, _ = w.Write(video(1, 1))
	_, _ = w.Write(video(2, 7))
	_, _ = w.Write(video(3, 8))
	_, _ = w.Write(video(4, 5))
	_, _ = w.Write(video(5, 1))
	got := output.Bytes()
	if got[1] != 0x1f || got[2*TSPacketSize+1] != 0x1f {
		t.Fatal("dependent media escaped before PMT/keyframe")
	}
	if got[len(got)-TSPacketSize+1]&0x1f != 1 {
		t.Fatal("IDR after PMT did not resume output")
	}
}

func TestContinuityWriterRecoversWhenCodecChangesOnSamePID(t *testing.T) {
	video := func(counter, nal byte) []byte {
		p := make([]byte, TSPacketSize)
		p[0], p[1], p[2], p[3] = 0x47, 0x41, 0, 0x10|counter
		copy(p[20:], []byte{0, 0, 1, nal})
		return p
	}
	pmt := make([]byte, TSPacketSize)
	pmt[0], pmt[1], pmt[2], pmt[3], pmt[4] = 0x47, 0x50, 0, 0x10, 0
	pmt[5], pmt[6], pmt[7] = 2, 0xb0, 18
	pmt[15], pmt[16] = 0xf0, 0
	pmt[17], pmt[18], pmt[19], pmt[20], pmt[21] = 0x1b, 0xe1, 0, 0xf0, 0
	var output bytes.Buffer
	w := NewContinuityWriter(&output)
	w.video[256], w.knownParams[256] = 0x24, true
	w.started = true
	w.BeginSession()
	_, _ = w.Write(pmt)
	_, _ = w.Write(video(0, 7))
	_, _ = w.Write(video(1, 8))
	_, _ = w.Write(video(2, 5))
	_, _ = w.Write(video(3, 1))
	if got := output.Bytes(); got[len(got)-TSPacketSize+1]&0x1f != 1 {
		t.Fatal("H.264 IDR did not resume after HEVC-to-H.264 PMT change")
	}
}

func TestContinuityWriterChecksIDRBeforeOldPTSReplayGuard(t *testing.T) {
	video := func(counter, nal byte, pts uint64) []byte {
		p := make([]byte, TSPacketSize)
		p[0], p[1], p[2], p[3] = 0x47, 0x41, 0, 0x10|counter
		copy(p[4:], []byte{0, 0, 1, 0xe0, 0, 0, 0x80, 0x80, 5})
		writeTimestamp(p[13:18], pts)
		copy(p[30:], []byte{0, 0, 1, nal})
		return p
	}
	var output bytes.Buffer
	w := NewContinuityWriter(&output)
	w.video[256], w.knownParams[256], w.rawPTS[256] = 0x1b, true, 100_000
	w.started = true
	w.BeginSession()
	_, _ = w.Write(video(0, 7, 1_000))
	_, _ = w.Write(video(1, 8, 2_000))
	_, _ = w.Write(video(2, 5, 3_000))
	_, _ = w.Write(video(3, 1, 4_000))
	if got := output.Bytes(); got[len(got)-TSPacketSize+1]&0x1f != 1 {
		t.Fatal("IDR replay guard prevented recovery from an older provider timeline")
	}
}

func TestContinuityWriterDoesNotResumeAtIDRInsidePES(t *testing.T) {
	packet := func(counter, nal byte) []byte {
		p := make([]byte, TSPacketSize)
		p[0], p[1], p[2], p[3] = 0x47, 0x01, 0, 0x10|counter
		copy(p[20:], []byte{0, 0, 1, nal})
		return p
	}
	var output bytes.Buffer
	w := NewContinuityWriter(&output)
	w.video[256], w.waitKey[256], w.dropping[256], w.params[256] = 0x1b, true, true, 3
	w.knownParams[256] = true
	w.BeginSession()
	w.params[256] = 3
	_, _ = w.Write(packet(0, 5))
	if got := output.Bytes(); got[1] != 0x1f {
		t.Fatal("IDR inside an incomplete PES was emitted")
	}
}

func TestContinuityWriterWaitsForCompleteIDRPES(t *testing.T) {
	packet := func(counter, nal byte) []byte {
		p := make([]byte, TSPacketSize)
		p[0], p[1], p[2], p[3] = 0x47, 0x41, 0, 0x10|counter
		copy(p[4:], []byte{0, 0, 1, 0xe0})
		copy(p[20:], []byte{0, 0, 1, nal})
		return p
	}
	var output bytes.Buffer
	w := NewContinuityWriter(&output)
	w.video[256], w.waitKey[256], w.waitVideo, w.params[256] = 0x1b, true, true, 3
	w.knownParams[256] = true
	_, _ = w.Write(packet(0, 5))
	if got := output.Bytes(); got[1]&0x1f != 0x1f {
		t.Fatal("incomplete IDR PES was emitted before its end was known")
	}
	_, _ = w.Write(packet(1, 1))
	if got := output.Bytes(); got[len(got)-2*TSPacketSize+1]&0x1f != 1 {
		t.Fatal("complete IDR PES was not replayed at the next PES boundary")
	}
}

func TestContinuityWriterBoundsReplaySuppression(t *testing.T) {
	packet := func(counter byte, pts uint64) []byte {
		p := make([]byte, TSPacketSize)
		p[0], p[1], p[2], p[3] = 0x47, 0x41, 0, 0x10|counter
		copy(p[4:], []byte{0, 0, 1, 0xe0, 0, 0, 0x80, 0x80, 5})
		writeTimestamp(p[13:18], pts)
		return p
	}
	var output bytes.Buffer
	w := NewContinuityWriter(&output)
	w.BeginSession()
	_, _ = w.Write(packet(0, 2000))
	w.BeginSession()
	for i := 0; i < 51; i++ {
		_, _ = w.Write(packet(byte(i+1)&0x0f, 1900))
	}
	last := output.Bytes()[51*TSPacketSize:]
	if last[1]&0x1f != 1 {
		t.Fatalf("replay suppression remained active after its frame budget: pid=%x wait=%v budget=%d", last[1:3], w.waitPTS[256], w.resumeMax[256])
	}
}

func TestContinuityWriterHoldsAudioWhileWaitingForVideoIDR(t *testing.T) {
	packet := func(pid uint16, counter, nal byte) []byte {
		p := make([]byte, TSPacketSize)
		p[0], p[1], p[2], p[3] = 0x47, byte(pid>>8)|0x40, byte(pid), 0x10|counter
		if nal != 0 {
			copy(p[20:], []byte{0, 0, 1, nal})
		}
		return p
	}
	var output bytes.Buffer
	w := NewContinuityWriter(&output)
	w.video[256] = 0x1b
	w.BeginSession()
	w.params[256] = 3
	w.knownParams[256] = true
	_, _ = w.Write(packet(256, 0, 5))
	w.BeginSession()
	_, _ = w.Write(packet(257, 0, 0))
	w.params[256] = 3
	w.knownParams[256] = true
	_, _ = w.Write(packet(256, 1, 5))
	_, _ = w.Write(packet(256, 2, 1))
	_, _ = w.Write(packet(257, 1, 0))
	got := output.Bytes()
	if got[TSPacketSize+1] != 0x1f {
		t.Fatal("audio escaped while video waited for an IDR")
	}
	if got[len(got)-TSPacketSize+1]&0x1f != 1 {
		t.Fatal("audio did not resume after video IDR")
	}
}

func TestContinuityWriterDoesNotRebaseFromHeldAudio(t *testing.T) {
	packet := make([]byte, TSPacketSize)
	packet[0], packet[1], packet[2], packet[3] = 0x47, 0x41, 1, 0x10
	copy(packet[4:], []byte{0, 0, 1, 0xc0, 0, 0, 0x80, 0x80, 5})
	writeTimestamp(packet[13:18], 10_000)
	var output bytes.Buffer
	w := NewContinuityWriter(&output)
	w.video[256] = 0x1b
	w.started = true
	w.beginDiscontinuity()
	_, _ = w.Write(packet)
	if w.offsetOK {
		t.Fatal("audio changed the global clock before video recovery")
	}
}

func TestContinuityWriterWritesPCRHeartbeatWhileWaitingForIDR(t *testing.T) {
	var output bytes.Buffer
	w := NewContinuityWriter(&output)
	w.video[256], w.waitVideo = 0x1b, true
	w.next[256] = 7
	w.session = 1
	w.emittedPCR, w.haveEmittedPCR = 10_000, true
	w.emittedPCRAt = time.Now().Add(-time.Second)
	w.resumeOut[256] = 20_000
	if err := w.writeHeartbeat(); err != nil {
		t.Fatal(err)
	}
	packet := output.Bytes()
	pcr, ok := readPCR(packet)
	if !ok || pcr < 100_000 || pcr > 101_000 {
		t.Fatalf("heartbeat PCR=%d ok=%v", pcr, ok)
	}
	if pid := uint16(packet[1]&0x1f)<<8 | uint16(packet[2]); pid != 256 || packet[3]&0x10 != 0 || packet[5]&0x80 == 0 {
		t.Fatalf("heartbeat header=%x", packet[:6])
	}
	if w.resumeOut[256] < 110_000 || w.resumeOut[256] > 111_000 {
		t.Fatalf("resume PTS did not follow heartbeat PCR: %d", w.resumeOut[256])
	}
}
