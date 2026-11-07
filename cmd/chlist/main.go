package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"newtv-session-bridge/internal/newtvauth"
)

type config struct {
	Endpoint     string          `json:"endpoint"`
	AuthEndpoint string          `json:"auth_endpoint"`
	AuthURL      string          `json:"auth_url"`
	Host         string          `json:"host"`
	Port         int             `json:"port"`
	Login        newtvauth.Login `json:"login"`
	APK          string          `json:"apk"`
	Sign         string          `json:"sign"`
	APKMD5       string          `json:"apk_md5"`
	SOMD5        string          `json:"so_md5"`
	OtherSO      string          `json:"other_so"`
	Arch         int             `json:"arch"`
	OSAPI        int             `json:"os_api"`
}

func main() {
	path := flag.String("config", "", "authorized NewTV chlist JSON")
	timeout := flag.Duration("timeout", 20*time.Second, "request timeout")
	flag.Parse()
	if *path == "" {
		fatal(fmt.Errorf("-config is required"))
	}
	f, err := os.Open(*path)
	if err != nil {
		fatal(err)
	}
	defer f.Close()
	var cfg config
	dec := json.NewDecoder(f)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		fatal(err)
	}
	if cfg.Host == "" {
		host, port, err := net.SplitHostPort(cfg.Endpoint)
		if err != nil {
			fatal(fmt.Errorf("endpoint: %w", err))
		}
		cfg.Host = host
		if _, err := fmt.Sscanf(port, "%d", &cfg.Port); err != nil {
			fatal(fmt.Errorf("endpoint port: %w", err))
		}
	}
	if cfg.APK != "" {
		proof, err := newtvauth.ReadAPKProof(cfg.APK)
		if err != nil {
			fatal(err)
		}
		if cfg.Sign != "" && cfg.Sign != proof.Sign || cfg.APKMD5 != "" && cfg.APKMD5 != proof.APKMD5 {
			fatal(fmt.Errorf("configured APK proof does not match %s", cfg.APK))
		}
		cfg.Sign, cfg.APKMD5 = proof.Sign, proof.APKMD5
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	request := newtvauth.ChlistRequest{
		Host: cfg.Host, Port: cfg.Port, Query: "?" + cfg.Login.Query(), Sign: cfg.Sign,
		APKMD5: cfg.APKMD5, SOMD5: cfg.SOMD5, OtherSO: cfg.OtherSO,
		Arch: cfg.Arch, OSAPI: cfg.OSAPI, Timestamp: time.Now().Unix(),
	}
	client := newtvauth.InterchangerClient{}
	if cfg.AuthEndpoint != "" || cfg.AuthURL != "" {
		if cfg.AuthEndpoint == "" || cfg.AuthURL == "" {
			fatal(fmt.Errorf("auth_endpoint and auth_url must be set together"))
		}
		separator := "?"
		if strings.Contains(cfg.AuthURL, "?") {
			separator = "&"
		}
		if _, err := client.AuthenticateRequest(ctx, cfg.AuthEndpoint, cfg.AuthURL+separator+cfg.Login.AuthQuery(), request); err != nil {
			fatal(err)
		}
		request.Timestamp = time.Now().Unix()
	}
	directory, err := client.FetchDirectory(ctx, cfg.Endpoint, request)
	if err != nil {
		fatal(err)
	}
	out := json.NewEncoder(os.Stdout)
	out.SetIndent("", "  ")
	if err := out.Encode(directory); err != nil {
		fatal(err)
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "chlist:", err)
	os.Exit(1)
}
