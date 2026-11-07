package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"time"
)

func streamFile(ctx context.Context, dst io.Writer, path string, bitrate int64) error {
	const chunk = 188 * 100
	buf := make([]byte, chunk)
	interval := time.Duration(int64(time.Second) * chunk * 8 / bitrate)
	if interval < time.Millisecond {
		interval = time.Millisecond
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		wrote := false
		for {
			n, readErr := io.ReadFull(f, buf)
			if n > 0 {
				wrote = true
				select {
				case <-ctx.Done():
					_ = f.Close()
					return ctx.Err()
				case <-ticker.C:
				}
				if _, err := dst.Write(buf[:n]); err != nil {
					_ = f.Close()
					return err
				}
				if flusher, ok := dst.(http.Flusher); ok {
					flusher.Flush()
				}
			}
			if readErr != nil {
				break
			}
		}
		_ = f.Close()
		if err := ctx.Err(); err != nil {
			return err
		}
		if !wrote {
			return errors.New("empty input")
		}
	}
}
