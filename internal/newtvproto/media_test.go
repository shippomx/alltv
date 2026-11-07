package newtvproto

import (
	"encoding/binary"
	"testing"
)

func playbackDatagram(t *testing.T, mode byte) ([]byte, []byte) {
	return playbackDatagramFor(t, mode, 99, 42)
}

func playbackDatagramFor(t *testing.T, mode byte, streamID, sequence uint32) ([]byte, []byte) {
	t.Helper()
	channel := []byte("0123456789abcdef")
	payload := make([]byte, MediaPayloadSize)
	for packet := 0; packet < TSPacketsPerFrame; packet++ {
		payload[packet*TSPacketSize] = 0x47
		payload[packet*TSPacketSize+1] = byte(packet)
	}
	if mode != 0 && !directExchange(payload, mode) {
		t.Fatal("invalid test exchange mode")
	}

	header := make([]byte, playbackPlainHeader)
	header[0], header[1], header[2], header[5] = 0x23, 0x55, 12, 16
	binary.BigEndian.PutUint16(header[3:5], 0x123)
	binary.BigEndian.PutUint16(header[6:8], uint16(playbackWireHeader+len(payload)))
	copy(header[8:24], channel)
	header[25] = mode
	binary.BigEndian.PutUint32(header[26:30], streamID)
	binary.BigEndian.PutUint32(header[30:34], sequence)
	binary.BigEndian.PutUint16(header[34:36], uint16(len(payload)))
	keyOffset := int(binary.BigEndian.Uint16(header[3:5]) & 0x0fff)
	for i := 0; i < 30; i++ {
		header[6+i] ^= playbackKey[keyOffset+i%int(header[5])]
	}

	pivot := int(header[2] & 0x3f)
	datagram := make([]byte, playbackWireHeader+len(payload))
	copy(datagram, header[:pivot])
	copy(datagram[pivot+4:playbackWireHeader], header[pivot:])
	copy(datagram[playbackWireHeader:], payload)
	return datagram, channel
}

func TestDecodePlayback(t *testing.T) {
	datagram, channel := playbackDatagram(t, 0x13)
	got, ok := DecodePlayback(datagram, channel)
	if !ok || got.StreamID != 99 || got.Sequence != 42 || len(got.Payload) != MediaPayloadSize {
		t.Fatalf("ok=%v stream=%d sequence=%d payload=%d", ok, got.StreamID, got.Sequence, len(got.Payload))
	}
	for packet := 0; packet < TSPacketsPerFrame; packet++ {
		if marker := got.Payload[packet*TSPacketSize+1]; marker != byte(packet) {
			t.Fatalf("packet %d marker=%d", packet, marker)
		}
	}
}

func TestDecodePlaybackRejectsInvalidFrames(t *testing.T) {
	datagram, channel := playbackDatagram(t, 0)
	badChannel := append([]byte(nil), channel...)
	badChannel[0]++
	if _, ok := DecodePlayback(datagram, badChannel); ok {
		t.Fatal("accepted wrong channel")
	}

	datagram[playbackWireHeader+TSPacketSize] = 0
	if _, ok := DecodePlayback(datagram, channel); ok {
		t.Fatal("accepted invalid MPEG-TS sync")
	}
}
