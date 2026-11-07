package newtvauth

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/hex"
	"fmt"
)

const resourceCodes = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"

var resourceIndex = [...]byte{
	59, 10, 3, 57, 15, 10, 12, 7, 11, 10, 8, 36, 40, 26, 11, 26, 0, 24, 24, 19, 46, 41, 32, 4, 35, 31, 24, 20, 15, 54, 53, 15,
	52, 50, 48, 4, 60, 59, 36, 53, 54, 4, 9, 45, 33, 4, 2, 30, 60, 39, 30, 11, 46, 11, 28, 38, 14, 24, 57, 15, 49, 34, 42, 4,
	24, 22, 40, 47, 17, 39, 6, 29, 17, 52, 11, 7, 54, 47, 18, 21, 21, 40, 50, 60, 36, 46, 34, 55, 14, 18, 46, 38, 17, 19, 56, 3,
	56, 17, 22, 15, 24, 42, 39, 3, 43, 16, 5, 15, 55, 20, 12, 31, 10, 7, 26, 28, 26, 30, 40, 1, 7, 16, 2, 33, 43, 29, 47, 45,
	10, 26, 37, 4, 17, 54, 38, 27, 36, 41, 33, 56, 29, 56, 4, 50, 5, 54, 1, 44, 4, 9, 9, 57, 40, 46, 0, 34, 20, 44, 38, 16,
	3, 31, 23, 3, 17, 23, 10, 41, 44, 37, 50, 29, 24, 57, 12, 29, 12, 32, 37, 26, 3, 36, 59, 1, 29, 29, 36, 54, 36, 60, 10, 12,
	38, 28, 6, 57, 41, 15, 29, 44, 44, 11, 1, 57, 31, 54, 27, 54, 23, 40, 16, 3, 57, 19, 35, 34, 22, 59, 44, 19, 7, 22, 4, 9,
	27, 19, 48, 60, 29, 2, 35, 59, 11, 52, 3, 41, 6, 26, 49, 44, 49, 32, 20, 17, 57, 28, 31, 55, 14, 35, 40, 51, 61, 50, 14, 40,
	57, 52, 21, 9, 36, 21, 5, 11, 55, 21, 41, 58, 14, 21, 28, 7,
}

func EncodeResource(keySelector, ivSelector byte, plain []byte) (string, error) {
	key, iv, err := resourceKeyIV(keySelector, ivSelector)
	if err != nil {
		return "", err
	}
	padding := aes.BlockSize - len(plain)%aes.BlockSize
	padded := make([]byte, len(plain)+padding)
	copy(padded, plain)
	for i := len(plain); i < len(padded); i++ {
		padded[i] = byte(padding)
	}
	block, _ := aes.NewCipher(key)
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(padded, padded)
	header := []byte{1, keySelector ^ ivSelector, keySelector, ivSelector}
	return fmt.Sprintf("%02X%02X%02X%02X", header[0], header[1], header[2], header[3]) + base64.StdEncoding.EncodeToString(padded), nil
}

func DecodeResource(encoded string) ([]byte, error) {
	if len(encoded) < 9 {
		return nil, fmt.Errorf("NewTV resource is shorter than its header")
	}
	header, err := hex.DecodeString(encoded[:8])
	if err != nil || len(header) != 4 || header[0] != 1 || header[2]^header[3] != header[1] {
		return nil, fmt.Errorf("invalid NewTV resource header")
	}
	key, iv, err := resourceKeyIV(header[2], header[3])
	if err != nil {
		return nil, err
	}
	data, err := base64.StdEncoding.DecodeString(encoded[8:])
	if err != nil || len(data) == 0 || len(data)%aes.BlockSize != 0 {
		return nil, fmt.Errorf("invalid NewTV resource payload")
	}
	block, _ := aes.NewCipher(key)
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(data, data)
	padding := int(data[len(data)-1])
	if padding == 0 || padding > aes.BlockSize || padding > len(data) {
		return nil, fmt.Errorf("invalid NewTV resource padding")
	}
	for _, b := range data[len(data)-padding:] {
		if int(b) != padding {
			return nil, fmt.Errorf("invalid NewTV resource padding")
		}
	}
	return data[:len(data)-padding], nil
}

func resourceKeyIV(keySelector, ivSelector byte) ([]byte, []byte, error) {
	if int(keySelector)+aes.BlockSize > len(resourceIndex) || int(ivSelector)+aes.BlockSize > len(resourceIndex) {
		return nil, nil, fmt.Errorf("unsupported NewTV resource selector %d/%d", keySelector, ivSelector)
	}
	section := func(selector byte) []byte {
		out := make([]byte, aes.BlockSize)
		for i, index := range resourceIndex[int(selector) : int(selector)+aes.BlockSize] {
			out[i] = resourceCodes[index]
		}
		return out
	}
	return section(keySelector), section(ivSelector), nil
}
