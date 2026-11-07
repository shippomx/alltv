package newtvproto

import (
	"encoding/binary"
	"testing"
)

func TestEncodeVODRequestsMatchNativeLayouts(t *testing.T) {
	var channel, device [16]byte
	copy(channel[:], "0123456789abcdef")
	copy(device[:], "authorized")
	rangeWire, err := EncodeVODRangeRequest(channel, device, 42, 3, 7, 0x12345678)
	if err != nil {
		t.Fatal(err)
	}
	rangePlain, _, ok := decodeVODRequestFixture(rangeWire)
	if !ok || len(rangeWire) != 92 || rangeWire[0] != 0x31 || rangeWire[1]&0xaa != 0xaa || rangePlain[13]&0xaa != 0xaa || binary.BigEndian.Uint32(rangePlain[50:54]) != 42 || binary.BigEndian.Uint16(rangePlain[54:56]) != 0 || binary.BigEndian.Uint16(rangePlain[56:58]) != 7 || binary.BigEndian.Uint32(rangePlain[58:62]) != 0x12345678 {
		t.Fatalf("range ok=%v len=%d plain=%x", ok, len(rangeWire), rangePlain)
	}
	if rangePlain[62] != 26 || !isLowerHex(rangePlain[63:]) {
		t.Fatalf("invalid range nonce: %x", rangePlain[62:])
	}
	loadingWire, err := EncodeVODLoadingRequest(channel, device, 42, 4, 9, 7, 0x12345678)
	if err != nil {
		t.Fatal(err)
	}
	loadingPlain, _, ok := decodeVODRequestFixture(loadingWire)
	if !ok || len(loadingWire) != 94 || loadingWire[0] != 0x32 || binary.BigEndian.Uint32(loadingPlain[54:58]) != 9 || binary.BigEndian.Uint16(loadingPlain[58:60]) != 7 || binary.BigEndian.Uint32(loadingPlain[60:64]) != 0x12345678 {
		t.Fatalf("loading ok=%v len=%d plain=%x", ok, len(loadingWire), loadingPlain)
	}
	if loadingPlain[64] != 26 || !isLowerHex(loadingPlain[65:]) {
		t.Fatalf("invalid loading nonce: %x", loadingPlain[64:])
	}
	dataWire, err := EncodeVODDataRequest(channel, device, []uint32{1, 2, 3}, 7, 0x12345678)
	if err != nil {
		t.Fatal(err)
	}
	dataPlain, _, ok := decodeVODRequestFixture(dataWire)
	if !ok || len(dataWire) != 95 || dataWire[0] != 0x33 || binary.BigEndian.Uint32(dataPlain[39:43]) != 3 || binary.BigEndian.Uint16(dataPlain[59:61]) != 7 || binary.BigEndian.Uint32(dataPlain[61:65]) != 0x12345678 {
		t.Fatalf("data ok=%v len=%d plain=%x", ok, len(dataWire), dataPlain)
	}
	if dataPlain[65] != 26 || !isLowerHex(dataPlain[66:]) {
		t.Fatalf("invalid data nonce: %x", dataPlain[65:])
	}
}

func isLowerHex(value []byte) bool {
	for _, b := range value {
		if b < '0' || b > '9' && (b < 'a' || b > 'f') {
			return false
		}
	}
	return len(value) > 0
}
