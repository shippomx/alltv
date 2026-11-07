package newtvauth

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// FetchStatic implements VideoClient.icStatic for an authorized resource URL.
func (c InterchangerClient) FetchStatic(ctx context.Context, endpoint, target string) ([]byte, error) {
	if endpoint == "" || target == "" {
		return nil, fmt.Errorf("incomplete NewTV static request")
	}
	separator := "?"
	if strings.Contains(target, "?") {
		separator = "&"
	}
	resource, err := json.Marshal(map[string]string{"type": "file", "target": target + separator + "cm=gz"})
	if err != nil {
		return nil, err
	}
	result, err := c.FetchResult(ctx, endpoint, string(resource))
	if err != nil {
		return nil, err
	}
	return decodeStaticResult(result)
}

func decodeStaticResult(result InterchangerResult) ([]byte, error) {
	content := result.Content
	var err error
	if result.Encoded {
		content, err = DecodeResource(string(content))
		if err != nil {
			return nil, fmt.Errorf("decode NewTV static resource: %w", err)
		}
	}
	if decoded, decodeErr := base64.StdEncoding.DecodeString(string(content)); decodeErr == nil {
		content = decoded
	}
	if result.Compression == "gz" || result.Compression == "gzip" || bytes.HasPrefix(content, []byte{0x1f, 0x8b}) {
		zr, openErr := gzip.NewReader(bytes.NewReader(content))
		if openErr != nil {
			return nil, fmt.Errorf("open NewTV static gzip: %w", openErr)
		}
		content, err = io.ReadAll(zr)
		closeErr := zr.Close()
		if err != nil {
			return nil, fmt.Errorf("read NewTV static gzip: %w", err)
		}
		if closeErr != nil {
			return nil, fmt.Errorf("close NewTV static gzip: %w", closeErr)
		}
	} else if result.Compression != "" && result.Compression != "none" {
		return nil, fmt.Errorf("unsupported NewTV static compression %q", result.Compression)
	}
	return content, nil
}
