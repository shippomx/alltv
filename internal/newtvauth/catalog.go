package newtvauth

import (
	"encoding/xml"
	"fmt"
	"io"
	"strings"
)

type Directory struct {
	Config     map[string]string
	Categories []Category
}

type Category struct {
	Name     string
	Password bool
	Channels []Channel
}

type Channel struct {
	Name     string
	Password bool
	Items    []Item
}

type Item struct {
	URL string
	Pic string
	EPG string
}

// ParseDirectory decodes the XML returned by VideoClient.icChlist.
func ParseDirectory(r io.Reader) (Directory, error) {
	d := Directory{Config: make(map[string]string)}
	dec := xml.NewDecoder(r)
	var category *Category
	var channel *Channel
	var item *Item
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			return d, nil
		}
		if err != nil {
			return Directory{}, fmt.Errorf("decode NewTV directory: %w", err)
		}
		start, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		switch start.Name.Local {
		case "Category":
			d.Categories = append(d.Categories, Category{Name: attr(start, "Name"), Password: attr(start, "PwdMode") == "1"})
			category = &d.Categories[len(d.Categories)-1]
			channel, item = nil, nil
		case "Channel":
			if category == nil {
				return Directory{}, fmt.Errorf("NewTV directory contains Channel outside Category")
			}
			category.Channels = append(category.Channels, Channel{Name: attr(start, "Name"), Password: attr(start, "PwdMode") == "1"})
			channel = &category.Channels[len(category.Channels)-1]
			item = nil
		case "Item":
			if channel == nil {
				return Directory{}, fmt.Errorf("NewTV directory contains Item outside Channel")
			}
			channel.Items = append(channel.Items, Item{})
			item = &channel.Items[len(channel.Items)-1]
		case "Url":
			var value string
			if err := dec.DecodeElement(&value, &start); err != nil {
				return Directory{}, fmt.Errorf("decode NewTV Url: %w", err)
			}
			if item == nil {
				return Directory{}, fmt.Errorf("NewTV directory contains Url outside Item")
			}
			item.URL, item.Pic, item.EPG = splitURL(strings.TrimSpace(value))
		default:
			if isConfig(start.Name.Local) {
				var value string
				if err := dec.DecodeElement(&value, &start); err != nil {
					return Directory{}, fmt.Errorf("decode NewTV %s: %w", start.Name.Local, err)
				}
				d.Config[start.Name.Local] = strings.TrimSpace(value)
			}
		}
	}
}

func attr(start xml.StartElement, name string) string {
	for _, a := range start.Attr {
		if a.Name.Local == name {
			return a.Value
		}
	}
	return ""
}

func splitURL(value string) (base, pic, epg string) {
	base, metadata, found := strings.Cut(value, "?")
	if !found {
		return base, "", ""
	}
	for _, field := range strings.Split(metadata, "_") {
		key, value, ok := strings.Cut(field, "=")
		if !ok {
			continue
		}
		switch key {
		case "pic":
			pic = value
		case "epg":
			epg = value
		}
	}
	return base, pic, epg
}

func isConfig(name string) bool {
	switch name {
	case "master", "auth_addr", "playback_url", "epg_url", "vod_addr", "search_addr", "pdata_addr", "fake_option_addr", "m3u8_addr", "unlink_addr", "payment_url", "icon_url", "icon_ver", "apk_addr", "upgrade_url", "upgrade_ver", "builder_ver", "last_androidid", "timestamp", "expire", "expire_private", "notify", "notify_private", "notify_trial", "trial_minute", "vip_days", "DisPlayDevNo":
		return true
	default:
		return false
	}
}
