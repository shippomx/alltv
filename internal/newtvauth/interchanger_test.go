package newtvauth

import (
	"bytes"
	"encoding/hex"
	"testing"
)

func TestInterchangerFrameRoundTrip(t *testing.T) {
	var id [32]byte
	copy(id[:], "df523eb75ab54d9ba7216fcdc13bba1d")
	want := InterchangerFrame{Version: 1, Kind: 0xff, KeySelector: 0x49, IVSelector: 0x36, RequestID: id, Sequence: 1, Payload: []byte("request")}
	packet, err := EncodeInterchangerFrame(want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeInterchangerFrame(packet)
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != want.Version || got.Kind != want.Kind || got.KeySelector != want.KeySelector || got.IVSelector != want.IVSelector || got.RequestID != want.RequestID || got.Sequence != want.Sequence || !bytes.Equal(got.Payload, want.Payload) {
		t.Fatalf("frame = %#v", got)
	}
	packet[20] ^= 1
	if _, err := DecodeInterchangerFrame(packet); err == nil {
		t.Fatal("expected checksum error")
	}
}

func TestDecodeInterchangerPCAPFixture(t *testing.T) {
	packet, err := hex.DecodeString("aa9b35c801ff49366466353233656237356162353464396261373231366663646331336262613164010000005bc3fd7028d9d22231e97250ec947af904040404")
	if err != nil {
		t.Fatal(err)
	}
	frame, err := DecodeInterchangerFrame(packet)
	if err != nil {
		t.Fatal(err)
	}
	if frame.Version != 1 || frame.Kind != 0xff || frame.KeySelector != 0x49 || frame.IVSelector != 0x36 || string(frame.RequestID[:]) != "df523eb75ab54d9ba7216fcdc13bba1d" || frame.Sequence != 0x01000000 || hex.EncodeToString(frame.Payload) != "5bc3fd7028d9d22231e97250ec947af9" {
		t.Fatalf("fixture frame = %#v", frame)
	}
	plain, err := frame.DecryptPayload()
	if err != nil {
		t.Fatal(err)
	}
	if string(plain) != "896B88.6AECA3A7" {
		t.Fatalf("fixture plaintext = %q", plain)
	}
	encrypted, err := EncryptInterchangerPayload(frame.KeySelector, frame.IVSelector, plain)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(encrypted, frame.Payload) {
		t.Fatalf("fixture re-encryption = %x", encrypted)
	}
}

func TestInterchangerFrameRejectsPadding(t *testing.T) {
	packet, err := EncodeInterchangerFrame(InterchangerFrame{Version: 1})
	if err != nil {
		t.Fatal(err)
	}
	packet[len(packet)-1] = 0
	var checksum uint32
	for i := 4; i < len(packet); i += 4 {
		checksum ^= uint32(packet[i])<<24 | uint32(packet[i+1])<<16 | uint32(packet[i+2])<<8 | uint32(packet[i+3])
	}
	packet[0], packet[1], packet[2], packet[3] = byte(checksum>>24), byte(checksum>>16), byte(checksum>>8), byte(checksum)
	if _, err := DecodeInterchangerFrame(packet); err == nil {
		t.Fatal("expected padding error")
	}
}

func TestInterchangerPlainPayload(t *testing.T) {
	frame := InterchangerFrame{KeySelector: 0xff, IVSelector: 0xff, Payload: []byte{1, 2, 3}}
	plain, err := frame.DecryptPayload()
	if err != nil || !bytes.Equal(plain, frame.Payload) {
		t.Fatalf("plain = %x, err = %v", plain, err)
	}
}

func TestInterchangerJSON(t *testing.T) {
	var id [32]byte
	copy(id[:], "0123456789abcdef0123456789abcdef")
	packet, err := EncodeInterchangerJSON(3, 0xe8, 0x64, id, 42, map[string]any{"blocks": []int{1, 2}})
	if err != nil {
		t.Fatal(err)
	}
	frame, err := DecodeInterchangerFrame(packet)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := frame.DecryptPayload()
	if err != nil {
		t.Fatal(err)
	}
	if string(plain) != "{\"blocks\":[1,2]}\n" {
		t.Fatalf("JSON = %q", plain)
	}
}
