package main

import (
	"context"
	"encoding/xml"
	"fmt"
	"regexp"
	"strconv"
	"time"
)

type uiNode struct {
	Text       string   `xml:"text,attr"`
	ResourceID string   `xml:"resource-id,attr"`
	Bounds     string   `xml:"bounds,attr"`
	Nodes      []uiNode `xml:"node"`
}
type uiDoc struct {
	Nodes []uiNode `xml:"node"`
}

var uiBounds = regexp.MustCompile(`\[(\d+),(\d+)\]\[(\d+),(\d+)\]`)
var categoryX = map[string]int{"Favorites": 271, "电影": 452, "新闻": 633, "体育": 814, "少儿": 995, "大陆": 1176, "欧美": 1357, "香港": 1538, "台湾": 1719, "日韩": 1900, "新马菲": 2081, "Special/ 特别节目": 2263}

func (b *bridge) selectChannel(ctx context.Context, c channel) (int, error) {
	x, ok := categoryX[c.Group]
	if !ok {
		return 0, fmt.Errorf("unknown group %q", c.Group)
	}
	_, _ = b.adb(ctx, "shell", "input", "tap", strconv.Itoa(x), "195")
	time.Sleep(500 * time.Millisecond)
	for page := 0; page < 60; page++ {
		y, err := b.channelY(ctx, c.ID)
		if err != nil {
			return 0, err
		}
		if y > 0 {
			oldPort, _ := b.playbackPort(ctx)
			if _, err := b.adb(ctx, "shell", "input", "tap", "650", strconv.Itoa(y)); err != nil {
				return 0, err
			}
			return b.waitPlaybackPortChange(ctx, oldPort, 30*time.Second)
		}
		_, _ = b.adb(ctx, "shell", "input", "swipe", "1000", "900", "1000", "300", "450")
		time.Sleep(250 * time.Millisecond)
	}
	return 0, fmt.Errorf("channel %s not found in group %s", c.ID, c.Group)
}

func (b *bridge) waitPlaybackPortChange(ctx context.Context, old int, timeout time.Duration) (int, error) {
	started := time.Now()
	deadline := time.Now().Add(timeout)
	var last error
	for {
		probe, cancel := context.WithTimeout(ctx, 2*time.Second)
		port, err := b.playbackPort(probe)
		cancel()
		if err == nil && (old == 0 || port != old || time.Since(started) >= 3*time.Second) {
			return port, nil
		}
		last = err
		if time.Now().After(deadline) {
			if last != nil {
				return 0, last
			}
			return 0, fmt.Errorf("playback port did not change from %d", old)
		}
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
}

func (b *bridge) channelY(ctx context.Context, id string) (int, error) {
	if _, err := b.adb(ctx, "shell", "uiautomator", "dump", "/data/local/tmp/newtv-switch.xml"); err != nil {
		return 0, err
	}
	raw, err := b.adb(ctx, "exec-out", "cat", "/data/local/tmp/newtv-switch.xml")
	if err != nil {
		return 0, err
	}
	var d uiDoc
	if err := xml.Unmarshal(raw, &d); err != nil {
		return 0, err
	}
	var walk func([]uiNode) int
	walk = func(ns []uiNode) int {
		for _, n := range ns {
			if n.ResourceID == "tv.newtv:id/tv_id" && n.Text == id {
				m := uiBounds.FindStringSubmatch(n.Bounds)
				if len(m) > 0 {
					a, _ := strconv.Atoi(m[2])
					z, _ := strconv.Atoi(m[4])
					return (a + z) / 2
				}
			}
			if y := walk(n.Nodes); y > 0 {
				return y
			}
		}
		return 0
	}
	return walk(d.Nodes), nil
}
