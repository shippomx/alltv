package newtvproto

import (
	"encoding/binary"
	"testing"
)

func TestDecodeNativeLiveRangeFixture(t *testing.T) {
	wire := mustHex(t, "21effa72d099c6fed1b94d30f1c2c901d9092cd544ab9f2320b8d266de6b0889d64946062fbfda4698aa294d96f40603740d9058c4b0205a0237883d7d75ad3e4cd271712c236a3152169c6abdcba04b9052116fe891a50695c2da4272139f49843e")
	plain, _, ok := decodeRequest(wire)
	if !ok || len(plain) != 94 || plain[0] != 0x21 {
		t.Fatal("native live Range did not decode")
	}
	if got := string(plain[34:46]); got != "261105859864" {
		t.Fatalf("device ID = %q", got)
	}
	if got := binary.BigEndian.Uint32(plain[50:54]); got != 453145100 {
		t.Fatalf("client ID = %d", got)
	}
	if got := binary.BigEndian.Uint32(plain[54:58]); got != liveSessionFakeKey {
		t.Fatalf("session key = %08x", got)
	}
	if plain[62] != 6 || binary.BigEndian.Uint16(plain[67:69]) != 6017 {
		t.Fatalf("PelosiExt = %x", plain[62:69])
	}
}
