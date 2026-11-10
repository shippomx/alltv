package newtvproto

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"net"
	"testing"
	"time"
)

type channelWriter chan []byte

func (w channelWriter) Write(p []byte) (int, error) {
	w <- append([]byte(nil), p...)
	return len(p), nil
}

func TestNewPunchPacketMatchesNativeWireFormatWithoutStackLeak(t *testing.T) {
	for range 64 {
		packet, err := newPunchPacket(nil)
		if err != nil {
			t.Fatal(err)
		}
		if packet[0] != 0x51 || packet[1]&0xaa != 0xaa {
			t.Fatalf("invalid prefix: %x", packet[:2])
		}
		for i, value := range packet[2:] {
			if value != 0 {
				t.Fatalf("payload byte %d leaks padding: %02x", i+2, value)
			}
		}
	}
}

func TestSendPunchSendsAirPacketOnly(t *testing.T) {
	server, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	clientConn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer clientConn.Close()
	client := Client{Config: ClientConfig{HolePunchPeers: []*net.UDPAddr{server.LocalAddr().(*net.UDPAddr)}}}
	if err := client.sendPunch(clientConn); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 64)
	_ = server.SetReadDeadline(time.Now().Add(time.Second))
	if n, _, err := server.ReadFrom(buf); err != nil || n != 30 {
		t.Fatalf("first packet length=%d err=%v", n, err)
	}
	_ = server.SetReadDeadline(time.Now().Add(20 * time.Millisecond))
	if n, _, err := server.ReadFrom(buf); err == nil {
		t.Fatalf("unexpected second packet length=%d", n)
	}
}

func TestNewPunchPacketPreservesOpaqueNativePadding(t *testing.T) {
	template := make([]byte, 30)
	for i := 2; i < len(template); i++ {
		template[i] = byte(i)
	}
	packet, err := newPunchPacket(template)
	if err != nil {
		t.Fatal(err)
	}
	if string(packet[2:]) != string(template[2:]) {
		t.Fatalf("padding=%x", packet[2:])
	}
}

func TestEndpointExtensionMatchesNativeLayout(t *testing.T) {
	extension, err := endpointExtension(&net.UDPAddr{IP: net.ParseIP("23.237.231.181"), Port: 6017})
	if err != nil {
		t.Fatal(err)
	}
	if got := extension[:7]; string(got) != string([]byte{6, 23, 237, 231, 181, 0x17, 0x81}) {
		t.Fatalf("endpoint prefix=%x", got)
	}
	if _, err := hex.DecodeString(string(extension[7:]) + "0"); err != nil {
		t.Fatalf("nonce=%q: %v", extension[7:], err)
	}
}

func TestLiveSessionFakeKeyMatchesNativeCaller(t *testing.T) {
	if liveSessionFakeKey != 0x7e1ecc7f {
		t.Fatalf("live session fake key=%08x", liveSessionFakeKey)
	}
}

func TestStreamStartsAtFirstLiveSequence(t *testing.T) {
	server, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	clientConn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer clientConn.Close()
	_, channel := playbackDatagramFor(t, 0, 0, 23)
	client := Client{Config: ClientConfig{Peers: []*net.UDPAddr{server.LocalAddr().(*net.UDPAddr)}}, Window: 3}
	copy(client.Config.Channel[:], channel)
	client.Config.ClientID = 1
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	output := make(channelWriter, 1)
	done := make(chan error, 1)
	go func() { done <- client.Stream(ctx, clientConn, output) }()
	buf := make([]byte, 1024)
	for {
		n, peer, err := server.ReadFrom(buf)
		if err != nil {
			t.Fatal(err)
		}
		switch n {
		case 98:
			plain, _ := controlPlain(56)
			plain[24], plain[30], plain[31] = 0, 0x55, 32
			binary.BigEndian.PutUint16(plain[32:34], 436)
			binary.BigEndian.PutUint32(plain[34:38], 1234)
			binary.BigEndian.PutUint32(plain[38:42], 1174)
			if _, err := server.WriteTo(encodeControlResponse(plain, 17), peer); err != nil {
				t.Fatal(err)
			}
		case 107:
			plain, _, ok := decodeRequest(buf[:n])
			if !ok {
				t.Fatal("invalid data request")
			}
			if streamID := binary.BigEndian.Uint32(plain[31:35]); streamID != 1174 {
				t.Fatalf("stream ID=%d, want Range stream ID 1174", streamID)
			}
			if fakeKey := binary.BigEndian.Uint16(plain[3:5]) & 0x0fff; fakeKey < 436 || fakeKey >= 468 {
				t.Fatalf("fake key=%03x", fakeKey)
			}
			datagram, _ := playbackDatagramFor(t, 0, binary.BigEndian.Uint32(plain[31:35]), 23)
			if _, err := server.WriteTo(datagram, peer); err != nil {
				t.Fatal(err)
			}
			goto sent
		}
	}
sent:
	select {
	case payload := <-output:
		if len(payload) != MediaPayloadSize {
			t.Fatalf("output=%d", len(payload))
		}
	case err := <-done:
		t.Fatal(err)
	case <-time.After(time.Second):
		t.Fatal("stream did not start at the first live sequence")
	}
}

func TestSendDataBatchesThreeSequences(t *testing.T) {
	server, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	clientConn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer clientConn.Close()
	client := Client{Config: ClientConfig{Peers: []*net.UDPAddr{server.LocalAddr().(*net.UDPAddr)}}}
	peerIndex := 0
	if err := client.sendData(clientConn, 9, []uint32{1, 2, 3, 4}, 436, 32, &peerIndex); err != nil {
		t.Fatal(err)
	}
	_ = server.SetReadDeadline(time.Now().Add(time.Second))
	buf := make([]byte, 1024)
	for _, want := range []uint16{3, 1} {
		n, _, err := server.ReadFrom(buf)
		if err != nil {
			t.Fatal(err)
		}
		plain, _, ok := decodeRequest(buf[:n])
		if !ok || binary.BigEndian.Uint16(plain[29:31]) != want {
			t.Fatalf("valid=%v count=%d", ok, binary.BigEndian.Uint16(plain[29:31]))
		}
	}
}

func TestSendDataBindsHoleToMatchingPeer(t *testing.T) {
	first, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	clientConn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer clientConn.Close()
	client := Client{Config: ClientConfig{
		Peers:          []*net.UDPAddr{first.LocalAddr().(*net.UDPAddr), second.LocalAddr().(*net.UDPAddr)},
		HolePunchPeers: []*net.UDPAddr{{IP: net.IPv4(192, 0, 2, 1), Port: 6001}, {IP: net.IPv4(192, 0, 2, 2), Port: 6002}},
	}}
	peerIndex := 0
	if err := client.sendData(clientConn, 9, []uint32{1}, 436, 32, &peerIndex); err != nil {
		t.Fatal(err)
	}
	if err := client.sendData(clientConn, 9, []uint32{2}, 436, 32, &peerIndex); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 1024)
	_ = first.SetReadDeadline(time.Now().Add(time.Second))
	n, _, err := first.ReadFrom(buf)
	if err != nil {
		t.Fatal(err)
	}
	plain, _, ok := decodeRequest(buf[:n])
	if !ok || !bytes.Equal(plain[len(plain)-31:len(plain)-25], []byte{192, 0, 2, 1, 0x17, 0x71}) {
		t.Fatalf("first extension=%x", plain[len(plain)-32:])
	}
	_ = second.SetReadDeadline(time.Now().Add(time.Second))
	n, _, err = second.ReadFrom(buf)
	if err != nil {
		t.Fatal(err)
	}
	plain, _, ok = decodeRequest(buf[:n])
	if !ok || !bytes.Equal(plain[len(plain)-31:len(plain)-25], []byte{192, 0, 2, 2, 0x17, 0x72}) {
		t.Fatalf("second extension=%x", plain[len(plain)-32:])
	}
}

func TestStreamVODNegotiatesRangeBeforeData(t *testing.T) {
	server, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	clientConn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer clientConn.Close()
	_, channel := controlPlain(50)
	client := Client{Config: ClientConfig{Peers: []*net.UDPAddr{server.LocalAddr().(*net.UDPAddr)}, ClientID: 1}, Window: 3}
	copy(client.Config.Channel[:], channel)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	output := make(channelWriter, 1)
	done := make(chan error, 1)
	go func() { done <- client.StreamVOD(ctx, clientConn, output, 0) }()
	buf := make([]byte, 2048)
	gotData := false
	for {
		n, peer, err := server.ReadFrom(buf)
		if err != nil {
			t.Fatal(err)
		}
		switch n {
		case 92:
			plain, _ := controlPlain(56)
			plain[0], plain[24], plain[30] = 0x31, 1, 0x55
			if _, err := server.WriteTo(encodeControlResponse(plain, 17), peer); err != nil {
				t.Fatal(err)
			}
		case 94:
			if !gotData {
				t.Fatal("VOD loading was sent before the initial data window")
			}
			plain, _ := controlPlain(50)
			plain[0] = 0x32
			if _, err := server.WriteTo(encodeControlResponse(plain, 17), peer); err != nil {
				t.Fatal(err)
			}
			datagram := vodPlaybackDatagram(channel, 48)
			if _, err := server.WriteTo(datagram, peer); err != nil {
				t.Fatal(err)
			}
			goto sent
		case 95:
			gotData = true
		}
	}
sent:
	select {
	case <-output:
	case err := <-done:
		t.Fatal(err)
	case <-time.After(time.Second):
		t.Fatal("VOD stream did not start")
	}
}

func TestExchangeClassifiesHandshakeTimeout(t *testing.T) {
	server, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	conn, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	client := Client{Config: ClientConfig{Peers: []*net.UDPAddr{server.LocalAddr().(*net.UDPAddr)}}, HandshakeAttempts: 1}
	err = client.exchange(context.Background(), conn, [][]byte{{1}}, false, nil, func([]byte) bool { return false })
	if !errors.Is(err, ErrHandshake) {
		t.Fatalf("error=%v", err)
	}
}

func TestStreamRetriesEstablishedPlaybackHandshakeOnce(t *testing.T) {
	server, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	conn, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	client := Client{Config: ClientConfig{Peers: []*net.UDPAddr{server.LocalAddr().(*net.UDPAddr)}, ClientID: 1}}
	media := NewContinuityWriter(&bytes.Buffer{})
	media.BeginSession()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	started := time.Now()
	err = client.Stream(ctx, conn, media)
	if !errors.Is(err, ErrHandshake) || time.Since(started) >= 2*time.Second {
		t.Fatalf("reconnect error=%v elapsed=%s", err, time.Since(started))
	}
}

func TestStreamKeepsPCRAliveDuringReconnectHandshake(t *testing.T) {
	server, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	conn, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	client := Client{Config: ClientConfig{Peers: []*net.UDPAddr{server.LocalAddr().(*net.UDPAddr)}, ClientID: 1}, HandshakeAttempts: 1}
	var output bytes.Buffer
	media := NewContinuityWriter(&output)
	media.started = true
	media.video[256] = 0x1b
	media.emittedPCR, media.haveEmittedPCR = 10_000, true
	media.emittedPCRAt = time.Now()
	err = client.Stream(context.Background(), conn, media)
	if !errors.Is(err, ErrHandshake) {
		t.Fatalf("reconnect error=%v", err)
	}
	if output.Len() == 0 {
		t.Fatal("reconnect handshake emitted no PCR heartbeat")
	}
	for offset := 0; offset < output.Len(); offset += TSPacketSize {
		if _, ok := readPCR(output.Bytes()[offset : offset+TSPacketSize]); !ok {
			t.Fatalf("packet %d has no PCR", offset/TSPacketSize)
		}
	}
}

func TestStreamClassifiesRangeWithoutMediaAsHandshakeFailure(t *testing.T) {
	server, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	conn, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_, channel := playbackDatagramFor(t, 0, 0, 23)
	client := Client{Config: ClientConfig{Peers: []*net.UDPAddr{server.LocalAddr().(*net.UDPAddr)}, ClientID: 1}, HandshakeAttempts: 1}
	copy(client.Config.Channel[:], channel)
	go func() {
		buf := make([]byte, 1024)
		for {
			n, peer, readErr := server.ReadFrom(buf)
			if readErr != nil {
				return
			}
			if n == 98 {
				plain, _ := controlPlain(56)
				plain[24], plain[30], plain[31] = 0, 0x55, 32
				binary.BigEndian.PutUint16(plain[32:34], 436)
				binary.BigEndian.PutUint32(plain[34:38], 1234)
				binary.BigEndian.PutUint32(plain[38:42], 1174)
				_, _ = server.WriteTo(encodeControlResponse(plain, 17), peer)
			}
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err = client.Stream(ctx, conn, &bytes.Buffer{})
	if !errors.Is(err, ErrHandshake) {
		t.Fatalf("range without media error=%v", err)
	}
}

func vodPlaybackDatagram(channel []byte, sequence uint32) []byte {
	plain := make([]byte, 32)
	plain[0], plain[1], plain[2], plain[5] = 0x33, 0x55, 17, 16
	binary.BigEndian.PutUint16(plain[3:5], 0x321)
	binary.BigEndian.PutUint16(plain[6:8], 36+MediaPayloadSize)
	copy(plain[8:24], channel)
	binary.BigEndian.PutUint32(plain[26:30], sequence)
	binary.BigEndian.PutUint16(plain[30:32], MediaPayloadSize)
	for i := 6; i < len(plain); i++ {
		plain[i] ^= playbackKey[0x321+(i-6)%16]
	}
	wire := make([]byte, 36+MediaPayloadSize)
	copy(wire, plain[:17])
	copy(wire[21:36], plain[17:])
	for i := 36; i < len(wire); i += 188 {
		wire[i] = 0x47
	}
	return wire
}
