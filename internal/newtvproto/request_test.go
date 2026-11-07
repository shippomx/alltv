package newtvproto

import (
	"encoding/binary"
	"encoding/hex"
	"testing"
)

func TestEncodeDataRequest(t *testing.T) {
	request := DataRequest{StreamID: 0x12345678, Sequences: []uint32{7, 8, 9}}
	copy(request.Channel[:], "0123456789abcdef")
	copy(request.DeviceID[:], "authorized-device")
	copy(request.Extension[:], "authorized-session-extension")

	wire, err := EncodeDataRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	plain, checksum, ok := decodeRequest(wire)
	if !ok || len(wire) != 107 || plain[1]&0xaa != 0xaa || plain[12] != 0x33 || checksum != xorChecksum(plain) {
		t.Fatalf("ok=%v wire=%d type=%x checksum=%08x", ok, len(wire), plain[12], checksum)
	}
	if got := binary.BigEndian.Uint16(plain[29:31]); got != 3 {
		t.Fatalf("clip count=%d", got)
	}
	for i, want := range request.Sequences {
		offset := 31 + i*8
		if stream, sequence := binary.BigEndian.Uint32(plain[offset:offset+4]), binary.BigEndian.Uint32(plain[offset+4:offset+8]); stream != request.StreamID || sequence != want {
			t.Fatalf("clip %d stream=%x sequence=%d", i, stream, sequence)
		}
	}
}

func TestDecodeNativeLiveDataRequestFixture(t *testing.T) {
	wire, err := hex.DecodeString("23bae511bd69368b481e76fbc007d714733c64d1234c483953e1d92ebda56be22c19c298bd02d14d9f2c559ba80dfe4781531ed252ddae30987d04545cca331553bb0e8a56cb7c9013fd3375cf51a178a310398b71b7527232265fe2252e42928a0074e2663d3d0177560e")
	if err != nil {
		t.Fatal(err)
	}
	plain, _, ok := decodeRequest(wire)
	if !ok || binary.BigEndian.Uint16(plain[3:5]) != 0x11bd || binary.BigEndian.Uint32(plain[31:35]) != 1793926840 || binary.BigEndian.Uint32(plain[35:39]) != 0 {
		t.Fatalf("invalid native data request: ok=%v plain=%x", ok, plain)
	}
}

func TestEncodeDataRequestGeneratesHexNonce(t *testing.T) {
	wire, err := EncodeDataRequest(DataRequest{Sequences: []uint32{1}})
	if err != nil {
		t.Fatal(err)
	}
	plain, _, ok := decodeRequest(wire)
	if !ok {
		t.Fatal("could not decode request")
	}
	nonce := plain[len(plain)-32:]
	if _, err := hex.DecodeString(string(nonce)); err != nil {
		t.Fatalf("nonce %q is not hexadecimal: %v", nonce, err)
	}
}

func TestEncodeDataRequestRejectsEmpty(t *testing.T) {
	if _, err := EncodeDataRequest(DataRequest{}); err == nil {
		t.Fatal("accepted empty request")
	}
}

func TestEncodeControlRequests(t *testing.T) {
	request := ControlRequest{Counter: 12, Values: [3]uint32{3, 4, 5}, Decorated: true, FakeKey: 0xa2d0}
	copy(request.Channel[:], "0123456789abcdef")
	copy(request.DeviceID[:], "authorized-device")
	copy(request.Extension[:], "authorized-session-extension")
	for name, fixture := range map[string]struct {
		kind   byte
		encode func(ControlRequest) ([]byte, error)
	}{
		"range":   {0x21, EncodeRangeRequest},
		"loading": {0x22, EncodeLoadingRequest},
	} {
		t.Run(name, func(t *testing.T) {
			wire, err := fixture.encode(request)
			if err != nil {
				t.Fatal(err)
			}
			plain, checksum, ok := decodeRequest(wire)
			if !ok || len(wire) != 98 || plain[0] != fixture.kind || plain[1]&0xaa != 0xaa || checksum != xorChecksum(plain) || plain[13]&0xaa != 0xaa {
				t.Fatalf("ok=%v wire=%d checksum=%08x marker=%02x", ok, len(wire), checksum, plain[13])
			}
			if counter := binary.BigEndian.Uint32(plain[14:18]); counter != request.Counter {
				t.Fatalf("counter=%d", counter)
			}
			if got := binary.BigEndian.Uint16(wire[3:5]) & 0x0fff; got != 0x2d0 {
				t.Fatalf("key offset=%03x", got)
			}
			for i, want := range request.Values {
				if got := binary.BigEndian.Uint32(plain[50+i*4 : 54+i*4]); got != want {
					t.Fatalf("value[%d]=%d", i, got)
				}
			}
		})
	}
}

func decodeRequest(wire []byte) ([]byte, uint32, bool) {
	if len(wire) < 17 {
		return nil, 0, false
	}
	pivot, keyLen := int(wire[2]&0x3f), int(wire[5])
	if pivot < 6 || pivot > len(wire)-4 || keyLen < 16 {
		return nil, 0, false
	}
	checksum := binary.BigEndian.Uint32(wire[pivot : pivot+4])
	plain := append(append([]byte(nil), wire[:pivot]...), wire[pivot+4:]...)
	keyOffset := int(binary.BigEndian.Uint16(wire[3:5]) & 0x0fff)
	for i := 6; i < len(plain); i++ {
		plain[i] ^= playbackKey[keyOffset+(i-6)%keyLen]
	}
	return plain, checksum, int(binary.BigEndian.Uint16(plain[10:12])) == len(wire)
}
