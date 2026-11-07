package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDecodeHexRequiresExactLength(t *testing.T) {
	dst := make([]byte, 2)
	if err := decodeHex(dst, "0102", "value"); err != nil || dst[1] != 2 {
		t.Fatalf("dst=%x err=%v", dst, err)
	}
	if err := decodeHex(dst, "01", "value"); err == nil {
		t.Fatal("accepted short value")
	}
}

func TestLoadSessionVODOptions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.json")
	raw := []byte(`{"channel":"00000000000000000000000000000000","device_id":"00000000000000000000000000000000","client_id":1,"peers":["127.0.0.1:1"],"vod_option":21,"vod_address_adapt":31}`)
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	runtime, err := loadSession(path)
	if err != nil || runtime.VODOption.Option != 21 || runtime.VODOption.AddressAdapt != 31 {
		t.Fatalf("runtime=%+v err=%v", runtime.VODOption, err)
	}
}

func TestLoadSessionHolePunchState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.json")
	raw := []byte(`{"channel":"00000000000000000000000000000000","device_id":"00000000000000000000000000000000","client_id":1,"peers":["127.0.0.1:1"],"hole_punch_peers":["192.0.2.1:6001"],"punch_payload":"510000000000000000000000000000000000000000000000000000000000"}`)
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	runtime, err := loadSession(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(runtime.Client.HolePunchPeers) != 1 || len(runtime.Client.PunchPayload) != 30 {
		t.Fatalf("holes=%v punch=%x", runtime.Client.HolePunchPeers, runtime.Client.PunchPayload)
	}
}
