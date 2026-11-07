package newtvproto

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"errors"
)

func EncodeVODRangeRequest(channel, device [16]byte, clientID, counter uint32, session uint16, fakeKey uint32) ([]byte, error) {
	plain := make([]byte, 88)
	if err := randomVODMarker(&plain[13], 0xaa); err != nil {
		return nil, err
	}
	binary.BigEndian.PutUint32(plain[14:18], counter)
	copy(plain[18:34], channel[:])
	copy(plain[34:50], device[:])
	binary.BigEndian.PutUint32(plain[50:54], clientID)
	binary.BigEndian.PutUint16(plain[56:58], session)
	binary.BigEndian.PutUint32(plain[58:62], fakeKey)
	if err := randomVODNonce(plain[62:]); err != nil {
		return nil, err
	}
	return encodeVODRequest(0x31, plain)
}

func EncodeVODLoadingRequest(channel, device [16]byte, clientID, counter, sequence uint32, session uint16, fakeKey uint32) ([]byte, error) {
	plain := make([]byte, 90)
	if err := randomVODMarker(&plain[13], 0); err != nil {
		return nil, err
	}
	binary.BigEndian.PutUint32(plain[14:18], counter)
	copy(plain[18:34], channel[:])
	copy(plain[34:50], device[:])
	binary.BigEndian.PutUint32(plain[50:54], clientID)
	binary.BigEndian.PutUint32(plain[54:58], sequence)
	binary.BigEndian.PutUint16(plain[58:60], session)
	binary.BigEndian.PutUint32(plain[60:64], fakeKey)
	if err := randomVODNonce(plain[64:]); err != nil {
		return nil, err
	}
	return encodeVODRequest(0x32, plain)
}

func EncodeVODDataRequest(channel, device [16]byte, sequences []uint32, session uint16, fakeKey uint32) ([]byte, error) {
	if len(sequences) == 0 || len(sequences) > 3 {
		return nil, errors.New("VOD data request must contain 1-3 sequences")
	}
	plain := make([]byte, 79+4*len(sequences))
	copy(plain[13:29], channel[:])
	binary.BigEndian.PutUint16(plain[29:31], uint16(len(sequences)))
	offset := 31
	for _, sequence := range sequences {
		binary.BigEndian.PutUint32(plain[offset:offset+4], sequence)
		offset += 4
	}
	copy(plain[offset:offset+16], device[:])
	binary.BigEndian.PutUint16(plain[offset+16:offset+18], session)
	binary.BigEndian.PutUint32(plain[offset+18:offset+22], fakeKey)
	if err := randomVODNonce(plain[offset+22:]); err != nil {
		return nil, err
	}
	return encodeVODRequest(0x33, plain)
}

func encodeVODRequest(kind byte, plain []byte) ([]byte, error) {
	return encodeRequestKindFlags(kind, 0xaa, plain)
}

func randomVODMarker(dst *byte, mask byte) error {
	var marker [1]byte
	if _, err := rand.Read(marker[:]); err != nil {
		return err
	}
	*dst = marker[0] | mask
	return nil
}

func randomVODNonce(dst []byte) error {
	if len(dst) < 2 {
		return errors.New("VOD nonce is too short")
	}
	raw := make([]byte, (len(dst)-1+1)/2)
	if _, err := rand.Read(raw); err != nil {
		return err
	}
	dst[0] = byte(len(dst))
	encoded := make([]byte, hex.EncodedLen(len(raw)))
	hex.Encode(encoded, raw)
	copy(dst[1:], encoded[:len(dst)-1])
	return nil
}
