package newtvauth

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/binary"
	"encoding/json"
	"fmt"
)

const interchangerHeaderSize = 44

type InterchangerFrame struct {
	Version     byte
	Kind        byte
	KeySelector byte
	IVSelector  byte
	RequestID   [32]byte
	Sequence    uint32
	Payload     []byte
}

func EncodeInterchangerJSON(kind, keySelector, ivSelector byte, requestID [32]byte, sequence uint32, value any) ([]byte, error) {
	plain, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("encode Interchanger JSON: %w", err)
	}
	plain = append(plain, '\n')
	encrypted, err := EncryptInterchangerPayload(keySelector, ivSelector, plain)
	if err != nil {
		return nil, err
	}
	return EncodeInterchangerFrame(InterchangerFrame{Version: 1, Kind: kind, KeySelector: keySelector, IVSelector: ivSelector, RequestID: requestID, Sequence: sequence, Payload: encrypted})
}

// DecodeInterchangerFrame validates and removes the transport checksum and padding.
func DecodeInterchangerFrame(packet []byte) (InterchangerFrame, error) {
	if len(packet) < interchangerHeaderSize+4 || len(packet)%4 != 0 {
		return InterchangerFrame{}, fmt.Errorf("invalid Interchanger frame length %d", len(packet))
	}
	want := binary.BigEndian.Uint32(packet)
	var got uint32
	for i := 4; i < len(packet); i += 4 {
		got ^= binary.BigEndian.Uint32(packet[i : i+4])
	}
	if got != want {
		return InterchangerFrame{}, fmt.Errorf("invalid Interchanger checksum")
	}
	padding := int(packet[len(packet)-1])
	if padding < 1 || padding > 4 || padding > len(packet)-interchangerHeaderSize {
		return InterchangerFrame{}, fmt.Errorf("invalid Interchanger padding")
	}
	for _, b := range packet[len(packet)-padding:] {
		if int(b) != padding {
			return InterchangerFrame{}, fmt.Errorf("invalid Interchanger padding")
		}
	}
	var frame InterchangerFrame
	frame.Version = packet[4]
	frame.Kind = packet[5]
	frame.KeySelector = packet[6]
	frame.IVSelector = packet[7]
	copy(frame.RequestID[:], packet[8:40])
	frame.Sequence = binary.BigEndian.Uint32(packet[40:44])
	frame.Payload = append([]byte(nil), packet[44:len(packet)-padding]...)
	return frame, nil
}

func EncodeInterchangerFrame(frame InterchangerFrame) ([]byte, error) {
	if frame.Version == 0 {
		return nil, fmt.Errorf("Interchanger version is required")
	}
	size := interchangerHeaderSize + len(frame.Payload)
	padding := 4 - size%4
	packet := make([]byte, size+padding)
	packet[4], packet[5], packet[6], packet[7] = frame.Version, frame.Kind, frame.KeySelector, frame.IVSelector
	copy(packet[8:40], frame.RequestID[:])
	binary.BigEndian.PutUint32(packet[40:44], frame.Sequence)
	copy(packet[44:], frame.Payload)
	for i := size; i < len(packet); i++ {
		packet[i] = byte(padding)
	}
	var checksum uint32
	for i := 4; i < len(packet); i += 4 {
		checksum ^= binary.BigEndian.Uint32(packet[i : i+4])
	}
	binary.BigEndian.PutUint32(packet, checksum)
	return packet, nil
}

func (frame InterchangerFrame) DecryptPayload() ([]byte, error) {
	if frame.KeySelector == 0xff && frame.IVSelector == 0xff {
		return append([]byte(nil), frame.Payload...), nil
	}
	if len(frame.Payload) == 0 || len(frame.Payload)%aes.BlockSize != 0 {
		return nil, fmt.Errorf("invalid encrypted Interchanger payload length %d", len(frame.Payload))
	}
	key, iv, err := resourceKeyIV(frame.KeySelector, frame.IVSelector)
	if err != nil {
		return nil, err
	}
	plain := append([]byte(nil), frame.Payload...)
	block, _ := aes.NewCipher(key)
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(plain, plain)
	padding := int(plain[len(plain)-1])
	if padding < 1 || padding > aes.BlockSize || padding > len(plain) {
		return nil, fmt.Errorf("invalid Interchanger payload padding")
	}
	for _, b := range plain[len(plain)-padding:] {
		if int(b) != padding {
			return nil, fmt.Errorf("invalid Interchanger payload padding")
		}
	}
	return plain[:len(plain)-padding], nil
}

func EncryptInterchangerPayload(keySelector, ivSelector byte, plain []byte) ([]byte, error) {
	key, iv, err := resourceKeyIV(keySelector, ivSelector)
	if err != nil {
		return nil, err
	}
	padding := aes.BlockSize - len(plain)%aes.BlockSize
	encrypted := make([]byte, len(plain)+padding)
	copy(encrypted, plain)
	for i := len(plain); i < len(encrypted); i++ {
		encrypted[i] = byte(padding)
	}
	block, _ := aes.NewCipher(key)
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(encrypted, encrypted)
	return encrypted, nil
}
