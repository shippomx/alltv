package newtvauth

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestChlistResource(t *testing.T) {
	r := ChlistRequest{Host: "127.0.0.1", Port: 1889, Query: "?name=alice", Sign: strings.Repeat("a", 64), APKMD5: strings.Repeat("b", 32), SOMD5: "none", OtherSO: "libclient.so", Arch: 64, OSAPI: 37, Timestamp: 1700000000}
	resource, err := r.Resource()
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]string
	if err := json.Unmarshal([]byte(resource), &got); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"/chlist?name=alice&sover=20260628", "&arch=64", "&sign=" + strings.Repeat("a", 64), "&rooted=0", "&timestamp=1700000000", "&verify=c3a54520d06e5c3c1468c1ebf5b62ffa"} {
		if !strings.Contains(got["target"], want) {
			t.Fatalf("target %q lacks %q", got["target"], want)
		}
	}
}

func TestChlistResourceRejectsIncompleteInput(t *testing.T) {
	if _, err := (ChlistRequest{}).Resource(); err == nil {
		t.Fatal("expected validation error")
	}
}

func TestAuthResource(t *testing.T) {
	r := ChlistRequest{Sign: strings.Repeat("a", 64), APKMD5: strings.Repeat("b", 32), OtherSO: "libclient.so", Arch: 64, OSAPI: 37, Timestamp: 1700000000}
	resource, err := r.AuthResource("http://127.0.0.1:1881/auth?name=alice")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(resource, `"type":"auth"`) || !strings.Contains(resource, `/auth?name=alice&sover=20260628`) {
		t.Fatalf("resource = %s", resource)
	}
	if !strings.HasPrefix(resource, `{"type":"auth","target":`) {
		t.Fatalf("native auth field order = %s", resource)
	}
}

func TestAuthResourceAcceptsNativeTargetWithoutScheme(t *testing.T) {
	r := ChlistRequest{Sign: strings.Repeat("a", 64), APKMD5: strings.Repeat("b", 32), OtherSO: "libclient.so", Arch: 64, OSAPI: 37, Timestamp: 1700000000}
	resource, err := r.AuthResource("127.0.0.1:1881/auth?name=alice")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(resource, `"target":"http://127.0.0.1:1881/auth?name=alice&sover=`) {
		t.Fatalf("resource = %s", resource)
	}
}
