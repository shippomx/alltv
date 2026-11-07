package newtvauth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
)

type InterchangerClient struct {
	Timeout time.Duration
	Retries int
}

type InterchangerResult struct {
	Content     []byte
	Encoded     bool
	Compression string
}

type interchangerMeta struct {
	Notice            string `json:"notice"`
	SyncSequence      uint32 `json:"sync_seq"`
	BlocksPerRequest  int    `json:"blks_per_req"`
	ContentEncodeFlag int    `json:"content_encode_flag"`
}

type interchangerRange struct {
	Notice         string `json:"notice"`
	FileID         string `json:"file_id"`
	FileSize       int    `json:"file_size"`
	BlockSize      int    `json:"blk_size"`
	HeaderBlock    int    `json:"header_blk"`
	TailBlock      int    `json:"tail_blk"`
	CompressMethod string `json:"compress_method"`
	Random         string `json:"random"`
}

func (c InterchangerClient) Fetch(ctx context.Context, endpoint, resource string) ([]byte, error) {
	result, err := c.FetchResult(ctx, endpoint, resource)
	if err != nil {
		return nil, err
	}
	return result.Content, nil
}

func (c InterchangerClient) FetchResult(ctx context.Context, endpoint, resource string) (InterchangerResult, error) {
	peer, err := net.ResolveUDPAddr("udp", endpoint)
	if err != nil {
		return InterchangerResult{}, fmt.Errorf("resolve Interchanger endpoint: %w", err)
	}
	conn, err := net.DialUDP("udp", nil, peer)
	if err != nil {
		return InterchangerResult{}, fmt.Errorf("dial Interchanger endpoint: %w", err)
	}
	defer conn.Close()
	return c.fetch(ctx, conn, endpoint, resource)
}

func (c InterchangerClient) Authenticate(ctx context.Context, endpoint, resource string) (int, error) {
	peer, err := net.ResolveUDPAddr("udp", endpoint)
	if err != nil {
		return 0, fmt.Errorf("resolve Interchanger endpoint: %w", err)
	}
	conn, err := net.DialUDP("udp", nil, peer)
	if err != nil {
		return 0, fmt.Errorf("dial Interchanger endpoint: %w", err)
	}
	defer conn.Close()
	if c.Timeout <= 0 {
		c.Timeout = 2 * time.Second
	}
	if c.Retries <= 0 {
		c.Retries = 4
	}
	requestID, err := randomRequestID()
	if err != nil {
		return 0, err
	}
	target := map[string]string{"air": endpoint, "resource": resource}
	meta, file, err := c.describe(ctx, conn, requestID, target)
	if err != nil {
		return 0, err
	}
	if err := c.finish(conn, requestID, ^uint32(0), target); err != nil {
		return 0, err
	}
	random, err := strconv.ParseUint(file.Random, 16, 32)
	if err != nil || random == 0 {
		return 0, fmt.Errorf("invalid Interchanger auth random (sync %d)", meta.SyncSequence)
	}
	return int(random), nil
}

func (c InterchangerClient) fetch(ctx context.Context, conn *net.UDPConn, endpoint, resource string) (InterchangerResult, error) {
	if c.Timeout <= 0 {
		c.Timeout = 2 * time.Second
	}
	if c.Retries <= 0 {
		c.Retries = 4
	}
	requestID, err := randomRequestID()
	if err != nil {
		return InterchangerResult{}, err
	}
	target := map[string]string{"air": endpoint, "resource": resource}
	if err := c.sendHello(conn, requestID); err != nil {
		return InterchangerResult{}, err
	}
	meta, file, err := c.describe(ctx, conn, requestID, target)
	if err != nil {
		return InterchangerResult{}, err
	}
	if file.Notice != "" || file.FileSize < 0 || file.BlockSize <= 0 || file.HeaderBlock < 0 || file.TailBlock < file.HeaderBlock {
		return InterchangerResult{}, fmt.Errorf("invalid Interchanger range: notice=%q size=%d block=%d range=%d..%d", file.Notice, file.FileSize, file.BlockSize, file.HeaderBlock, file.TailBlock)
	}
	totalBlocks := file.TailBlock - file.HeaderBlock + 1
	fileRequestID, err := interchangerFileID(file.FileID)
	if err != nil {
		return InterchangerResult{}, err
	}
	blocks := make(map[uint32][]byte, totalBlocks)
	nextSequence := meta.SyncSequence + 2
	for attempt := 0; attempt < c.Retries && len(blocks) < totalBlocks; attempt++ {
		missing := missingBlocks(file.HeaderBlock, file.TailBlock, blocks)
		for i := 0; i < len(missing); i += meta.BlocksPerRequest {
			end := min(i+meta.BlocksPerRequest, len(missing))
			packet, err := c.jsonPacket(3, requestID, nextSequence, map[string]any{"air": endpoint, "blocks": missing[i:end], "file_id": file.FileID})
			if err != nil {
				return InterchangerResult{}, err
			}
			nextSequence++
			if _, err := conn.Write(packet); err != nil {
				return InterchangerResult{}, fmt.Errorf("send Interchanger blocks: %w", err)
			}
		}
		deadline := time.Now().Add(c.Timeout)
		for len(blocks) < totalBlocks {
			frame, err := readInterchanger(ctx, conn, deadline)
			if err != nil {
				break
			}
			if frame.RequestID != fileRequestID || frame.Kind != 3 || int(frame.Sequence) < file.HeaderBlock || int(frame.Sequence) > file.TailBlock {
				continue
			}
			payload, err := frame.DecryptPayload()
			if err != nil || len(payload) > file.BlockSize {
				continue
			}
			blocks[frame.Sequence] = payload
		}
	}
	if len(blocks) != totalBlocks {
		return InterchangerResult{}, fmt.Errorf("Interchanger incomplete: received %d/%d blocks", len(blocks), totalBlocks)
	}
	content := make([]byte, 0, file.FileSize)
	for block := file.HeaderBlock; block <= file.TailBlock; block++ {
		content = append(content, blocks[uint32(block)]...)
	}
	if len(content) < file.FileSize {
		return InterchangerResult{}, fmt.Errorf("Interchanger content is short: %d/%d", len(content), file.FileSize)
	}
	if err := c.finish(conn, requestID, nextSequence, target); err != nil {
		return InterchangerResult{}, err
	}
	return InterchangerResult{Content: content[:file.FileSize], Encoded: meta.ContentEncodeFlag != 0, Compression: file.CompressMethod}, nil
}

func interchangerFileID(value string) ([32]byte, error) {
	var id [32]byte
	if len(value) != len(id) {
		return id, fmt.Errorf("invalid Interchanger file ID length %d", len(value))
	}
	copy(id[:], value)
	return id, nil
}

func (c InterchangerClient) describe(ctx context.Context, conn *net.UDPConn, requestID [32]byte, target map[string]string) (interchangerMeta, interchangerRange, error) {
	metaPacket, err := c.jsonPacket(1, requestID, 0, target)
	if err != nil {
		return interchangerMeta{}, interchangerRange{}, err
	}
	var meta interchangerMeta
	if err := c.exchangeJSON(ctx, conn, metaPacket, requestID, 1, &meta); err != nil {
		return meta, interchangerRange{}, fmt.Errorf("Interchanger meta: %w", err)
	}
	if meta.Notice != "" || meta.BlocksPerRequest <= 0 {
		return meta, interchangerRange{}, fmt.Errorf("Interchanger meta rejected: %s", meta.Notice)
	}
	rangePacket, err := c.jsonPacket(2, requestID, meta.SyncSequence+1, target)
	if err != nil {
		return meta, interchangerRange{}, err
	}
	var file interchangerRange
	if err := c.exchangeJSON(ctx, conn, rangePacket, requestID, 2, &file); err != nil {
		return meta, file, fmt.Errorf("Interchanger range: %w", err)
	}
	return meta, file, nil
}

func (c InterchangerClient) finish(conn *net.UDPConn, requestID [32]byte, sequence uint32, target map[string]string) error {
	packet, err := c.jsonPacket(0x1a, requestID, sequence, target)
	if err != nil {
		return err
	}
	if _, err := conn.Write(packet); err != nil {
		return fmt.Errorf("finish Interchanger request: %w", err)
	}
	return nil
}

func (c InterchangerClient) sendHello(conn *net.UDPConn, requestID [32]byte) error {
	var random [7]byte
	if _, err := rand.Read(random[:]); err != nil {
		return fmt.Errorf("generate Interchanger hello: %w", err)
	}
	plain := strings.ToUpper(hex.EncodeToString(random[:3]) + "." + hex.EncodeToString(random[3:]))
	var selectors [2]byte
	if _, err := rand.Read(selectors[:]); err != nil {
		return fmt.Errorf("generate Interchanger selectors: %w", err)
	}
	encrypted, err := EncryptInterchangerPayload(selectors[0], selectors[1], []byte(plain))
	if err != nil {
		return err
	}
	packet, err := EncodeInterchangerFrame(InterchangerFrame{Version: 1, Kind: 0xff, KeySelector: selectors[0], IVSelector: selectors[1], RequestID: requestID, Sequence: 0x01000000, Payload: encrypted})
	if err != nil {
		return err
	}
	if _, err := conn.Write(packet); err != nil {
		return fmt.Errorf("send Interchanger hello: %w", err)
	}
	return nil
}

func (c InterchangerClient) jsonPacket(kind byte, requestID [32]byte, sequence uint32, value any) ([]byte, error) {
	var selectors [2]byte
	if _, err := rand.Read(selectors[:]); err != nil {
		return nil, fmt.Errorf("generate Interchanger selectors: %w", err)
	}
	return EncodeInterchangerJSON(kind, selectors[0], selectors[1], requestID, sequence, value)
}

func (c InterchangerClient) exchangeJSON(ctx context.Context, conn *net.UDPConn, packet []byte, requestID [32]byte, kind byte, dst any) error {
	for attempt := 0; attempt < c.Retries; attempt++ {
		if _, err := conn.Write(packet); err != nil {
			return err
		}
		deadline := time.Now().Add(c.Timeout)
		for {
			frame, err := readInterchanger(ctx, conn, deadline)
			if err != nil {
				break
			}
			if frame.RequestID != requestID || frame.Kind != kind {
				continue
			}
			plain, err := frame.DecryptPayload()
			if err != nil {
				continue
			}
			if err := json.Unmarshal(plain, dst); err != nil {
				continue
			}
			return nil
		}
	}
	return fmt.Errorf("timeout after %d attempts", c.Retries)
}

func readInterchanger(ctx context.Context, conn *net.UDPConn, deadline time.Time) (InterchangerFrame, error) {
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	if err := conn.SetReadDeadline(deadline); err != nil {
		return InterchangerFrame{}, err
	}
	packet := make([]byte, 64*1024)
	n, err := conn.Read(packet)
	if err != nil {
		return InterchangerFrame{}, err
	}
	select {
	case <-ctx.Done():
		return InterchangerFrame{}, ctx.Err()
	default:
	}
	return DecodeInterchangerFrame(packet[:n])
}

func randomRequestID() ([32]byte, error) {
	var id [32]byte
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return id, fmt.Errorf("generate Interchanger request ID: %w", err)
	}
	hex.Encode(id[:], raw[:])
	return id, nil
}

func missingBlocks(first, last int, received map[uint32][]byte) []int {
	missing := make([]int, 0, last-first+1-len(received))
	for block := first; block <= last; block++ {
		if _, ok := received[uint32(block)]; !ok {
			missing = append(missing, block)
		}
	}
	return missing
}
