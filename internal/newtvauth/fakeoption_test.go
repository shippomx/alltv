package newtvauth

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"
)

func TestFakeOptionCodec(t *testing.T) {
	want := []byte(`{"air":"38.180.193.170:1018","reqtype":"hbservers","ver":"1000"}`)
	packet, err := encodeFakeOption(map[string]string{"air": "38.180.193.170:1018", "reqtype": "hbservers", "ver": "1000"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := decodeFakeOption(packet)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("got=%s err=%v", got, err)
	}
	packet[3]++
	if _, err := decodeFakeOption(packet); err == nil {
		t.Fatal("accepted invalid payload length")
	}
}

func TestTrialAccountRequestMatchesNativeFields(t *testing.T) {
	request := trialAccountRequest("trial", "android-id", "358770", "device", "4", "3915", "host:1818", time.Unix(123, 0))
	got, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"air":"host:1818","androidid":"android-id","cpu":"4","devname":"device","encrypt":true,"level":"A","mem":"3915","product":"newtv","ref_code":"358770","remote":"host:1818","reqtype":"trial","timestamp":123,"ver":"1000"}`
	if string(got) != want {
		t.Fatalf("got %s", got)
	}
}
