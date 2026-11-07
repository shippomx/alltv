package newtvproto

import (
	"bytes"
	"testing"
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
	_, _ = w.Write(packet(3, 5, 2200))
	got := output.Bytes()
	if got[TSPacketSize+1] != 0x1f || got[2*TSPacketSize+1] != 0x1f {
		t.Fatal("replayed or dependent H.264 frame was not dropped")
	}
	if got[3*TSPacketSize+1]&0x1f != 1 || got[3*TSPacketSize+3]&0x0f != 1 {
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
	_, _ = w.Write(packet(3, 5))
	got := output.Bytes()
	if got[TSPacketSize+1] != 0x1f {
		t.Fatal("dependent frame after a PES-boundary gap was not dropped")
	}
	if got[2*TSPacketSize+1]&0x1f != 1 {
		t.Fatal("IDR after a PES-boundary gap did not resume output")
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
	_, _ = w.Write(packet(2, 19))
	got := output.Bytes()
	if got[TSPacketSize+1] != 0x1f {
		t.Fatal("HEVC CRA resumed without its prior reference pictures")
	}
	if got[2*TSPacketSize+1]&0x1f != 1 {
		t.Fatal("HEVC IDR did not resume output")
	}
}

func TestContinuityWriterPassesHEVCParameterSetsWhileWaitingForIDR(t *testing.T) {
	packet := func(counter, nalType byte) []byte {
		p := make([]byte, TSPacketSize)
		p[0], p[1], p[2], p[3] = 0x47, 0x41, 0, 0x10|counter
		copy(p[20:], []byte{0, 0, 1, nalType << 1})
		return p
	}
	var output bytes.Buffer
	w := NewContinuityWriter(&output)
	w.video[256], w.waitKey[256] = 0x24, true
	w.BeginSession()
	_, _ = w.Write(packet(0, 32))
	_, _ = w.Write(packet(1, 33))
	_, _ = w.Write(packet(2, 34))
	_, _ = w.Write(packet(3, 1))
	_, _ = w.Write(packet(4, 19))
	got := output.Bytes()
	if got[1]&0x1f != 1 || got[TSPacketSize+1]&0x1f != 1 || got[2*TSPacketSize+1]&0x1f != 1 || got[3*TSPacketSize+1] != 0x1f || got[4*TSPacketSize+1]&0x1f != 1 {
		t.Fatal("HEVC startup did not preserve parameter sets and wait for IDR")
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
	got := output.Bytes()
	if got[1] != 0x1f || got[2*TSPacketSize+1] != 0x1f {
		t.Fatal("dependent media escaped before PMT/keyframe")
	}
	if got[5*TSPacketSize+1]&0x1f != 1 {
		t.Fatal("IDR after PMT did not resume output")
	}
}

func TestContinuityWriterResumesAtIDRInsidePES(t *testing.T) {
	packet := func(counter, nal byte) []byte {
		p := make([]byte, TSPacketSize)
		p[0], p[1], p[2], p[3] = 0x47, 0x01, 0, 0x10|counter
		copy(p[20:], []byte{0, 0, 1, nal})
		return p
	}
	var output bytes.Buffer
	w := NewContinuityWriter(&output)
	w.video[256], w.waitKey[256], w.dropping[256], w.params[256] = 0x1b, true, true, 3
	w.BeginSession()
	w.params[256] = 3
	_, _ = w.Write(packet(0, 5))
	if got := output.Bytes(); got[1]&0x1f != 1 {
		t.Fatal("IDR inside PES remained dropped")
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
