package newtvauth

import (
	"strings"
	"testing"
)

func TestParseDirectory(t *testing.T) {
	xml := `<Root><master>edge.example:1889</master><auth_addr>auth.example</auth_addr><Category Name="News" PwdMode="1"><Channel Name="World" PwdMode="0"><Item><Url>0123456789abcdef?pic=logo.png_epg=world</Url></Item><Item><Url>fedcba9876543210</Url></Item></Channel></Category></Root>`
	d, err := ParseDirectory(strings.NewReader(xml))
	if err != nil {
		t.Fatal(err)
	}
	if d.Config["master"] != "edge.example:1889" || d.Config["auth_addr"] != "auth.example" {
		t.Fatalf("config = %#v", d.Config)
	}
	if len(d.Categories) != 1 || !d.Categories[0].Password || len(d.Categories[0].Channels) != 1 {
		t.Fatalf("categories = %#v", d.Categories)
	}
	items := d.Categories[0].Channels[0].Items
	if len(items) != 2 || items[0].URL != "0123456789abcdef" || items[0].Pic != "logo.png" || items[0].EPG != "world" || items[1].URL != "fedcba9876543210" {
		t.Fatalf("items = %#v", items)
	}
}

func TestParseDirectoryRejectsInvalidHierarchy(t *testing.T) {
	_, err := ParseDirectory(strings.NewReader(`<Root><Channel Name="orphan"/></Root>`))
	if err == nil {
		t.Fatal("expected hierarchy error")
	}
}
