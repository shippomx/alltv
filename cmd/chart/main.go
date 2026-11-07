package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"newtv-session-bridge/internal/newtvauth"
)

type config struct {
	Endpoint string `json:"endpoint"`
	Host     string `json:"host"`
	Name     string `json:"name"`
	Product  string `json:"product"`
	KeyID    string `json:"key_id"`
	Version  string `json:"version"`
}

func main() {
	path := flag.String("config", "", "authorized NewTV chart JSON")
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
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	resources, err := (newtvauth.InterchangerClient{}).FetchChartResources(ctx, cfg.Endpoint, cfg.Host, cfg.Name, cfg.Product, cfg.KeyID, cfg.Version)
	if err != nil {
		fatal(err)
	}
	out := json.NewEncoder(os.Stdout)
	out.SetIndent("", "  ")
	if err := out.Encode(resources); err != nil {
		fatal(err)
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "chart:", err)
	os.Exit(1)
}
