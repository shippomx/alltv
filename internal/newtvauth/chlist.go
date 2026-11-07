package newtvauth

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"strings"
)

const SoftwareVersion = "20260628"

type ChlistRequest struct {
	Host, Query, Sign, APKMD5, SOMD5, OtherSO string
	Port, Arch, OSAPI                         int
	Rooted                                    bool
	Timestamp                                 int64
}

func ChlistVerify(timestamp int64, sign, apkMD5, soMD5 string) string {
	sum := md5.Sum([]byte(strconv.FormatInt(timestamp, 10) + "&" + sign + "$" + soMD5 + "!" + apkMD5))
	return hex.EncodeToString(sum[:])
}

func (r ChlistRequest) Resource() (string, error) {
	if r.Host == "" || r.Port <= 0 || r.Port > 65535 {
		return "", fmt.Errorf("incomplete NewTV chlist request")
	}
	if !strings.HasPrefix(r.Query, "?") {
		return "", fmt.Errorf("NewTV chlist query must start with ?")
	}
	return r.signedResource("chlist", "http://"+net.JoinHostPort(r.Host, strconv.Itoa(r.Port))+"/chlist"+r.Query)
}

// AuthResource wraps a legal account login URL in the native icAuth envelope.
func (r ChlistRequest) AuthResource(target string) (string, error) {
	target = strings.TrimPrefix(strings.TrimPrefix(target, "http://"), "https://")
	if !strings.Contains(target, "?") {
		return "", fmt.Errorf("NewTV auth target must contain a login query")
	}
	return r.signedResource("auth", "http://"+target)
}

func (r ChlistRequest) signedResource(kind, target string) (string, error) {
	if r.Timestamp <= 0 || r.Arch <= 0 || r.OSAPI <= 0 || r.OtherSO == "" {
		return "", fmt.Errorf("incomplete NewTV %s request", kind)
	}
	if decoded, err := hex.DecodeString(r.Sign); err != nil || len(decoded) != 32 {
		return "", fmt.Errorf("sign must be exactly 32 hexadecimal bytes")
	}
	if decoded, err := hex.DecodeString(r.APKMD5); err != nil || len(decoded) != 16 {
		return "", fmt.Errorf("apk_md5 must be exactly 16 hexadecimal bytes")
	}
	if r.SOMD5 == "" {
		r.SOMD5 = "none"
	}
	if r.SOMD5 != "none" {
		if decoded, err := hex.DecodeString(r.SOMD5); err != nil || len(decoded) != 16 {
			return "", fmt.Errorf("so_md5 must be none or exactly 16 hexadecimal bytes")
		}
	}
	rooted := 0
	if r.Rooted {
		rooted = 1
	}
	target = fmt.Sprintf("%s&sover=%s&arch=%d&sign=%s&apkmd5=%s&somd5=%s&osapi=%d&rooted=%d&timestamp=%d&verify=%s&otherso=%s", target, SoftwareVersion, r.Arch, r.Sign, r.APKMD5, r.SOMD5, r.OSAPI, rooted, r.Timestamp, ChlistVerify(r.Timestamp, r.Sign, r.APKMD5, r.SOMD5), r.OtherSO)
	if kind == "auth" {
		return `{"type":"auth","target":` + strconv.Quote(target) + `}`, nil
	}
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(map[string]string{"type": kind, "target": target}); err != nil {
		return "", err
	}
	return strings.TrimSuffix(b.String(), "\n"), nil
}

func (c InterchangerClient) AuthenticateRequest(ctx context.Context, endpoint, target string, request ChlistRequest) (int, error) {
	resource, err := request.AuthResource(target)
	if err != nil {
		return 0, err
	}
	return c.Authenticate(ctx, endpoint, resource)
}

func (c InterchangerClient) FetchDirectory(ctx context.Context, endpoint string, request ChlistRequest) (Directory, error) {
	resource, err := request.Resource()
	if err != nil {
		return Directory{}, err
	}
	result, err := c.FetchResult(ctx, endpoint, resource)
	if err != nil {
		return Directory{}, err
	}
	content := result.Content
	if result.Encoded {
		content, err = DecodeResource(string(content))
		if err != nil {
			return Directory{}, fmt.Errorf("decode NewTV chlist resource: %w", err)
		}
	}
	if result.Compression != "" && result.Compression != "none" {
		return Directory{}, fmt.Errorf("unsupported NewTV chlist compression %q", result.Compression)
	}
	return ParseDirectory(bytes.NewReader(content))
}
