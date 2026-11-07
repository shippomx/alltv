package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"time"
)

type capturePortFunc func(context.Context, int, io.Writer) error
type probePortFunc func(context.Context) (int, error)

func (b *bridge) capture(ctx context.Context, port int, dst io.Writer) error {
	command := fmt.Sprintf("tcpdump -i lo -U -nn -s 0 -w - 'tcp src port %d' 2>/dev/null", port)
	cmd := exec.CommandContext(ctx, "adb", "-s", b.serial, "shell", command)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	err = copyPCAPTCP(dst, stdout, port)
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
	return err
}

func relayPlayback(ctx context.Context, dst io.Writer, port int, interval time.Duration, capture capturePortFunc, probe probePortFunc) error {
	for {
		captureCtx, cancel := context.WithCancel(ctx)
		done := make(chan error, 1)
		go func(current int) { done <- capture(captureCtx, current, dst) }(port)
		ticker := time.NewTicker(interval)

		for {
			select {
			case err := <-done:
				ticker.Stop()
				cancel()
				return err
			case <-ctx.Done():
				ticker.Stop()
				cancel()
				<-done
				return ctx.Err()
			case <-ticker.C:
				probeCtx, probeCancel := context.WithTimeout(ctx, 2*time.Second)
				next, err := probe(probeCtx)
				probeCancel()
				if err != nil || next == port {
					continue
				}
				ticker.Stop()
				cancel()
				<-done
				port = next
				goto reconnect
			}
		}
	reconnect:
	}
}

func copyPCAPTCP(dst io.Writer, src io.Reader, port int) error {
	r := bufio.NewReaderSize(src, 256*1024)
	global := make([]byte, 24)
	if _, err := io.ReadFull(r, global); err != nil {
		return err
	}
	linkType := binary.LittleEndian.Uint32(global[20:24])
	if binary.LittleEndian.Uint32(global[:4]) != 0xa1b2c3d4 || (linkType != 1 && linkType != 276) {
		return fmt.Errorf("unsupported pcap format")
	}
	var next uint32
	started := false
	stripper := responseStripper{}
	for {
		h := make([]byte, 16)
		if _, err := io.ReadFull(r, h); err != nil {
			return err
		}
		n := binary.LittleEndian.Uint32(h[8:12])
		if n > 256*1024 {
			return fmt.Errorf("pcap packet too large: %d", n)
		}
		packet := make([]byte, n)
		if _, err := io.ReadFull(r, packet); err != nil {
			return err
		}
		seq, payload, ok := tcpPayloadForLinkType(packet, port, linkType)
		if !ok || len(payload) == 0 {
			continue
		}
		if !started {
			next, started = seq, true
		}
		if seq > next {
			continue
		}
		if end := seq + uint32(len(payload)); end <= next {
			continue
		}
		payload = payload[next-seq:]
		next += uint32(len(payload))
		payload, stripErr := stripper.body(payload)
		if stripErr != nil {
			return stripErr
		}
		if len(payload) == 0 {
			continue
		}
		if _, err := dst.Write(payload); err != nil {
			return err
		}
		if f, ok := dst.(http.Flusher); ok {
			f.Flush()
		}
	}
}

type responseStripper struct {
	decided bool
	header  []byte
}

func (s *responseStripper) body(p []byte) ([]byte, error) {
	if s.decided {
		return p, nil
	}
	s.header = append(s.header, p...)
	if len(s.header) > 64*1024 {
		return nil, fmt.Errorf("HTTP response header too large")
	}
	prefix := []byte("HTTP/")
	if len(s.header) < len(prefix) && bytes.Equal(s.header, prefix[:len(s.header)]) {
		return nil, nil
	}
	if !bytes.HasPrefix(s.header, prefix) {
		s.decided = true
		body := s.header
		s.header = nil
		return body, nil
	}
	if i := headerEnd(s.header); i >= 0 {
		s.decided = true
		body := s.header[i:]
		s.header = nil
		return body, nil
	}
	return nil, nil
}

func headerEnd(p []byte) int {
	for i := 0; i+3 < len(p); i++ {
		if p[i] == '\r' && p[i+1] == '\n' && p[i+2] == '\r' && p[i+3] == '\n' {
			return i + 4
		}
	}
	return -1
}

func tcpPayload(p []byte, port int) (uint32, []byte, bool) {
	return tcpPayloadForLinkType(p, port, 1)
}

func tcpPayloadForLinkType(p []byte, port int, linkType uint32) (uint32, []byte, bool) {
	var networkOffset int
	switch linkType {
	case 1:
		if len(p) < 14 || binary.BigEndian.Uint16(p[12:14]) != 0x0800 {
			return 0, nil, false
		}
		networkOffset = 14
	case 276:
		if len(p) < 20 || binary.BigEndian.Uint16(p[:2]) != 0x0800 {
			return 0, nil, false
		}
		networkOffset = 20
	default:
		return 0, nil, false
	}
	if len(p) < networkOffset+40 {
		return 0, nil, false
	}
	ip := p[networkOffset:]
	ihl := int(ip[0]&0x0f) * 4
	if len(ip) < ihl+20 || ip[9] != 6 {
		return 0, nil, false
	}
	tcp := ip[ihl:]
	if int(binary.BigEndian.Uint16(tcp[:2])) != port {
		return 0, nil, false
	}
	off := int(tcp[12]>>4) * 4
	if len(tcp) < off {
		return 0, nil, false
	}
	return binary.BigEndian.Uint32(tcp[4:8]), tcp[off:], true
}
