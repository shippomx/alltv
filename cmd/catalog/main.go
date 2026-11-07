package main

import (
	"encoding/json"
	"encoding/xml"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type node struct {
	Text   string `xml:"text,attr"`
	ID     string `xml:"resource-id,attr"`
	Bounds string `xml:"bounds,attr"`
	Nodes  []node `xml:"node"`
}
type doc struct {
	Nodes []node `xml:"node"`
}
type channel struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Group string `json:"group"`
}
type category struct {
	name string
	x    int
}

var boundsRE = regexp.MustCompile(`\[(\d+),(\d+)\]\[(\d+),(\d+)\]`)

func main() {
	serial := flag.String("serial", "emulator-5554", "adb serial")
	out := flag.String("out", "channels.json", "output JSON")
	flag.Parse()
	cats := []category{{"Favorites", 271}, {"电影", 452}, {"新闻", 633}, {"体育", 814}, {"少儿", 995}, {"大陆", 1176}, {"欧美", 1357}, {"香港", 1538}, {"台湾", 1719}, {"日韩", 1900}, {"新马菲", 2081}, {"Special/ 特别节目", 2263}}
	var all []channel
	for _, cat := range cats {
		run(*serial, "shell", "input", "tap", strconv.Itoa(cat.x), "195")
		time.Sleep(500 * time.Millisecond)
		seen := map[string]bool{}
		for page := 0; page < 60; page++ {
			chs := dump(*serial, cat.name)
			added := 0
			for _, c := range chs {
				if !seen[c.ID] {
					seen[c.ID] = true
					all = append(all, c)
					added++
				}
			}
			if page > 0 && added == 0 {
				break
			}
			run(*serial, "shell", "input", "swipe", "1000", "900", "1000", "300", "450")
			time.Sleep(250 * time.Millisecond)
		}
		fmt.Fprintf(os.Stderr, "%s: %d\n", cat.name, len(seen))
	}
	b, _ := json.MarshalIndent(all, "", "  ")
	if err := os.WriteFile(*out, append(b, '\n'), 0644); err != nil {
		panic(err)
	}
	fmt.Fprintf(os.Stderr, "total: %d -> %s\n", len(all), *out)
}

func run(serial string, args ...string) []byte {
	all := append([]string{"-s", serial}, args...)
	b, err := exec.Command("adb", all...).CombinedOutput()
	if err != nil {
		panic(fmt.Errorf("adb %s: %w: %s", strings.Join(args, " "), err, b))
	}
	return b
}

func dump(serial, group string) []channel {
	run(serial, "shell", "uiautomator", "dump", "/data/local/tmp/newtv-catalog.xml")
	b := run(serial, "exec-out", "cat", "/data/local/tmp/newtv-catalog.xml")
	var d doc
	if err := xml.Unmarshal(b, &d); err != nil {
		panic(err)
	}
	var flat []node
	var walk func([]node)
	walk = func(ns []node) {
		for _, n := range ns {
			flat = append(flat, n)
			walk(n.Nodes)
		}
	}
	walk(d.Nodes)
	type item struct {
		text string
		y    int
	}
	var ids, names []item
	for _, n := range flat {
		m := boundsRE.FindStringSubmatch(n.Bounds)
		if len(m) == 0 {
			continue
		}
		y, _ := strconv.Atoi(m[2])
		if y < 250 {
			continue
		}
		if n.ID == "tv.newtv:id/tv_id" {
			ids = append(ids, item{n.Text, y})
		}
		if n.ID == "tv.newtv:id/tv_name" {
			names = append(names, item{n.Text, y})
		}
	}
	var out []channel
	for _, id := range ids {
		for _, name := range names {
			if abs(id.y-name.y) < 20 {
				out = append(out, channel{id.text, name.text, group})
				break
			}
		}
	}
	return out
}
func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
