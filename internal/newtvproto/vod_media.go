package newtvproto

import (
	"bytes"
	"encoding/binary"
)

func DecodeVODPlayback(datagram, expectedChannel []byte) (PlaybackFrame, bool) {
	if len(datagram) < 36 || datagram[0] != 0x33 || datagram[1]&0x55 != 0x55 {
		return PlaybackFrame{}, false
	}
	pivot, keyLen := int(datagram[2]&0x3f), int(datagram[5])
	if pivot < 6 || pivot > 31 || keyLen < 16 {
		return PlaybackFrame{}, false
	}
	plain := make([]byte, 32)
	copy(plain, datagram[:pivot])
	copy(plain[pivot:], datagram[pivot+4:36])
	keyOffset := int(binary.BigEndian.Uint16(datagram[3:5]) & 0x0fff)
	for i := 6; i < len(plain); i++ {
		plain[i] ^= playbackKey[keyOffset+(i-6)%keyLen]
	}
	length := int(binary.BigEndian.Uint16(plain[30:32]))
	if int(binary.BigEndian.Uint16(plain[6:8])) != len(datagram) || plain[24] != 0 || length != len(datagram)-36 || length != MediaPayloadSize {
		return PlaybackFrame{}, false
	}
	if len(expectedChannel) != 0 && (len(expectedChannel) != 16 || !bytes.Equal(plain[8:24], expectedChannel)) {
		return PlaybackFrame{}, false
	}
	return PlaybackFrame{Sequence: binary.BigEndian.Uint32(plain[26:30]), Payload: append([]byte(nil), datagram[36:]...)}, true
}
