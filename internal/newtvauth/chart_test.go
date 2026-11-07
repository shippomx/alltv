package newtvauth

import (
	"bytes"
	"compress/gzip"
	"strings"
	"testing"
)

func TestDecodeChart(t *testing.T) {
	var compressed bytes.Buffer
	zw := gzip.NewWriter(&compressed)
	_, _ = zw.Write([]byte(`{"hbservers":["one:1889","two:1889"],"natoption":{"mode":1},"key2":"k","playurl":"p"}`))
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	encoded, err := EncodeResource(2, 9, compressed.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	chart, err := DecodeChart(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if len(chart.HBServers) != 2 || chart.HBServers[1] != "two:1889" || string(chart.Key) != `"k"` {
		t.Fatalf("chart = %#v", chart)
	}
}

func TestChartURL(t *testing.T) {
	got := chartURL("chart.example:1889", "news/hd", "new tv", "arg", "key", "20231212")
	for _, want := range []string{"http://chart.example:1889/chart?", "charttype=arg", "name=news%2Fhd", "product=new+tv", "keyid=key", "ver=20231212", "cm=gz"} {
		if !strings.Contains(got, want) {
			t.Fatalf("chart URL %q does not contain %q", got, want)
		}
	}
}

func TestChartResourceMatchesNativeEnvelope(t *testing.T) {
	got, err := chartResource("http://chart.example/chart?name=device&product=newtv&charttype=arg")
	if err != nil {
		t.Fatal(err)
	}
	want := "{\"target\":\"http://chart.example/chart?name=device&product=newtv&charttype=arg\",\"type\":\"chart\"}\n"
	if got != want {
		t.Fatalf("resource = %q, want %q", got, want)
	}
}

func TestGunzipChartRejectsPlainData(t *testing.T) {
	if _, err := gunzipChart([]byte(`{"chart_list":[]}`)); err == nil {
		t.Fatal("accepted plain data as gzip")
	}
}
