package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"newtv-session-bridge/internal/newtvauth"
	"newtv-session-bridge/internal/newtvproto"
)

func TestParsePlaybackPort(t *testing.T) {
	proc := "0: 0100007F:B35A 00000000:0000 0A 0 0 0 10234 0\n" +
		"1: 00000000:2B6E 00000000:0000 0A 0 0 0 10234 0\n"
	p, err := parsePlaybackPort(proc, "10234")
	if err != nil || p != 45914 {
		t.Fatalf("port=%d err=%v", p, err)
	}
	if _, err := parsePlaybackPort(proc, "99999"); err == nil {
		t.Fatal("expected no playback port")
	}
}

func TestRefreshAfterUsesTrialWindow(t *testing.T) {
	if got := refreshAfter("15"); got != 14*time.Minute {
		t.Fatalf("refreshAfter(15) = %s", got)
	}
	if got := refreshAfter(""); got != 14*time.Minute {
		t.Fatalf("refreshAfter(empty) = %s", got)
	}
}

func TestDirectoryChannelLabelsUseAppNamesAndGroups(t *testing.T) {
	directory := newtvauth.Directory{Categories: []newtvauth.Category{
		{Name: "电影", Channels: []newtvauth.Channel{{Name: "HBO HD", Items: []newtvauth.Item{{URL: "501"}}}}},
		{Name: "香港", Channels: []newtvauth.Channel{{Name: "TVB经典", Items: []newtvauth.Item{{URL: "2"}, {URL: "501"}}}}},
	}}
	labels := directoryChannelLabels(directory)
	if got := labels["501"]; got.Name != "HBO HD" || got.Group != "电影" {
		t.Fatalf("label=%+v", got)
	}
	if got := directoryChannelOrder(directory); !slices.Equal(got, []string{"501", "2"}) {
		t.Fatalf("order=%v", got)
	}
}

func TestPlaylistDoesNotSeekNonSeekableLiveInput(t *testing.T) {
	b := &bridge{channels: []channel{{ID: "501", Name: "HBO HD", Group: "电影"}}}
	w := httptest.NewRecorder()
	b.playlist(w, httptest.NewRequest(http.MethodGet, "/playlist.m3u", nil))
	if strings.Contains(w.Body.String(), "input-repeat") {
		t.Fatal("live HTTP input must not be rewound after a stream restart")
	}
}

func TestApplyNewTVClientIDKeepsExistingChannelsPlayable(t *testing.T) {
	first := &newtvproto.Client{Config: newtvproto.ClientConfig{ClientID: 1}}
	second := &newtvproto.Client{Config: newtvproto.ClientConfig{ClientID: 1}}
	b := &bridge{newtvClient: first, newtvChannels: [][]*newtvproto.Client{{first, second}}}
	b.applyNewTVClientID(2)
	if first.Config.ClientID != 2 || second.Config.ClientID != 2 {
		t.Fatal("authenticated client ID was not applied to existing channels")
	}
}

func TestStartStreamCancelsPreviousSession(t *testing.T) {
	b := new(bridge)
	first, cancelFirst := b.startStream(context.Background())
	defer cancelFirst()
	second, cancelSecond := b.startStream(context.Background())
	defer cancelSecond()
	select {
	case <-first.Done():
	default:
		t.Fatal("previous stream remains active")
	}
	if err := second.Err(); err != nil {
		t.Fatalf("new stream canceled: %v", err)
	}
}

func TestHeaderFlagFromEnvironment(t *testing.T) {
	t.Setenv("TEST_UPSTREAM_TOKEN", "Bearer secret")
	var headers headerFlags
	if err := headers.Set("Authorization: @TEST_UPSTREAM_TOKEN"); err != nil {
		t.Fatal(err)
	}
	if got := http.Header(headers).Get("Authorization"); got != "Bearer secret" {
		t.Fatalf("header=%q", got)
	}
}

func TestProxyStream(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "video/mp2t")
		w.WriteHeader(http.StatusPartialContent)
		_, _ = io.WriteString(w, "stream")
	}))
	defer upstream.Close()

	b := &bridge{upstream: upstream.URL, httpClient: upstream.Client()}
	w := httptest.NewRecorder()
	if err := b.proxyStream(context.Background(), w); err != nil {
		t.Fatal(err)
	}
	if w.Code != http.StatusPartialContent || w.Header().Get("Content-Type") != "video/mp2t" || w.Body.String() != "stream" {
		t.Fatalf("code=%d content-type=%q body=%q", w.Code, w.Header().Get("Content-Type"), w.Body.String())
	}
}

func TestFlushingWriterFlushesEachMediaWrite(t *testing.T) {
	w := httptest.NewRecorder()
	if _, err := (flushingWriter{w}).Write([]byte("media")); err != nil {
		t.Fatal(err)
	}
	if !w.Flushed || w.Body.String() != "media" {
		t.Fatalf("flushed=%v body=%q", w.Flushed, w.Body.String())
	}
}

func TestParseVODURL(t *testing.T) {
	channel, peers, err := parseVODURL("udp://64.62.198.107:3101/08E2D6FD9961BA05744BB1A7016C7C91", 21, 31)
	if err != nil || len(peers) != 2 || peers[0].String() != "64.62.198.97:4101" || peers[1].String() != "64.62.198.98:4101" || channel[0] != 0x08 || channel[15] != 0x91 {
		t.Fatalf("channel=%x peers=%v err=%v", channel, peers, err)
	}
	if _, _, err := parseVODURL("https://example.test/not-vod", 21, 31); err == nil {
		t.Fatal("accepted non-VOD URL")
	}
	if _, _, err := parseVODURL("udp://64.62.198.107:3101/08E2D6FD9961BA05744BB1A7016C7C91", 0, 0); err == nil {
		t.Fatal("accepted UDP VOD without FakeOption adaptation")
	}
}

func TestRewriteHLS(t *testing.T) {
	b := &bridge{hlsKey: []byte("01234567890123456789012345678901")}
	base, _ := url.Parse("https://media.example/live/master.m3u8")
	manifest := "#EXTM3U\n#EXT-X-KEY:METHOD=AES-128,URI=\"keys/key.bin\"\n#EXT-X-KEY:METHOD=SAMPLE-AES,URI=\"skd://license\"\nsegment.ts?part=1\n"
	var out bytes.Buffer
	if err := b.rewriteHLS(&out, strings.NewReader(manifest), base); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if lines[2] != `#EXT-X-KEY:METHOD=SAMPLE-AES,URI="skd://license"` {
		t.Fatalf("DRM URI changed: %q", lines[2])
	}
	for _, line := range []string{strings.TrimSuffix(strings.TrimPrefix(lines[1], `#EXT-X-KEY:METHOD=AES-128,URI="`), `"`), lines[3]} {
		target, err := b.decodeHLSURL(strings.TrimPrefix(line, "/hls/"))
		if err != nil {
			t.Fatal(err)
		}
		if target != "https://media.example/live/keys/key.bin" && target != "https://media.example/live/segment.ts?part=1" {
			t.Fatalf("unexpected target %q", target)
		}
	}
}

func TestHLSResourceRejectsForgedToken(t *testing.T) {
	b := &bridge{hlsKey: []byte("01234567890123456789012345678901"), httpClient: http.DefaultClient}
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/hls/forged.token", nil)
	r.SetPathValue("token", "forged.token")
	b.hlsResource(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatalf("code=%d", w.Code)
	}
}

func TestHLSManifestAndSegment(t *testing.T) {
	var segmentRange string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/live/index.m3u8":
			w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
			_, _ = io.WriteString(w, "#EXTM3U\nsegment.ts\n")
		case "/live/segment.ts":
			segmentRange = r.Header.Get("Range")
			w.Header().Set("Content-Type", "video/mp2t")
			w.WriteHeader(http.StatusPartialContent)
			_, _ = io.WriteString(w, "segment")
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	b := &bridge{upstream: upstream.URL + "/live/index.m3u8", httpClient: upstream.Client(), hlsKey: []byte("01234567890123456789012345678901")}
	manifest := httptest.NewRecorder()
	if err := b.proxyStream(context.Background(), manifest); err != nil {
		t.Fatal(err)
	}
	segmentPath := strings.TrimSpace(strings.Split(manifest.Body.String(), "\n")[1])
	segment := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, segmentPath, nil)
	req.Header.Set("Range", "bytes=10-19")
	req.SetPathValue("token", strings.TrimPrefix(segmentPath, "/hls/"))
	b.hlsResource(segment, req)
	if segment.Code != http.StatusPartialContent || segment.Body.String() != "segment" || segmentRange != "bytes=10-19" {
		t.Fatalf("code=%d body=%q range=%q", segment.Code, segment.Body.String(), segmentRange)
	}
}

func TestHLSUpstreamAuthAndCookies(t *testing.T) {
	var manifestAuthorized, segmentAuthorized bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorized := r.Header.Get("Authorization") == "Bearer test" && strings.Contains(r.Header.Get("Cookie"), "device=known")
		switch r.URL.Path {
		case "/index.m3u8":
			manifestAuthorized = authorized
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "fresh", Path: "/"})
			w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
			_, _ = io.WriteString(w, "#EXTM3U\nsegment.ts\n")
		case "/segment.ts":
			segmentAuthorized = authorized && strings.Contains(r.Header.Get("Cookie"), "session=fresh")
			_, _ = io.WriteString(w, "segment")
		}
	}))
	defer upstream.Close()
	parsed, _ := url.Parse(upstream.URL)
	jar, _ := cookiejar.New(nil)
	b := &bridge{
		upstream:     upstream.URL + "/index.m3u8",
		upstreamHost: parsed.Host,
		upstreamAuth: http.Header{"Authorization": {"Bearer test"}, "Cookie": {"device=known"}},
		httpClient:   &http.Client{Jar: jar},
		hlsKey:       []byte("01234567890123456789012345678901"),
	}
	manifest := httptest.NewRecorder()
	if err := b.proxyStream(context.Background(), manifest); err != nil {
		t.Fatal(err)
	}
	segmentPath := strings.TrimSpace(strings.Split(manifest.Body.String(), "\n")[1])
	segment := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, segmentPath, nil)
	req.SetPathValue("token", strings.TrimPrefix(segmentPath, "/hls/"))
	b.hlsResource(segment, req)
	if !manifestAuthorized || !segmentAuthorized || segment.Body.String() != "segment" {
		t.Fatalf("manifest auth=%v segment auth=%v body=%q", manifestAuthorized, segmentAuthorized, segment.Body.String())
	}
}

func TestStreamFileLoops(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "*.ts")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.Write([]byte{0x47, 1, 2})
	_ = f.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	var out bytes.Buffer
	_ = streamFile(ctx, &out, f.Name(), 100_000_000)
	if out.Len() < 6 || out.Bytes()[0] != 0x47 || out.Bytes()[3] != 0x47 {
		t.Fatalf("not looped: %x", out.Bytes())
	}
}

func TestTCPPayload(t *testing.T) {
	p := make([]byte, 14+20+20+3)
	binary.BigEndian.PutUint16(p[12:14], 0x0800)
	p[14], p[23] = 0x45, 6
	tcp := p[34:]
	binary.BigEndian.PutUint16(tcp[:2], 45914)
	binary.BigEndian.PutUint32(tcp[4:8], 7)
	tcp[12] = 5 << 4
	copy(tcp[20:], []byte{0x47, 1, 2})
	seq, payload, ok := tcpPayload(p, 45914)
	if !ok || seq != 7 || !bytes.Equal(payload, []byte{0x47, 1, 2}) {
		t.Fatalf("seq=%d payload=%x ok=%v", seq, payload, ok)
	}
}

func TestTCPPayloadLinuxCookedV2(t *testing.T) {
	p := make([]byte, 20+20+20+3)
	binary.BigEndian.PutUint16(p[:2], 0x0800)
	p[20], p[29] = 0x45, 6
	tcp := p[40:]
	binary.BigEndian.PutUint16(tcp[:2], 41016)
	binary.BigEndian.PutUint32(tcp[4:8], 9)
	tcp[12] = 5 << 4
	copy(tcp[20:], []byte{0x47, 2, 3})
	seq, payload, ok := tcpPayloadForLinkType(p, 41016, 276)
	if !ok || seq != 9 || !bytes.Equal(payload, []byte{0x47, 2, 3}) {
		t.Fatalf("seq=%d payload=%x ok=%v", seq, payload, ok)
	}
}

func TestResponseStripperSplitHeader(t *testing.T) {
	s := responseStripper{}
	parts := [][]byte{[]byte("HT"), []byte("TP/1.1 200 OK\r\nContent-Type: video/mp2t\r\n"), []byte("\r\n\x47\x01"), []byte("\x47\x02")}
	var got []byte
	for _, part := range parts {
		body, err := s.body(part)
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, body...)
	}
	if want := []byte{0x47, 1, 0x47, 2}; !bytes.Equal(got, want) {
		t.Fatalf("body=%x want=%x", got, want)
	}
}

func TestResponseStripperMidstream(t *testing.T) {
	s := responseStripper{}
	body, err := s.body([]byte{0x47, 1, 2})
	if err != nil || !bytes.Equal(body, []byte{0x47, 1, 2}) {
		t.Fatalf("body=%x err=%v", body, err)
	}
}

func TestRelayPlaybackReconnectsOnPortChange(t *testing.T) {
	wantDone := errors.New("done")
	var ports []int
	capture := func(ctx context.Context, port int, _ io.Writer) error {
		ports = append(ports, port)
		if port == 42000 {
			return wantDone
		}
		<-ctx.Done()
		return ctx.Err()
	}
	probe := func(context.Context) (int, error) { return 42000, nil }
	err := relayPlayback(context.Background(), io.Discard, 41016, time.Millisecond, capture, probe)
	if !errors.Is(err, wantDone) || !slices.Equal(ports, []int{41016, 42000}) {
		t.Fatalf("ports=%v err=%v", ports, err)
	}
}
