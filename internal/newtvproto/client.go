package newtvproto

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"time"
)

type ClientConfig struct {
	Channel        [16]byte
	DeviceID       [16]byte
	ClientID       uint32
	Peers          []*net.UDPAddr
	HolePunchPeers []*net.UDPAddr
	PunchPayload   []byte
}

type Client struct {
	Config            ClientConfig
	Window            int
	RetryAfter        time.Duration
	HandshakeAttempts int
}

const liveSessionFakeKey uint32 = 2115947647

var ErrHandshake = errors.New("newtv provider did not complete the handshake")
var ErrStalled = errors.New("newtv provider stopped sending media")

// StreamVOD performs the VOD range negotiation and streams the selected title.
func (c Client) StreamVOD(ctx context.Context, conn net.PacketConn, dst io.Writer, position uint16) error {
	if len(c.Config.Peers) == 0 || c.Config.ClientID == 0 {
		return errors.New("newtv VOD requires peers and an authorized client ID")
	}
	var seed [6]byte
	if _, err := rand.Read(seed[:]); err != nil {
		return err
	}
	session := binary.BigEndian.Uint16(seed[:2])
	fakeKey := binary.BigEndian.Uint32(seed[2:])
	for counter := uint32(0); counter < 3; counter++ {
		rangeRequest, err := EncodeVODRangeRequest(c.Config.Channel, c.Config.DeviceID, c.Config.ClientID, counter, session, fakeKey)
		if err != nil {
			return err
		}
		if err := c.send(conn, rangeRequest); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(600 * time.Millisecond):
		}
	}
	window := c.Window
	if window <= 0 {
		window = 45
	}
	retryAfter := c.RetryAfter
	if retryAfter <= 0 {
		retryAfter = 750 * time.Millisecond
	}
	requests := NewRequestWindow(0, window, retryAfter)
	reorder := NewReorderBuffer(0, window*2)
	if err := c.sendVODData(conn, requests.Fill(time.Now()), session, fakeKey); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(50 * time.Millisecond):
	}
	loadingRequest, err := EncodeVODLoadingRequest(c.Config.Channel, c.Config.DeviceID, c.Config.ClientID, 3, uint32(position), session, fakeKey)
	if err != nil {
		return err
	}
	if err := c.send(conn, loadingRequest); err != nil {
		return err
	}
	buf := make([]byte, 64*1024)
	for {
		now := time.Now()
		if err := c.sendVODData(conn, requests.Fill(now), session, fakeKey); err != nil {
			return err
		}
		if err := conn.SetReadDeadline(now.Add(min(retryAfter/2, 250*time.Millisecond))); err != nil {
			return err
		}
		n, _, readErr := conn.ReadFrom(buf)
		if readErr == nil {
			frame, ok := DecodeVODPlayback(buf[:n], c.Config.Channel[:])
			if ok {
				if frame.Sequence >= reorder.Next()+uint32(window/2) {
					requests = NewRequestWindow(frame.Sequence+1, window, retryAfter)
					reorder = NewReorderBuffer(frame.Sequence, window*2)
				}
				requests.Received(frame.Sequence)
				payloads, accepted := reorder.Push(frame)
				if accepted {
					requests.Advance(reorder.Next())
					for _, payload := range payloads {
						if err := writeAll(dst, payload); err != nil {
							return err
						}
					}
				}
			}
		} else if !isTimeout(readErr) {
			return readErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := c.sendVODData(conn, requests.Retries(time.Now()), session, fakeKey); err != nil {
			return err
		}
	}
}

func (c Client) sendVODData(conn net.PacketConn, sequences []uint32, session uint16, fakeKey uint32) error {
	for len(sequences) > 0 {
		n := min(3, len(sequences))
		request, err := EncodeVODDataRequest(c.Config.Channel, c.Config.DeviceID, sequences[:n], session, fakeKey)
		if err != nil {
			return err
		}
		if err := c.send(conn, request); err != nil {
			return err
		}
		sequences = sequences[n:]
	}
	return nil
}

// Stream performs the authorized NewTV UDP handshake and writes ordered MPEG-TS.
func (c Client) Stream(ctx context.Context, conn net.PacketConn, dst io.Writer) error {
	if len(c.Config.Peers) == 0 || c.Config.ClientID == 0 {
		return errors.New("newtv protocol requires peers and an authorized client ID")
	}
	window := c.Window
	if window <= 0 {
		window = 96
	}
	retryAfter := c.RetryAfter
	if retryAfter <= 0 {
		retryAfter = 250 * time.Millisecond
	}

	setup := c.control([3]uint32{c.Config.ClientID, liveSessionFakeKey, 0}, true)
	var rangeKey [2]byte
	if _, err := rand.Read(rangeKey[:]); err != nil {
		return err
	}
	setup.FakeKey = binary.BigEndian.Uint16(rangeKey[:]) & 0xf3ff
	var setupRequests [][]byte
	for _, peer := range c.Config.HolePunchPeers {
		extension, err := endpointExtension(peer)
		if err != nil {
			return err
		}
		setup.Extension = extension
		request, err := EncodeRangeRequest(setup)
		if err != nil {
			return err
		}
		setupRequests = append(setupRequests, request)
	}
	if len(setupRequests) == 0 {
		request, err := EncodeRangeRequest(setup)
		if err != nil {
			return err
		}
		setupRequests = append(setupRequests, request)
	}
	if err := c.sendPunch(conn); err != nil {
		return err
	}
	punchCtx, stopPunch := context.WithCancel(ctx)
	defer stopPunch()
	punchErrors := make(chan error, 1)
	go func() { punchErrors <- c.punchLoop(punchCtx, conn) }()
	if err := c.sendRequests(conn, setupRequests); err != nil {
		return err
	}
	rangeResponse, err := c.waitRange(ctx, conn, setupRequests)
	if err != nil {
		return err
	}
	if rangeResponse.Flags == 0 || rangeResponse.Window > 1023 {
		return fmt.Errorf("newtv provider returned invalid fake key range base=%d limit=%d", rangeResponse.Window, rangeResponse.Flags)
	}

	streamID := rangeResponse.StreamID
	requests := NewRequestWindow(rangeResponse.StartSequence, window, retryAfter)
	// Keep retransmit head-of-line blocking below VLC's three-second cache.
	reorder := NewReorderBuffer(rangeResponse.StartSequence, window*4)
	mediaWriter := NewContinuityWriter(dst)
	mediaWriter.waitPMT = true
	mediaWriter.BeginSession()
	dataPeer := 0
	started := false
	lastMedia := time.Now()
	if err := c.sendData(conn, streamID, requests.Fill(time.Now()), rangeResponse.Window, rangeResponse.Flags, &dataPeer); err != nil {
		return err
	}
	buf := make([]byte, 64*1024)
	for {
		if err := conn.SetReadDeadline(time.Now().Add(min(retryAfter/2, 250*time.Millisecond))); err != nil {
			return err
		}
		n, _, readErr := conn.ReadFrom(buf)
		if readErr == nil {
			frame, ok := DecodePlayback(buf[:n], c.Config.Channel[:])
			if ok {
				if !started {
					requests = NewRequestWindow(frame.Sequence+1, window, retryAfter)
					reorder = NewReorderBuffer(frame.Sequence, window*4)
					started = true
				} else {
					requests.Received(frame.Sequence)
				}
				payloads, _ := reorder.Push(frame)
				requests.Advance(reorder.Next())
				for _, payload := range payloads {
					if err := writeAll(mediaWriter, payload); err != nil {
						return err
					}
				}
				if len(payloads) > 0 {
					lastMedia = time.Now()
				}
			}
		} else if !isTimeout(readErr) {
			return readErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		select {
		case err := <-punchErrors:
			return err
		default:
		}
		now := time.Now()
		retries := requests.Retries(now)
		if err := c.sendData(conn, streamID, retries, rangeResponse.Window, rangeResponse.Flags, &dataPeer); err != nil {
			return err
		}
		if err := c.sendData(conn, streamID, requests.Fill(now), rangeResponse.Window, rangeResponse.Flags, &dataPeer); err != nil {
			return err
		}
		if now.Sub(lastMedia) >= time.Second {
			payloads := reorder.SkipGap()
			requests.Advance(reorder.Next())
			if len(payloads) > 0 {
				mediaWriter.BeginSession()
			}
			for _, payload := range payloads {
				if err := writeAll(mediaWriter, payload); err != nil {
					return err
				}
			}
			if len(payloads) > 0 {
				lastMedia = now
			} else if now.Sub(lastMedia) >= 600*time.Millisecond {
				return ErrStalled
			}
		}
	}
}

func (c Client) punchLoop(ctx context.Context, conn net.PacketConn) error {
	for {
		if err := c.sendPunch(conn); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func (c Client) sendPunch(conn net.PacketConn) error {
	if len(c.Config.HolePunchPeers) == 0 {
		return nil
	}
	packet, err := newPunchPacket(c.Config.PunchPayload)
	if err != nil {
		return err
	}
	for _, peer := range c.Config.HolePunchPeers {
		if _, err := conn.WriteTo(packet[:], peer); err != nil {
			return err
		}
	}
	return nil
}

func newPunchPacket(template []byte) ([30]byte, error) {
	var packet [30]byte
	if len(template) == len(packet) {
		copy(packet[:], template)
	}
	nonce, err := rand.Int(rand.Reader, big.NewInt(255))
	if err != nil {
		return packet, err
	}
	packet[0], packet[1] = 0x51, byte(nonce.Uint64())|0xaa
	return packet, nil
}

func (c Client) control(values [3]uint32, decorated bool) ControlRequest {
	return ControlRequest{Channel: c.Config.Channel, DeviceID: c.Config.DeviceID, Values: values, Decorated: decorated}
}

func (c Client) waitRange(ctx context.Context, conn net.PacketConn, requests [][]byte) (RangeResponse, error) {
	var response RangeResponse
	err := c.exchange(ctx, conn, requests, true, func(datagram []byte) bool {
		var ok bool
		response, ok = DecodeRangeResponse(datagram, c.Config.Channel[:])
		return ok && response.Status == 0 && response.Available && response.StreamID != 0
	})
	return response, err
}

func (c Client) waitLoading(ctx context.Context, conn net.PacketConn, request []byte) (LoadingResponse, error) {
	var response LoadingResponse
	err := c.exchange(ctx, conn, [][]byte{request}, false, func(datagram []byte) bool {
		var ok bool
		response, ok = DecodeLoadingResponse(datagram, c.Config.Channel[:])
		return ok
	})
	return response, err
}

func (c Client) exchange(ctx context.Context, conn net.PacketConn, requests [][]byte, initialSent bool, accept func([]byte) bool) error {
	buf := make([]byte, 64*1024)
	received := 0
	lastKind, lastLength := byte(0), 0
	attempts := c.HandshakeAttempts
	if attempts <= 0 {
		attempts = 5
	}
	for attempt := 0; attempt < attempts; attempt++ {
		if attempt > 0 || !initialSent {
			if err := c.sendRequests(conn, requests); err != nil {
				return err
			}
		}
		deadline := time.Now().Add(time.Second)
		for time.Now().Before(deadline) {
			if err := ctx.Err(); err != nil {
				return err
			}
			_ = conn.SetReadDeadline(minTime(deadline, time.Now().Add(200*time.Millisecond)))
			n, _, err := conn.ReadFrom(buf)
			if err == nil {
				received++
				lastLength = n
				if n > 0 {
					lastKind = buf[0]
				}
				if accept(buf[:n]) {
					return nil
				}
			}
			if err != nil && !isTimeout(err) {
				return err
			}
		}
	}
	return fmt.Errorf("%w (%d datagrams received, last kind=%02x length=%d)", ErrHandshake, received, lastKind, lastLength)
}

func (c Client) sendRequests(conn net.PacketConn, requests [][]byte) error {
	if len(requests) == 1 {
		return c.send(conn, requests[0])
	}
	if len(requests) > len(c.Config.Peers) {
		return fmt.Errorf("newtv has %d requests for %d peers", len(requests), len(c.Config.Peers))
	}
	for i, request := range requests {
		if _, err := conn.WriteTo(request, c.Config.Peers[i]); err != nil {
			return err
		}
	}
	return nil
}

func (c Client) sendData(conn net.PacketConn, streamID uint32, sequences []uint32, base uint16, limit byte, peerIndex *int) error {
	for len(sequences) > 0 {
		n := min(3, len(sequences))
		if len(c.Config.HolePunchPeers) > 0 {
			if len(c.Config.HolePunchPeers) > len(c.Config.Peers) {
				return fmt.Errorf("newtv has %d hole endpoints for %d peers", len(c.Config.HolePunchPeers), len(c.Config.Peers))
			}
			index := *peerIndex % len(c.Config.HolePunchPeers)
			extension, err := endpointExtension(c.Config.HolePunchPeers[index])
			if err != nil {
				return err
			}
			fakeKey, err := newRequestFakeKey(base, limit)
			if err != nil {
				return err
			}
			request, err := EncodeDataRequest(DataRequest{Channel: c.Config.Channel, StreamID: streamID, Sequences: sequences[:n], DeviceID: c.Config.DeviceID, Extension: extension, FakeKey: fakeKey})
			if err != nil {
				return err
			}
			if _, err := conn.WriteTo(request, c.Config.Peers[index]); err != nil {
				return err
			}
			*peerIndex = (*peerIndex + 1) % len(c.Config.HolePunchPeers)
		} else {
			fakeKey, err := newRequestFakeKey(base, limit)
			if err != nil {
				return err
			}
			request, err := EncodeDataRequest(DataRequest{Channel: c.Config.Channel, StreamID: streamID, Sequences: sequences[:n], DeviceID: c.Config.DeviceID, FakeKey: fakeKey})
			if err != nil {
				return err
			}
			index := *peerIndex % len(c.Config.Peers)
			if _, err := conn.WriteTo(request, c.Config.Peers[index]); err != nil {
				return err
			}
			*peerIndex = (*peerIndex + 1) % len(c.Config.Peers)
		}
		sequences = sequences[n:]
	}
	return nil
}

func newRequestFakeKey(base uint16, limit byte) (uint16, error) {
	if limit == 0 || base > 1023 {
		return 0, fmt.Errorf("invalid fake key range base=%d limit=%d", base, limit)
	}
	var high [2]byte
	if _, err := rand.Read(high[:]); err != nil {
		return 0, err
	}
	offset, err := rand.Int(rand.Reader, big.NewInt(int64(limit)))
	if err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint16(high[:])&0xf000 | (base + uint16(offset.Uint64())), nil
}

func endpointExtension(peer *net.UDPAddr) ([32]byte, error) {
	var extension [32]byte
	ip := peer.IP.To4()
	if ip == nil || peer.Port <= 0 || peer.Port > 65535 {
		return extension, fmt.Errorf("invalid IPv4 hole-punch endpoint %v", peer)
	}
	extension[0] = 6
	copy(extension[1:5], ip)
	binary.BigEndian.PutUint16(extension[5:7], uint16(peer.Port))
	const alphabet = "1234567890abcdef"
	var random [25]byte
	if _, err := rand.Read(random[:]); err != nil {
		return extension, err
	}
	for i, value := range random {
		extension[7+i] = alphabet[int(value)%len(alphabet)]
	}
	return extension, nil
}

func (c Client) send(conn net.PacketConn, datagram []byte) error {
	for _, peer := range c.Config.Peers {
		if _, err := conn.WriteTo(datagram, peer); err != nil {
			return err
		}
	}
	return nil
}

func isTimeout(err error) bool {
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

func writeAll(dst io.Writer, p []byte) error {
	for len(p) > 0 {
		n, err := dst.Write(p)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		p = p[n:]
	}
	return nil
}
