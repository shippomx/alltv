package newtvproto

import (
	"bytes"
	"encoding/binary"
)

const (
	TSPacketSize        = 188
	TSPacketsPerFrame   = 7
	MediaPayloadSize    = TSPacketSize * TSPacketsPerFrame
	playbackWireHeader  = 40
	playbackPlainHeader = 36
)

type PlaybackFrame struct {
	StreamID uint32
	Sequence uint32
	Payload  []byte
}

// DecodePlayback validates and restores one NewTV live-playback UDP frame.
func DecodePlayback(datagram, expectedChannel []byte) (PlaybackFrame, bool) {
	if len(datagram) < playbackWireHeader || datagram[1]&0x55 != 0x55 {
		return PlaybackFrame{}, false
	}
	pivot, keyLen := int(datagram[2]&0x3f), int(datagram[5])
	if pivot < 6 || pivot > 35 || keyLen < 16 {
		return PlaybackFrame{}, false
	}

	header := make([]byte, playbackPlainHeader)
	copy(header, datagram[:pivot])
	copy(header[pivot:], datagram[pivot+4:playbackWireHeader])
	keyOffset := int(binary.BigEndian.Uint16(datagram[3:5]) & 0x0fff)
	for i := 0; i < 30; i++ {
		header[6+i] ^= playbackKey[keyOffset+i%keyLen]
	}

	if int(binary.BigEndian.Uint16(header[6:8])) != len(datagram) || header[24] != 0 {
		return PlaybackFrame{}, false
	}
	if len(expectedChannel) != 0 && (len(expectedChannel) != 16 || !bytes.Equal(header[8:24], expectedChannel)) {
		return PlaybackFrame{}, false
	}
	payloadLen := int(binary.BigEndian.Uint16(header[34:36]))
	if payloadLen != len(datagram)-playbackWireHeader || payloadLen%TSPacketSize != 0 {
		return PlaybackFrame{}, false
	}
	payload := append([]byte(nil), datagram[playbackWireHeader:]...)
	if !directExchange(payload, header[25]) {
		return PlaybackFrame{}, false
	}
	for offset := 0; offset < len(payload); offset += TSPacketSize {
		if payload[offset] != 0x47 {
			return PlaybackFrame{}, false
		}
	}
	return PlaybackFrame{
		StreamID: binary.BigEndian.Uint32(header[26:30]),
		Sequence: binary.BigEndian.Uint32(header[30:34]),
		Payload:  payload,
	}, true
}

func directExchange(payload []byte, mode byte) bool {
	if mode == 0 {
		return true
	}
	a, b := int(mode>>4)-1, int(mode&0x0f)-1
	packets := len(payload) / TSPacketSize
	if a < 0 || b < 0 || a >= packets || b >= packets || a == b {
		return false
	}
	left, right := payload[a*TSPacketSize:(a+1)*TSPacketSize], payload[b*TSPacketSize:(b+1)*TSPacketSize]
	for i := range left {
		left[i], right[i] = right[i], left[i]
	}
	return true
}

// MediaPayload keeps the original payload-only API for callers that do not need ordering metadata.
func MediaPayload(datagram []byte) ([]byte, bool) {
	frame, ok := DecodePlayback(datagram, nil)
	return frame.Payload, ok
}
