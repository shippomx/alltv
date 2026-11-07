package newtvproto

import (
	"encoding/binary"
	"encoding/hex"
	"testing"
)

func TestDecodeNativeVODRangeFixture(t *testing.T) {
	for _, encoded := range []string{
		"31ba0df1d96b107456ab55ad76db28d81f8046478153175a69068f512278722ddb5c03491a1209855fcc78a52bc80a4bee8846cdb49110493738364602a1a41bd9ca3e92db0127b26569395b755104b5aaf236d7c48777db57ed56f6",
		"31bb2da10884db897cf815230330875183b828f74db1bf380a7551120e83e83b56ad6b40e421d554767b73539382572595be6a6c0c0ff9b37009a2736ca00ba831c2c06be37c872691b7747f1873da3a38c9512636ca6f2415480833",
	} {
		wire, err := hex.DecodeString(encoded)
		if err != nil {
			t.Fatal(err)
		}
		plain, checksum, ok := decodeVODRequestFixture(wire)
		if !ok {
			t.Fatal("could not decode native VOD range request")
		}
		if checksum != xorChecksum(plain) || wire[1]&0xaa != 0xaa || len(plain) != 88 || plain[62] != 26 || !isLowerHex(plain[63:]) {
			t.Fatalf("invalid native VOD range layout: checksum=%08x plain=%x", checksum, plain)
		}
	}
}

func TestDecodeNativeVODDataFixture(t *testing.T) {
	wire, err := hex.DecodeString("33fabaa2a15837c8a0b977ff3989b385e86317462924114ad209202c5bf2046501cc336c29cc2ab5dd2f5253ffc7292e930d469a8b9a27f74d4d17e833d952c4de0a99478f66467df4af10f0a5ec2f4daf243f8b0a11002dba8f4ac2ab7510")
	if err != nil {
		t.Fatal(err)
	}
	plain, checksum, ok := decodeVODRequestFixture(wire)
	if !ok {
		t.Fatal("could not decode native VOD data request")
	}
	if checksum != xorChecksum(plain) || wire[1]&0xaa != 0xaa || len(plain) != 91 || plain[65] != 26 || !isLowerHex(plain[66:]) {
		t.Fatalf("invalid native VOD data layout: checksum=%08x plain=%x", checksum, plain)
	}
}

func TestDecodeNativeVODLoadingFixture(t *testing.T) {
	wire, err := hex.DecodeString("32abbd52b46b62a0bb58f2595662cc336c29c4c96320b631dbcc8253af01341fdf229e25c67c7d67fa5cf039ef3d7c7745918680e679db1674ca41eb7ab1d08d6da4eab604bfdd40c3af754690f23136406eea62868a6670a82ac85b79b1")
	if err != nil {
		t.Fatal(err)
	}
	plain, checksum, ok := decodeVODRequestFixture(wire)
	if !ok {
		t.Fatal("could not decode native VOD loading request")
	}
	if checksum != xorChecksum(plain) || wire[1]&0xaa != 0xaa || len(plain) != 90 || plain[64] != 26 || !isLowerHex(plain[65:]) {
		t.Fatalf("invalid native VOD loading layout: checksum=%08x plain=%x", checksum, plain)
	}
}

func TestDecodeNativeVODRangeResponseFixture(t *testing.T) {
	for _, encoded := range []string{
		"315f5dfc843c6a0dc317e7c2c7c9002e8082fef01ee8eee08e41eb2ff900001f322bac79e0882fb6235fed2955501f3e8acd0b7068ce782366963b2f",
		"317546c9b7f40000aa2b4b023ae66e0ee2de033671af121112754d797bfe628a2286c4c065b7b8a4949597d277a556ca121c92f946d967b446bdcca3",
		"3177e2cd3d176e98a9c69c6f36afd1acc1abb837018747a864be3a48056f598126d10000644cf84325faa9b5e0099000eb3b3965be3a48056ea4a124",
	} {
		wire, err := hex.DecodeString(encoded)
		if err != nil {
			t.Fatal(err)
		}
		response, ok := DecodeRangeResponse(wire, nil)
		if !ok || response.Status != 1 || !response.Available {
			t.Fatalf("invalid native VOD range response: ok=%v response=%+v", ok, response)
		}
	}
}

func TestDecodeNativeVODLoadingResponseFixture(t *testing.T) {
	wire, err := hex.DecodeString("32d72ed8974e4f3d2df52a2fca549b977fb7544058a8a85c100b619c72f7db376cadad2d1f4e348aa0d99e992e510000e30368ad636c")
	if err != nil {
		t.Fatal(err)
	}
	response, ok := DecodeLoadingResponse(wire, nil)
	if !ok || response.EndSequence != 0x1625 {
		t.Fatalf("invalid native VOD loading response: ok=%v response=%+v", ok, response)
	}
}

func TestDecodeCapturedVODMediaHeaders(t *testing.T) {
	for i, encoded := range []string{
		"33751a8c88673477564a6cd66da8f5526bcf23d68e2d97bef90e00001eedf159e223407e",
		"33fd11681acb704896c6fdd04721fcbc6900004ce6700431dd58d049fa6bd63f095affb6",
		"335f48ada6d6301b0000066a87ce3a2eed5e123e61147dbdc674f701a50b4cdc391b5bed",
	} {
		header, err := hex.DecodeString(encoded)
		if err != nil {
			t.Fatal(err)
		}
		wire := append(header, make([]byte, MediaPayloadSize)...)
		frame, ok := DecodeVODPlayback(wire, nil)
		want := []uint32{48, 60, 49}[i]
		if !ok || frame.Sequence != want {
			t.Fatalf("invalid native VOD media header: ok=%v sequence=%d want=%d", ok, frame.Sequence, want)
		}
	}
}

func decodeVODRequestFixture(wire []byte) ([]byte, uint32, bool) {
	pivot, keyLen := int(wire[2]&0x3f), int(wire[5])
	checksum := binary.BigEndian.Uint32(wire[pivot : pivot+4])
	plain := append(append([]byte(nil), wire[:pivot]...), wire[pivot+4:]...)
	keyOffset := int(binary.BigEndian.Uint16(wire[3:5]) & 0x0fff)
	for i := 6; i < len(plain); i++ {
		plain[i] ^= playbackKey[keyOffset+(i-6)%keyLen]
	}
	return plain, checksum, int(binary.BigEndian.Uint16(plain[10:12])) == len(wire)
}
