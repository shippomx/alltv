package main

import (
	"bufio"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

func (b *bridge) proxyStream(ctx context.Context, dst http.ResponseWriter) error {
	return b.proxyURL(ctx, dst, b.upstream, "")
}

func (b *bridge) hlsResource(dst http.ResponseWriter, req *http.Request) {
	resourceURL, err := b.decodeHLSURL(req.PathValue("token"))
	if err != nil {
		http.NotFound(dst, req)
		return
	}
	_ = b.proxyURL(req.Context(), dst, resourceURL, req.Header.Get("Range"))
}

func (b *bridge) proxyURL(ctx context.Context, dst http.ResponseWriter, resourceURL, byteRange string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, resourceURL, nil)
	if err != nil {
		http.Error(dst, err.Error(), http.StatusBadGateway)
		return err
	}
	if req.URL.Host == b.upstreamHost {
		for name, values := range b.upstreamAuth {
			for _, value := range values {
				req.Header.Add(name, value)
			}
		}
	}
	if byteRange != "" {
		req.Header.Set("Range", byteRange)
	}
	resp, err := b.httpClient.Do(req)
	if err != nil {
		http.Error(dst, err.Error(), http.StatusBadGateway)
		return err
	}
	defer resp.Body.Close()

	manifest := resp.StatusCode >= 200 && resp.StatusCode < 300 && isHLSManifest(resp)
	for _, name := range []string{"Content-Type", "Accept-Ranges", "Content-Range"} {
		if value := resp.Header.Get(name); value != "" {
			dst.Header().Set(name, value)
		}
	}
	if !manifest && resp.ContentLength >= 0 {
		dst.Header().Set("Content-Length", fmt.Sprint(resp.ContentLength))
	}
	dst.WriteHeader(resp.StatusCode)
	if manifest {
		return b.rewriteHLS(dst, resp.Body, resp.Request.URL)
	}
	if _, err := io.Copy(dst, resp.Body); err != nil {
		return fmt.Errorf("copy upstream response: %w", err)
	}
	return nil
}

func isHLSManifest(resp *http.Response) bool {
	contentType := strings.ToLower(resp.Header.Get("Content-Type"))
	return strings.Contains(contentType, "mpegurl") || strings.HasSuffix(strings.ToLower(resp.Request.URL.Path), ".m3u8")
}

func (b *bridge) rewriteHLS(dst io.Writer, src io.Reader, base *url.URL) error {
	scanner := bufio.NewScanner(src)
	scanner.Buffer(make([]byte, 64*1024), 1<<20)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "#") {
			line = b.rewriteURIAttributes(line, base)
		} else if strings.TrimSpace(line) != "" {
			line = b.bridgeHLSURL(line, base)
		}
		if _, err := fmt.Fprintln(dst, line); err != nil {
			return err
		}
	}
	return scanner.Err()
}

func (b *bridge) rewriteURIAttributes(line string, base *url.URL) string {
	const prefix = `URI="`
	for start := 0; ; {
		i := strings.Index(line[start:], prefix)
		if i < 0 {
			return line
		}
		i += start + len(prefix)
		j := strings.IndexByte(line[i:], '"')
		if j < 0 {
			return line
		}
		j += i
		rewritten := b.bridgeHLSURL(line[i:j], base)
		line = line[:i] + rewritten + line[j:]
		start = i + len(rewritten) + 1
	}
}

func (b *bridge) bridgeHLSURL(raw string, base *url.URL) string {
	ref, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return raw
	}
	if ref.Scheme != "" && ref.Scheme != "http" && ref.Scheme != "https" {
		return raw
	}
	target := base.ResolveReference(ref).String()
	mac := hmac.New(sha256.New, b.hlsKey)
	_, _ = io.WriteString(mac, target)
	token := base64.RawURLEncoding.EncodeToString([]byte(target)) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)[:16])
	return "/hls/" + token
}

func (b *bridge) decodeHLSURL(token string) (string, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return "", fmt.Errorf("invalid HLS token")
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return "", err
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, b.hlsKey)
	_, _ = mac.Write(raw)
	if !hmac.Equal(signature, mac.Sum(nil)[:16]) {
		return "", fmt.Errorf("invalid HLS signature")
	}
	parsed, err := url.Parse(string(raw))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return "", fmt.Errorf("invalid HLS URL")
	}
	return parsed.String(), nil
}
