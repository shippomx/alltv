package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"newtv-session-bridge/internal/newtvauth"
)

func main() {
	endpoint := flag.String("endpoint", "", "Interchanger UDP endpoint")
	target := flag.String("target", "", "authorized static resource URL")
	out := flag.String("out", "", "output file (stdout when empty)")
	timeout := flag.Duration("timeout", 20*time.Second, "request timeout")
	flag.Parse()
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	content, err := (newtvauth.InterchangerClient{}).FetchStatic(ctx, *endpoint, *target)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if *out == "" {
		_, err = os.Stdout.Write(content)
	} else {
		err = os.WriteFile(*out, content, 0600)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
