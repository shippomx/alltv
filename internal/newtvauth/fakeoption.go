package newtvauth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net"
	"time"
)

const fakeOptionTableBase64 = "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"

var fakeOptionTable = func() []byte {
	table, err := base64.StdEncoding.DecodeString(fakeOptionTableBase64)
	if err != nil || len(table) != 1536 {
		panic("invalid FakeOption table")
	}
	return table
}()

type FakeOptionClient struct {
	Timeout time.Duration
	Retries int
}

type FakeOptionResponse struct {
	Resource       string `json:"restype"`
	Version        string `json:"ver"`
	HeartbeatPeers string `json:"hb_servers"`
	Option         int    `json:"option"`
	AddressAdapt   int    `json:"addr_adapt"`
}

type TrialAccountResponse struct {
	Resource   string `json:"restype"`
	Code       int    `json:"code"`
	ExpireStat int    `json:"expire_stat"`
	Account    string `json:"account"`
	Password   string `json:"pass"`
}

func (c FakeOptionClient) FetchTrialAccount(ctx context.Context, endpoint, androidID, refCode, device, cpu, memory string) (TrialAccountResponse, error) {
	request := trialAccountRequest("trial", androidID, refCode, device, cpu, memory, endpoint, time.Now())
	plain, err := c.exchange(ctx, endpoint, request)
	if err != nil {
		return TrialAccountResponse{}, err
	}
	var response TrialAccountResponse
	if err := json.Unmarshal(plain, &response); err != nil {
		return TrialAccountResponse{}, fmt.Errorf("decode trial account: %w", err)
	}
	if response.Resource != "trial" || response.Code != 0 || response.ExpireStat < 0 || response.Account == "" || response.Password == "" {
		return TrialAccountResponse{}, fmt.Errorf("invalid trial account response")
	}
	return response, nil
}

func trialAccountRequest(requestType, androidID, refCode, device, cpu, memory, endpoint string, now time.Time) map[string]any {
	return map[string]any{"air": endpoint, "androidid": androidID, "cpu": cpu, "devname": device, "encrypt": true, "level": "A", "mem": memory, "product": Product, "ref_code": refCode, "remote": endpoint, "reqtype": requestType, "timestamp": now.Unix(), "ver": "1000"}
}

func (c FakeOptionClient) Fetch(ctx context.Context, endpoint, name, requestType string, natType int) (FakeOptionResponse, error) {
	request := map[string]any{"air": endpoint, "body": "", "encrypt": true, "level": "A", "name": name, "nattype": natType, "product": Product, "remote": endpoint, "reqtype": requestType, "timestamp": time.Now().Unix(), "ver": "1000"}
	plain, err := c.exchange(ctx, endpoint, request)
	if err != nil {
		return FakeOptionResponse{}, err
	}
	var response FakeOptionResponse
	if err := json.Unmarshal(plain, &response); err != nil || response.Resource != requestType || response.Version != "1000" {
		return FakeOptionResponse{}, fmt.Errorf("invalid FakeOption %s response", requestType)
	}
	return response, nil
}

func (c FakeOptionClient) exchange(ctx context.Context, endpoint string, request map[string]any) ([]byte, error) {
	peer, err := net.ResolveUDPAddr("udp", endpoint)
	if err != nil {
		return nil, fmt.Errorf("resolve FakeOption endpoint: %w", err)
	}
	conn, err := net.DialUDP("udp", nil, peer)
	if err != nil {
		return nil, fmt.Errorf("dial FakeOption endpoint: %w", err)
	}
	defer conn.Close()
	if c.Timeout <= 0 {
		c.Timeout = time.Second
	}
	if c.Retries <= 0 {
		c.Retries = 4
	}
	packet, err := encodeFakeOption(request)
	if err != nil {
		return nil, err
	}
	buf := make([]byte, 2048)
	for attempt := 0; attempt < c.Retries; attempt++ {
		if _, err := conn.Write(packet); err != nil {
			return nil, fmt.Errorf("send FakeOption request: %w", err)
		}
		deadline := time.Now().Add(c.Timeout)
		if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
			deadline = d
		}
		if err := conn.SetReadDeadline(deadline); err != nil {
			return nil, err
		}
		n, err := conn.Read(buf)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			continue
		}
		plain, err := decodeFakeOption(buf[:n])
		if err != nil {
			continue
		}
		request["level"] = "Z"
		ack, err := encodeFakeOption(request)
		if err == nil {
			_, _ = conn.Write(ack)
		}
		return plain, nil
	}
	return nil, fmt.Errorf("FakeOption request timeout after %d attempts", c.Retries)
}

func encodeFakeOption(value any) ([]byte, error) {
	plain, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	if len(plain) == 0 || len(plain) > 1464 {
		return nil, fmt.Errorf("invalid FakeOption payload length %d", len(plain))
	}
	var random [3]byte
	if _, err := rand.Read(random[:]); err != nil {
		return nil, err
	}
	offset := int(binary.BigEndian.Uint16(random[:2])) % 1280
	keyLength := int(random[2])
	if keyLength < 32 {
		keyLength += 32
	}
	packet := make([]byte, 8+len(plain))
	packet[0], packet[1], packet[2] = 0x23, 0x01, 0x02
	binary.BigEndian.PutUint16(packet[3:5], uint16(len(plain)))
	binary.BigEndian.PutUint16(packet[5:7], uint16(offset))
	packet[7] = byte(keyLength)
	for i, value := range plain {
		packet[8+i] = value ^ fakeOptionTable[offset+i%keyLength]
	}
	return packet, nil
}

func decodeFakeOption(packet []byte) ([]byte, error) {
	if len(packet) < 9 || packet[0] != 0x23 || packet[1] != 0x01 || packet[2] != 0x02 {
		return nil, fmt.Errorf("invalid FakeOption frame")
	}
	length := int(binary.BigEndian.Uint16(packet[3:5]))
	offset := int(binary.BigEndian.Uint16(packet[5:7]))
	keyLength := int(packet[7])
	if length != len(packet)-8 || length > 1464 || offset > 1279 || keyLength < 32 || offset+keyLength > len(fakeOptionTable) {
		return nil, fmt.Errorf("invalid FakeOption frame fields")
	}
	plain := make([]byte, length)
	for i, value := range packet[8:] {
		plain[i] = value ^ fakeOptionTable[offset+i%keyLength]
	}
	return plain, nil
}
