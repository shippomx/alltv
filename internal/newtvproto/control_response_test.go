package newtvproto

import (
	"encoding/binary"
	"encoding/hex"
	"testing"
)

func TestDecodeLoadingResponse(t *testing.T) {
	plain, channel := controlPlain(50)
	plain[24], plain[25], plain[30], plain[31] = 0, 2, 0x55, 3
	binary.BigEndian.PutUint16(plain[32:34], 235)
	binary.BigEndian.PutUint32(plain[34:38], 1000)
	binary.BigEndian.PutUint32(plain[38:42], 99)
	binary.BigEndian.PutUint32(plain[42:46], 7)
	binary.BigEndian.PutUint32(plain[46:50], 88)

	response, ok := DecodeLoadingResponse(encodeControlResponse(plain, 17), channel)
	if !ok || !response.Available || response.StreamID != 99 || response.StartSequence != 7 || response.EndSequence != 88 {
		t.Fatalf("ok=%v response=%+v", ok, response)
	}
}

func TestDecodeLiveLoadingClips(t *testing.T) {
	plain, channel := controlPlain(64)
	plain[0], plain[24] = 0x22, 0
	binary.BigEndian.PutUint16(plain[46:48], 2)
	binary.BigEndian.PutUint32(plain[48:52], 120)
	binary.BigEndian.PutUint32(plain[52:56], 2)
	binary.BigEndian.PutUint32(plain[56:60], 140)
	binary.BigEndian.PutUint32(plain[60:64], 3)
	response, ok := DecodeLoadingResponse(encodeControlResponse(plain, 17), channel)
	if !ok || len(response.Clips) != 2 || response.Clips[0] != (ClipInfo{120, 2}) || response.Clips[1] != (ClipInfo{140, 3}) {
		t.Fatalf("ok=%v response=%+v", ok, response)
	}
}

func TestDecodeRangeResponse(t *testing.T) {
	plain, channel := controlPlain(66)
	plain[0] = 0x21
	binary.BigEndian.PutUint32(plain[38:42], 123)
	binary.BigEndian.PutUint32(plain[42:46], 10)
	binary.BigEndian.PutUint16(plain[46:48], 4)
	binary.BigEndian.PutUint16(plain[48:50], 2)
	binary.BigEndian.PutUint32(plain[50:54], 123)
	binary.BigEndian.PutUint32(plain[54:58], 10)
	binary.BigEndian.PutUint32(plain[58:62], 124)
	binary.BigEndian.PutUint32(plain[62:66], 100)

	response, ok := DecodeRangeResponse(encodeControlResponse(plain, 23), channel)
	if !ok || response.StreamID != 123 || response.Tail != 4 || len(response.Clips) != 2 || response.Clips[0] != (ClipInfo{123, 10}) || response.Clips[1] != (ClipInfo{124, 100}) {
		t.Fatalf("ok=%v response=%+v", ok, response)
	}
}

func TestDecodeExtendedNativeRangeResponseFixture(t *testing.T) {
	wire, err := hex.DecodeString("217dc92d83c954da330000a679670feb20e4ed9c1b5d70d630a252aa71b8ec3a2bfded6745cad7e127e52172108d538f2cecd3743fab51f879741ac70a5cfa480880dc393c4ba37822d546853b24")
	if err != nil {
		t.Fatal(err)
	}
	response, ok := DecodeRangeResponse(wire, mustHex(t, "50a2013a0000b67c0970aaa14fd56f75"))
	if !ok || !response.Available || response.Flags != 32 || response.Window != 436 || response.ServerTime != 1793926909 || response.StreamID != 1793926840 || response.StartSequence != 0 || len(response.Clips) != 3 || response.Clips[0] != (ClipInfo{1793926840, 4823}) || response.Clips[1] != (ClipInfo{1793926860, 5653}) || response.Clips[2] != (ClipInfo{1793926880, 3320}) {
		t.Fatalf("ok=%v response=%+v", ok, response)
	}
}

func mustHex(t *testing.T, value string) []byte {
	t.Helper()
	decoded, err := hex.DecodeString(value)
	if err != nil {
		t.Fatal(err)
	}
	return decoded
}

func TestDecodeControlResponseRejectsWrongChannel(t *testing.T) {
	plain, _ := controlPlain(50)
	if _, ok := DecodeLoadingResponse(encodeControlResponse(plain, 9), []byte("wrong-channel-id")); ok {
		t.Fatal("accepted wrong channel")
	}
}

func controlPlain(size int) ([]byte, []byte) {
	plain := make([]byte, size)
	channel := []byte("0123456789abcdef")
	plain[0], plain[1], plain[5] = 0x23, 0x55, 16
	copy(plain[8:24], channel)
	return plain, channel
}

func encodeControlResponse(plain []byte, pivot int) []byte {
	plain = append([]byte(nil), plain...)
	plain[2] = byte(pivot)
	binary.BigEndian.PutUint16(plain[3:5], 0x321)
	binary.BigEndian.PutUint16(plain[6:8], uint16(len(plain)+4))
	for i := 6; i < len(plain); i++ {
		plain[i] ^= playbackKey[0x321+(i-6)%int(plain[5])]
	}
	wire := make([]byte, len(plain)+4)
	copy(wire, plain[:pivot])
	copy(wire[pivot+4:], plain[pivot:])
	return wire
}
