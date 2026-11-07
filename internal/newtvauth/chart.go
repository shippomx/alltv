package newtvauth

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
)

type Chart struct {
	HBServers []string        `json:"hbservers"`
	NatOption json.RawMessage `json:"natoption"`
	Key       json.RawMessage `json:"key2"`
	PlayURL   json.RawMessage `json:"playurl"`
}

type ChartResources struct {
	Arg   json.RawMessage
	Chart json.RawMessage
}

// FetchChartResources obtains the two resources used by the native ChartMonitor.
func (c InterchangerClient) FetchChartResources(ctx context.Context, endpoint, host, name, product, keyID, version string) (ChartResources, error) {
	if endpoint == "" || host == "" || name == "" || product == "" || keyID == "" || version == "" {
		return ChartResources{}, fmt.Errorf("incomplete NewTV chart request")
	}
	argResource, err := chartResource(chartURL(host, name, product, "arg", keyID, version))
	if err != nil {
		return ChartResources{}, err
	}
	arg, err := c.fetchChartResource(ctx, endpoint, argResource)
	if err != nil {
		return ChartResources{}, fmt.Errorf("fetch NewTV channel arguments: %w", err)
	}
	chartResource, err := chartResource(chartURL(host, name, product, "chart", keyID, version))
	if err != nil {
		return ChartResources{}, err
	}
	chart, err := c.fetchChartResource(ctx, endpoint, chartResource)
	if err != nil {
		return ChartResources{}, fmt.Errorf("fetch NewTV provider chart: %w", err)
	}
	return ChartResources{Arg: arg, Chart: chart}, nil
}

func chartResource(target string) (string, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(map[string]string{"type": "chart", "target": target}); err != nil {
		return "", fmt.Errorf("encode NewTV chart resource: %w", err)
	}
	return b.String(), nil
}

func chartURL(host, name, product, kind, keyID, version string) string {
	return fmt.Sprintf("http://%s/chart?name=%s&product=%s&charttype=%s&keyid=%s&ver=%s&cm=gz",
		host, url.QueryEscape(name), url.QueryEscape(product), kind, url.QueryEscape(keyID), url.QueryEscape(version))
}

func (c InterchangerClient) fetchChartResource(ctx context.Context, endpoint, resource string) (json.RawMessage, error) {
	result, err := c.FetchResult(ctx, endpoint, resource)
	if err != nil {
		return nil, err
	}
	content := result.Content
	if result.Encoded {
		content, err = DecodeResource(string(content))
		if err != nil {
			return nil, fmt.Errorf("decode NewTV chart resource: %w", err)
		}
	}
	if result.Compression != "" && result.Compression != "none" && result.Compression != "gz" && result.Compression != "gzip" {
		return nil, fmt.Errorf("unsupported NewTV chart compression %q", result.Compression)
	}
	if result.Compression == "gz" || result.Compression == "gzip" || bytes.HasPrefix(content, []byte{0x1f, 0x8b}) {
		content, err = gunzipChart(content)
		if err != nil {
			return nil, err
		}
	}
	if !json.Valid(content) {
		return nil, fmt.Errorf("invalid NewTV chart JSON")
	}
	return append(json.RawMessage(nil), content...), nil
}

func DecodeChart(encoded string) (Chart, error) {
	compressed, err := DecodeResource(encoded)
	if err != nil {
		return Chart{}, err
	}
	decompressed, err := gunzipChart(compressed)
	if err != nil {
		return Chart{}, err
	}
	var chart Chart
	if err := json.Unmarshal(decompressed, &chart); err != nil {
		return Chart{}, fmt.Errorf("decode NewTV chart JSON: %w", err)
	}
	return chart, nil
}

func gunzipChart(compressed []byte) ([]byte, error) {
	zr, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		return nil, fmt.Errorf("open NewTV chart gzip: %w", err)
	}
	decompressed, err := io.ReadAll(zr)
	closeErr := zr.Close()
	if err != nil {
		return nil, fmt.Errorf("read NewTV chart gzip: %w", err)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("close NewTV chart gzip: %w", closeErr)
	}
	return decompressed, nil
}
