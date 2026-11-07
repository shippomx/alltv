package newtvauth

import (
	"bytes"
	"strings"
	"testing"
)

func TestResourceCodec(t *testing.T) {
	plain := []byte(`{"hbservers":["127.0.0.1:1889"]}`)
	encoded, err := EncodeResource(3, 11, plain)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeResource(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(decoded, plain) {
		t.Fatalf("decoded = %q", decoded)
	}
}

func TestResourceSelectorsUseSlidingWindow(t *testing.T) {
	key, iv, err := resourceKeyIV(0x49, 0x36)
	if err != nil {
		t.Fatal(err)
	}
	if string(key) != "0LH2vSVVoy8kui3O" || string(iv) != "cmOY5PxiqEYWovRn" {
		t.Fatalf("key/iv = %q/%q", key, iv)
	}
}

func TestResourceCodecRejectsTampering(t *testing.T) {
	if _, err := DecodeResource("01020304AAAA"); err == nil {
		t.Fatal("expected invalid header error")
	}
	encoded, err := EncodeResource(0, 16, []byte("chart"))
	if err != nil {
		t.Fatal(err)
	}
	encoded = encoded[:len(encoded)-2] + "AA"
	if _, err := DecodeResource(encoded); err == nil || !strings.Contains(err.Error(), "payload") && !strings.Contains(err.Error(), "padding") {
		t.Fatalf("expected payload or padding error, got %v", err)
	}
}
