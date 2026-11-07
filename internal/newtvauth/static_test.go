package newtvauth

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"io"
	"strings"
	"testing"
)

func TestStaticResource(t *testing.T) {
	target := "http://example.test/root.xml?name=user"
	raw, err := json.Marshal(map[string]string{"type": "file", "target": target + "&cm=gz"})
	if err != nil {
		t.Fatal(err)
	}
	var request map[string]string
	if err := json.Unmarshal(raw, &request); err != nil || request["type"] != "file" || !strings.HasSuffix(request["target"], "&cm=gz") {
		t.Fatalf("request=%v err=%v", request, err)
	}
}

func TestDecodeStaticResult(t *testing.T) {
	var compressed bytes.Buffer
	zw := gzip.NewWriter(&compressed)
	if _, err := io.WriteString(zw, "<vod_addrs/>"); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	content, err := decodeStaticResult(InterchangerResult{Content: []byte(base64.StdEncoding.EncodeToString(compressed.Bytes())), Compression: "gz"})
	if err != nil || string(content) != "<vod_addrs/>" {
		t.Fatalf("content=%q err=%v", content, err)
	}
}
