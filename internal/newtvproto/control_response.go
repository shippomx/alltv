package newtvproto

import (
	"bytes"
	"encoding/binary"
)

type LoadingResponse struct {
	Status        byte
	Code          byte
	Value         uint32
	Available     bool
	Flags         byte
	Window        uint16
	ServerTime    uint32
	StreamID      uint32
	StartSequence uint32
	EndSequence   uint32
}

type RangeResponse struct {
	Status        byte
	Code          byte
	Value         uint32
	Available     bool
	Flags         byte
	Window        uint16
	ServerTime    uint32
	StreamID      uint32
	StartSequence uint32
	Tail          uint16
	FirstSequence uint32
	EndSequence   uint32
}

func DecodeLoadingResponse(datagram, expectedChannel []byte) (LoadingResponse, bool) {
	plain, ok := decodeControlResponse(datagram, expectedChannel)
	if !ok || len(plain) != 50 {
		return LoadingResponse{}, false
	}
	return LoadingResponse{
		Status:        plain[24],
		Code:          plain[25],
		Value:         binary.BigEndian.Uint32(plain[26:30]),
		Available:     plain[30]&0x55 == 0x55,
		Flags:         plain[31],
		Window:        binary.BigEndian.Uint16(plain[32:34]),
		ServerTime:    binary.BigEndian.Uint32(plain[34:38]),
		StreamID:      binary.BigEndian.Uint32(plain[38:42]),
		StartSequence: binary.BigEndian.Uint32(plain[42:46]),
		EndSequence:   binary.BigEndian.Uint32(plain[46:50]),
	}, true
}

func DecodeRangeResponse(datagram, expectedChannel []byte) (RangeResponse, bool) {
	plain, ok := decodeControlResponse(datagram, expectedChannel)
	if !ok || len(plain) < 56 {
		return RangeResponse{}, false
	}
	return RangeResponse{
		Status:        plain[24],
		Code:          plain[25],
		Value:         binary.BigEndian.Uint32(plain[26:30]),
		Available:     plain[30]&0x55 == 0x55,
		Flags:         plain[31],
		Window:        binary.BigEndian.Uint16(plain[32:34]),
		ServerTime:    binary.BigEndian.Uint32(plain[34:38]),
		StreamID:      binary.BigEndian.Uint32(plain[38:42]),
		StartSequence: binary.BigEndian.Uint32(plain[42:46]),
		Tail:          binary.BigEndian.Uint16(plain[46:48]),
		FirstSequence: binary.BigEndian.Uint32(plain[48:52]),
		EndSequence:   binary.BigEndian.Uint32(plain[52:56]),
	}, true
}

func decodeControlResponse(datagram, expectedChannel []byte) ([]byte, bool) {
	if len(datagram) < 12 || datagram[1]&0x55 != 0x55 {
		return nil, false
	}
	pivot, keyLen := int(datagram[2]&0x3f), int(datagram[5])
	if pivot < 6 || pivot >= len(datagram)-4 || keyLen < 16 {
		return nil, false
	}
	plain := make([]byte, len(datagram)-4)
	copy(plain, datagram[:pivot])
	copy(plain[pivot:], datagram[pivot+4:])
	keyOffset := int(binary.BigEndian.Uint16(datagram[3:5]) & 0x0fff)
	for i := 6; i < len(plain); i++ {
		plain[i] ^= playbackKey[keyOffset+(i-6)%keyLen]
	}
	if int(binary.BigEndian.Uint16(plain[6:8])) != len(datagram) || len(plain) < 24 {
		return nil, false
	}
	if len(expectedChannel) != 0 && (len(expectedChannel) != 16 || !bytes.Equal(plain[8:24], expectedChannel)) {
		return nil, false
	}
	return plain, true
}
