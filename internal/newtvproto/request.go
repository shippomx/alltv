package newtvproto

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

type DataRequest struct {
	Channel   [16]byte
	StreamID  uint32
	Sequences []uint32
	DeviceID  [16]byte
	Extension [32]byte
	FakeKey   uint16
}

type ControlRequest struct {
	Channel   [16]byte
	DeviceID  [16]byte
	Extension [32]byte
	Counter   uint32
	Values    [3]uint32
	Decorated bool
	FakeKey   uint16
}

// EncodeDataRequest builds a Playback V3 data request using caller-supplied authorized session fields.
func EncodeDataRequest(request DataRequest) ([]byte, error) {
	if len(request.Sequences) == 0 || len(request.Sequences) > 8180 {
		return nil, errors.New("data request must contain 1-8180 sequences")
	}
	if err := fillRequestNonce(&request.Extension); err != nil {
		return nil, err
	}
	plainLen := 31 + len(request.Sequences)*8 + len(request.DeviceID) + len(request.Extension)
	wireLen := plainLen + 4
	if wireLen > 65535 {
		return nil, errors.New("data request is too large")
	}

	plain := make([]byte, plainLen)
	copy(plain[13:29], request.Channel[:])
	binary.BigEndian.PutUint16(plain[29:31], uint16(len(request.Sequences)))
	offset := 31
	for _, sequence := range request.Sequences {
		binary.BigEndian.PutUint32(plain[offset:offset+4], request.StreamID)
		binary.BigEndian.PutUint32(plain[offset+4:offset+8], sequence)
		offset += 8
	}
	copy(plain[offset:offset+16], request.DeviceID[:])
	copy(plain[offset+16:], request.Extension[:])
	return encodeRequestWithFakeKey(plain, request.FakeKey)
}

func EncodeRangeRequest(request ControlRequest) ([]byte, error) {
	return encodeControlRequest(0x21, request)
}

func EncodeLoadingRequest(request ControlRequest) ([]byte, error) {
	return encodeControlRequest(0x22, request)
}

func encodeControlRequest(kind byte, request ControlRequest) ([]byte, error) {
	if err := fillRequestNonce(&request.Extension); err != nil {
		return nil, err
	}
	plain := make([]byte, 94)
	var marker [1]byte
	if _, err := rand.Read(marker[:]); err != nil {
		return nil, err
	}
	plain[13] = marker[0]
	if request.Decorated {
		plain[13] |= 0xaa
	}
	binary.BigEndian.PutUint32(plain[14:18], request.Counter)
	copy(plain[18:34], request.Channel[:])
	copy(plain[34:50], request.DeviceID[:])
	for i, value := range request.Values {
		binary.BigEndian.PutUint32(plain[50+i*4:54+i*4], value)
	}
	copy(plain[62:94], request.Extension[:])
	return encodeRequestKindFlagsWithFakeKey(kind, 0xaa, plain, int(request.FakeKey))
}

func fillRequestNonce(value *[32]byte) error {
	if *value != ([32]byte{}) {
		return nil
	}
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return fmt.Errorf("generate NewTV request nonce: %w", err)
	}
	hex.Encode(value[:], raw[:])
	return nil
}

func encodeRequest(plain []byte) ([]byte, error) {
	return encodeRequestKind(0x23, plain)
}

func encodeRequestWithFakeKey(plain []byte, fakeKey uint16) ([]byte, error) {
	return encodeRequestKindFlagsWithFakeKey(0x23, 0xaa, plain, int(fakeKey))
}

func encodeRequestKind(kind byte, plain []byte) ([]byte, error) {
	return encodeRequestKindFlags(kind, 0xaa, plain)
}

func encodeRequestKindFlags(kind, flags byte, plain []byte) ([]byte, error) {
	return encodeRequestKindFlagsWithFakeKey(kind, flags, plain, -1)
}

func encodeRequestKindFlagsWithFakeKey(kind, flags byte, plain []byte, fakeKey int) ([]byte, error) {
	wireLen := len(plain) + 4
	if len(plain) < 13 || wireLen > 65535 {
		return nil, errors.New("request size is invalid")
	}
	var random [6]byte
	if _, err := rand.Read(random[:]); err != nil {
		return nil, err
	}
	pivot := 6 + int(random[0])%min(58, wireLen-10)
	if fakeKey < 0 {
		fakeKey = int(binary.BigEndian.Uint16(random[1:3]))
	}
	keyOffset := fakeKey & 0x0fff
	keyLen := 16 + int(random[3])%240
	plain[0], plain[1], plain[2], plain[5] = kind, random[4]|flags, byte(pivot)|((random[5]%255)&3)<<6, byte(keyLen)
	binary.BigEndian.PutUint16(plain[3:5], uint16(fakeKey))
	binary.BigEndian.PutUint32(plain[6:10], uint32(time.Now().Unix()))
	binary.BigEndian.PutUint16(plain[10:12], uint16(wireLen))
	plain[12] = 0x33
	checksum := xorChecksum(plain)
	for i := 6; i < len(plain); i++ {
		plain[i] ^= playbackKey[keyOffset+(i-6)%keyLen]
	}
	wire := make([]byte, wireLen)
	copy(wire, plain[:pivot])
	binary.BigEndian.PutUint32(wire[pivot:pivot+4], checksum)
	copy(wire[pivot+4:], plain[pivot:])
	return wire, nil
}

func xorChecksum(p []byte) uint32 {
	var sum uint32
	for len(p) >= 4 {
		sum ^= binary.LittleEndian.Uint32(p[:4])
		p = p[4:]
	}
	var tail [4]byte
	copy(tail[:], p)
	return sum ^ binary.LittleEndian.Uint32(tail[:])
}
