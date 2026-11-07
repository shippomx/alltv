package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"newtv-session-bridge/internal/newtvauth"
	"newtv-session-bridge/internal/newtvproto"
)

type newTVConfig struct {
	AuthEndpoint  string          `json:"auth_endpoint"`
	AuthURL       string          `json:"auth_url"`
	TrialEndpoint string          `json:"trial_endpoint"`
	TrialRefCode  string          `json:"trial_refcode"`
	Directory     string          `json:"directory_endpoint"`
	DirectoryHost string          `json:"directory_host"`
	DirectoryPort int             `json:"directory_port"`
	ChartEndpoint string          `json:"chart_endpoint"`
	ChartHost     string          `json:"chart_host"`
	ChartKeyID    string          `json:"chart_key_id"`
	ChartVersion  string          `json:"chart_version"`
	ProviderID    string          `json:"provider_id"`
	ClientID      uint32          `json:"client_id"`
	PunchPayload  string          `json:"punch_payload"`
	Login         newtvauth.Login `json:"login"`
	APK           string          `json:"apk"`
	Sign          string          `json:"sign"`
	APKMD5        string          `json:"apk_md5"`
	SOMD5         string          `json:"so_md5"`
	OtherSO       string          `json:"other_so"`
	Arch          int             `json:"arch"`
	OSAPI         int             `json:"os_api"`
}

type newTVRuntime struct {
	Client       newtvproto.ClientConfig
	Channels     []newTVChannel
	VODOption    newtvauth.FakeOptionResponse
	RefreshAfter time.Duration
}

type newTVChannel struct {
	ID      string
	Name    string
	Group   string
	Clients []newtvproto.ClientConfig
}

func loadNewTVClient(ctx context.Context, path string) (newTVRuntime, error) {
	return loadNewTVRuntime(ctx, path, false)
}

func loadNewTVRuntime(ctx context.Context, path string, authenticate bool) (newTVRuntime, error) {
	return loadNewTVRuntimeWithAuth(ctx, path, authenticate, nil)
}

func loadNewTVRuntimeWithAuth(ctx context.Context, path string, authenticate bool, authenticated func(uint32)) (newTVRuntime, error) {
	f, err := os.Open(path)
	if err != nil {
		return newTVRuntime{}, err
	}
	defer f.Close()
	var cfg newTVConfig
	dec := json.NewDecoder(f)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return newTVRuntime{}, fmt.Errorf("decode NewTV config: %w", err)
	}
	if cfg.APK != "" {
		proof, err := newtvauth.ReadAPKProof(cfg.APK)
		if err != nil {
			return newTVRuntime{}, err
		}
		if cfg.Sign != "" && cfg.Sign != proof.Sign || cfg.APKMD5 != "" && cfg.APKMD5 != proof.APKMD5 {
			return newTVRuntime{}, fmt.Errorf("configured APK proof does not match %s", cfg.APK)
		}
		cfg.Sign, cfg.APKMD5 = proof.Sign, proof.APKMD5
	}
	if cfg.AuthEndpoint == "" || cfg.AuthURL == "" || cfg.Directory == "" || cfg.ChartEndpoint == "" || cfg.Login.Name == "" {
		return newTVRuntime{}, fmt.Errorf("incomplete NewTV runtime config")
	}
	if cfg.DirectoryHost == "" {
		host, port, err := net.SplitHostPort(cfg.Directory)
		if err != nil {
			return newTVRuntime{}, fmt.Errorf("directory endpoint: %w", err)
		}
		cfg.DirectoryHost = host
		if _, err := fmt.Sscanf(port, "%d", &cfg.DirectoryPort); err != nil {
			return newTVRuntime{}, fmt.Errorf("directory endpoint port: %w", err)
		}
	}
	if cfg.ChartHost == "" {
		cfg.ChartHost = cfg.ChartEndpoint
	}
	if cfg.ChartKeyID == "" {
		cfg.ChartKeyID = "key2"
	}
	if cfg.ChartVersion == "" {
		cfg.ChartVersion = "20231212"
	}
	if authenticate {
		if cfg.TrialEndpoint == "" {
			host, _, splitErr := net.SplitHostPort(cfg.AuthEndpoint)
			if splitErr != nil {
				return newTVRuntime{}, fmt.Errorf("auth endpoint: %w", splitErr)
			}
			cfg.TrialEndpoint = net.JoinHostPort(host, "1818")
		}
		if cfg.TrialRefCode == "" {
			cfg.TrialRefCode = "358770"
		}
		trial, trialErr := (newtvauth.FakeOptionClient{Timeout: 2 * time.Second, Retries: 5}).FetchTrialAccount(ctx, cfg.TrialEndpoint, cfg.Login.AndroidID, cfg.TrialRefCode, cfg.Login.Device, cfg.Login.CPU, cfg.Login.Memory)
		if trialErr != nil {
			return newTVRuntime{}, fmt.Errorf("refresh NewTV trial account: %w", trialErr)
		}
		cfg.Login.Name, cfg.Login.Password = trial.Account, trial.Password
	}
	request := newtvauth.ChlistRequest{
		Host: cfg.DirectoryHost, Port: cfg.DirectoryPort, Query: "?" + cfg.Login.Query(), Sign: cfg.Sign,
		APKMD5: cfg.APKMD5, SOMD5: cfg.SOMD5, OtherSO: cfg.OtherSO, Arch: cfg.Arch, OSAPI: cfg.OSAPI, Timestamp: time.Now().Unix(),
	}
	separator := "?"
	if strings.Contains(cfg.AuthURL, "?") {
		separator = "&"
	}
	client := newtvauth.InterchangerClient{Retries: 8}
	clientID := int(cfg.ClientID)
	if authenticate {
		clientID = 0
	}
	if clientID == 0 {
		clientID, err = client.AuthenticateRequest(ctx, cfg.AuthEndpoint, cfg.AuthURL+separator+cfg.Login.AuthQuery(), request)
		if err != nil {
			return newTVRuntime{}, fmt.Errorf("authenticate NewTV account: %w", err)
		}
		if authenticated != nil {
			authenticated(uint32(clientID))
		}
	}
	request.Timestamp = time.Now().Unix()
	directory, err := client.FetchDirectory(ctx, cfg.Directory, request)
	if err != nil {
		return newTVRuntime{}, fmt.Errorf("fetch NewTV directory: %w", err)
	}
	var vodOption newtvauth.FakeOptionResponse
	if endpoint := directory.Config["fake_option_addr"]; endpoint != "" {
		options := newtvauth.FakeOptionClient{}
		if _, err := options.Fetch(ctx, endpoint, cfg.Login.Name, "hbservers", 0); err == nil {
			// shortcut: the native detector reports cone NAT as 1 on this runtime; implement its 10-byte probe when other NATs must be supported.
			vodOption, _ = options.Fetch(ctx, endpoint, cfg.Login.Name, "natoption", 1)
		}
	}
	resources, err := client.FetchChartResources(ctx, cfg.ChartEndpoint, cfg.ChartHost, cfg.Login.Name, newtvauth.Product, cfg.ChartKeyID, cfg.ChartVersion)
	if err != nil {
		return newTVRuntime{}, err
	}
	index, err := newtvauth.ParseChartIndex(resources)
	if err != nil {
		return newTVRuntime{}, err
	}
	var authorized []newtvauth.AuthorizedChannel
	if cfg.ProviderID != "" {
		selected, err := index.Resolve(cfg.ProviderID, cfg.Login.Name, uint32(clientID))
		if err != nil {
			return newTVRuntime{}, err
		}
		authorized = []newtvauth.AuthorizedChannel{selected}
	} else {
		authorized, err = index.ResolveAll(cfg.Login.Name, uint32(clientID))
	}
	if err != nil {
		return newTVRuntime{}, err
	}
	if len(authorized) == 0 {
		return newTVRuntime{}, fmt.Errorf("NewTV chart contains no authorized channels")
	}
	labels := directoryChannelLabels(directory)
	if cfg.ProviderID == "" {
		byID := make(map[string]newtvauth.AuthorizedChannel, len(authorized))
		for _, item := range authorized {
			byID[item.ProviderID] = item
		}
		authorized = authorized[:0]
		for _, id := range directoryChannelOrder(directory) {
			if item, ok := byID[id]; ok {
				authorized = append(authorized, item)
				delete(byID, id)
			}
		}
	}
	result := newTVRuntime{VODOption: vodOption, RefreshAfter: refreshAfter(directory.Config["trial_minute"])}
	var punchPayload []byte
	if cfg.PunchPayload != "" {
		punchPayload, err = hex.DecodeString(cfg.PunchPayload)
		if err != nil || len(punchPayload) != 30 {
			return newTVRuntime{}, fmt.Errorf("punch_payload must be exactly 30 bytes of hexadecimal")
		}
	}
	for _, item := range authorized {
		name, group := item.Name, "NewTV"
		label, listed := labels[item.ProviderID]
		if cfg.ProviderID == "" && !listed {
			continue
		}
		if listed {
			name, group = label.Name, label.Group
		}
		var clients []newtvproto.ClientConfig
		for _, endpoints := range item.EndpointSets {
			clientConfig := newtvproto.ClientConfig{Channel: item.Channel, DeviceID: item.DeviceID, ClientID: item.ClientID, PunchPayload: punchPayload}
			for _, address := range endpoints.Peers {
				peer, err := net.ResolveUDPAddr("udp", address)
				if err != nil {
					return newTVRuntime{}, err
				}
				clientConfig.Peers = append(clientConfig.Peers, peer)
			}
			for _, address := range endpoints.HolePunchPeers {
				peer, err := net.ResolveUDPAddr("udp", address)
				if err != nil {
					return newTVRuntime{}, err
				}
				clientConfig.HolePunchPeers = append(clientConfig.HolePunchPeers, peer)
			}
			clients = append(clients, clientConfig)
		}
		result.Channels = append(result.Channels, newTVChannel{ID: item.ProviderID, Name: name, Group: group, Clients: clients})
	}
	if len(result.Channels) == 0 {
		return newTVRuntime{}, fmt.Errorf("NewTV directory contains no playable channels")
	}
	result.Client = result.Channels[0].Clients[0]
	return result, nil
}

func directoryChannelLabels(directory newtvauth.Directory) map[string]channel {
	labels := make(map[string]channel)
	for _, category := range directory.Categories {
		for _, entry := range category.Channels {
			for _, item := range entry.Items {
				if _, exists := labels[item.URL]; item.URL != "" && !exists {
					labels[item.URL] = channel{Name: entry.Name, Group: category.Name}
				}
			}
		}
	}
	return labels
}

func directoryChannelOrder(directory newtvauth.Directory) []string {
	var order []string
	seen := make(map[string]bool)
	for _, category := range directory.Categories {
		for _, entry := range category.Channels {
			for _, item := range entry.Items {
				if item.URL != "" && !seen[item.URL] {
					order = append(order, item.URL)
					seen[item.URL] = true
				}
			}
		}
	}
	return order
}

func refreshAfter(trialMinutes string) time.Duration {
	minutes, err := time.ParseDuration(trialMinutes + "m")
	if err != nil || minutes <= time.Minute {
		return 14 * time.Minute
	}
	return minutes - time.Minute
}
