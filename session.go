package main

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"os"

	"newtv-session-bridge/internal/newtvauth"
	"newtv-session-bridge/internal/newtvproto"
)

type sessionFile struct {
	Channel         string   `json:"channel"`
	DeviceID        string   `json:"device_id"`
	ClientID        uint32   `json:"client_id"`
	Peers           []string `json:"peers"`
	HolePunchPeers  []string `json:"hole_punch_peers"`
	PunchPayload    string   `json:"punch_payload"`
	VODOption       int      `json:"vod_option"`
	VODAddressAdapt int      `json:"vod_address_adapt"`
}

func loadSession(path string) (newTVRuntime, error) {
	var file sessionFile
	raw, err := os.ReadFile(path)
	if err != nil {
		return newTVRuntime{}, err
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		return newTVRuntime{}, err
	}
	var config newtvproto.ClientConfig
	if err := decodeHex(config.Channel[:], file.Channel, "channel"); err != nil {
		return newTVRuntime{}, err
	}
	if err := decodeHex(config.DeviceID[:], file.DeviceID, "device_id"); err != nil {
		return newTVRuntime{}, err
	}
	if file.ClientID == 0 || len(file.Peers) == 0 {
		return newTVRuntime{}, fmt.Errorf("client_id and peers are required")
	}
	if file.VODOption < 0 || file.VODAddressAdapt < 0 || (file.VODOption == 0) != (file.VODAddressAdapt == 0) {
		return newTVRuntime{}, fmt.Errorf("vod_option and vod_address_adapt must be positive and set together")
	}
	config.ClientID = file.ClientID
	for _, rawPeer := range file.Peers {
		peer, err := net.ResolveUDPAddr("udp", rawPeer)
		if err != nil {
			return newTVRuntime{}, fmt.Errorf("peer %q: %w", rawPeer, err)
		}
		config.Peers = append(config.Peers, peer)
	}
	for _, rawPeer := range file.HolePunchPeers {
		peer, err := net.ResolveUDPAddr("udp", rawPeer)
		if err != nil {
			return newTVRuntime{}, fmt.Errorf("hole peer %q: %w", rawPeer, err)
		}
		config.HolePunchPeers = append(config.HolePunchPeers, peer)
	}
	if file.PunchPayload != "" {
		config.PunchPayload, err = hex.DecodeString(file.PunchPayload)
		if err != nil || len(config.PunchPayload) != 30 {
			return newTVRuntime{}, fmt.Errorf("punch_payload must be exactly 30 bytes of hexadecimal")
		}
	}
	return newTVRuntime{Client: config, VODOption: newtvauth.FakeOptionResponse{Option: file.VODOption, AddressAdapt: file.VODAddressAdapt}}, nil
}

func decodeHex(dst []byte, value, name string) error {
	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) != len(dst) {
		return fmt.Errorf("%s must be exactly %d hexadecimal bytes", name, len(dst))
	}
	copy(dst, decoded)
	return nil
}
