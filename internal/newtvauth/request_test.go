package newtvauth

import "testing"

func TestLoginQueryMatchesNativeOrder(t *testing.T) {
	got := (Login{Name: "alice", Password: "secret", AndroidID: "device-id", Device: "TV Box", CPU: "8", Memory: "4096"}).AuthQuery()
	want := "name=alice&pass=secret&androidid=device-id&lang=cn&ver=44150&buildver=2026.10.29.1&package=tv.newtv&devname=TV Box&cpu=8&mem=4096"
	if got != want {
		t.Fatalf("query = %q, want %q", got, want)
	}
}
