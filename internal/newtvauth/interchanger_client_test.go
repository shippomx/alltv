package newtvauth

import (
	"context"
	"encoding/json"
	"net"
	"testing"
	"time"
)

func TestInterchangerFetchReordersAndRetries(t *testing.T) {
	const fileID = "0123456789abcdef0123456789abcdef"
	server, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	done := make(chan error, 1)
	go func() {
		buf := make([]byte, 4096)
		sent := map[int]bool{}
		omittedOne := false
		for {
			n, peer, err := server.ReadFromUDP(buf)
			if err != nil {
				done <- err
				return
			}
			frame, err := DecodeInterchangerFrame(buf[:n])
			if err != nil || frame.Kind == 0xff {
				continue
			}
			plain, err := frame.DecryptPayload()
			if err != nil {
				continue
			}
			switch frame.Kind {
			case 1:
				err = sendJSONFrame(server, peer, frame, map[string]any{"notice": "", "sync_seq": 100, "blks_per_req": 2, "content_encode_flag": 1})
			case 2:
				if err = sendRawJSONFrame(server, peer, frame, nil); err != nil {
					break
				}
				err = sendJSONFrame(server, peer, frame, map[string]any{"notice": "", "file_id": fileID, "file_size": 10, "blk_size": 4, "header_blk": 0, "tail_blk": 2, "compress_method": "none"})
			case 3:
				var request struct {
					Blocks []int `json:"blocks"`
				}
				if err = json.Unmarshal(plain, &request); err == nil {
					for _, block := range request.Blocks {
						if block == 1 && !omittedOne { // Force one retry after blocks 0 and 2 arrive.
							omittedOne = true
							continue
						}
						parts := [][]byte{[]byte("ABCD"), []byte("EFGH"), []byte("IJ")}
						var responseID [32]byte
						copy(responseID[:], fileID)
						packet, encodeErr := EncodeInterchangerFrame(InterchangerFrame{Version: 1, Kind: 3, KeySelector: 0xff, IVSelector: 0xff, RequestID: responseID, Sequence: uint32(block), Payload: parts[block]})
						if encodeErr != nil {
							err = encodeErr
							break
						}
						_, err = server.WriteToUDP(packet, peer)
						sent[block] = true
					}
				}
			case 0x1a:
				if !sent[0] || !sent[1] || !sent[2] || frame.Sequence != 105 {
					err = &net.AddrError{Err: "invalid Interchanger finish", Addr: peer.String()}
				}
				done <- err
				return
			}
			if err != nil {
				done <- err
				return
			}
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	content, err := (InterchangerClient{Timeout: 30 * time.Millisecond, Retries: 4}).Fetch(ctx, server.LocalAddr().String(), `{"type":"fixture"}`)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "ABCDEFGHIJ" {
		t.Fatalf("content = %q", content)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestInterchangerAuthenticateReturnsRandomAndFinishes(t *testing.T) {
	server, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	done := make(chan error, 1)
	go func() {
		buf := make([]byte, 4096)
		for {
			n, peer, err := server.ReadFromUDP(buf)
			if err != nil {
				done <- err
				return
			}
			frame, err := DecodeInterchangerFrame(buf[:n])
			if err != nil || frame.Kind == 0xff {
				continue
			}
			switch frame.Kind {
			case 1:
				err = sendJSONFrame(server, peer, frame, map[string]any{"notice": "", "sync_seq": 50, "blks_per_req": 1})
			case 2:
				err = sendJSONFrame(server, peer, frame, map[string]any{"notice": "", "random": "0001E240"})
			case 0x1a:
				if frame.Sequence != ^uint32(0) {
					err = &net.AddrError{Err: "invalid auth finish", Addr: peer.String()}
				}
				done <- err
				return
			}
			if err != nil {
				done <- err
				return
			}
		}
	}()
	random, err := (InterchangerClient{Timeout: 30 * time.Millisecond}).Authenticate(context.Background(), server.LocalAddr().String(), `{"type":"auth"}`)
	if err != nil {
		t.Fatal(err)
	}
	if random != 123456 {
		t.Fatalf("random = %d", random)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func sendJSONFrame(conn *net.UDPConn, peer *net.UDPAddr, request InterchangerFrame, value any) error {
	packet, err := EncodeInterchangerJSON(request.Kind, 10, 20, request.RequestID, request.Sequence, value)
	if err != nil {
		return err
	}
	_, err = conn.WriteToUDP(packet, peer)
	return err
}

func sendRawJSONFrame(conn *net.UDPConn, peer *net.UDPAddr, request InterchangerFrame, plain []byte) error {
	encrypted, err := EncryptInterchangerPayload(10, 20, plain)
	if err != nil {
		return err
	}
	packet, err := EncodeInterchangerFrame(InterchangerFrame{Version: 1, Kind: request.Kind, KeySelector: 10, IVSelector: 20, RequestID: request.RequestID, Sequence: request.Sequence, Payload: encrypted})
	if err != nil {
		return err
	}
	_, err = conn.WriteToUDP(packet, peer)
	return err
}

func TestMissingBlocks(t *testing.T) {
	got := missingBlocks(2, 5, map[uint32][]byte{2: {1}, 4: {1}})
	if len(got) != 2 || got[0] != 3 || got[1] != 5 {
		t.Fatalf("missing = %v", got)
	}
}
