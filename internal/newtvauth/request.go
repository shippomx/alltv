package newtvauth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"strconv"
)

const (
	Product = "newtv"
	Version = "44150"
	Build   = "2026.10.29.1"
)

type Login struct {
	Name      string
	Password  string
	AndroidID string
	Device    string
	CPU       string
	Memory    string
}

func (l Login) Query() string {
	q := url.Values{}
	q.Set("name", l.Name)
	q.Set("pass", l.Password)
	q.Set("androidid", l.AndroidID)
	q.Set("lang", "cn")
	q.Set("ver", Version)
	q.Set("buildver", Build)
	q.Set("package", "tv.newtv")
	q.Set("devname", l.Device)
	q.Set("cpu", l.CPU)
	q.Set("mem", l.Memory)
	q.Set("jsp", "index.jsp")
	return q.Encode()
}

func (l Login) AuthQuery() string {
	// Native login sends this literal order without URL encoding.
	return fmt.Sprintf("name=%s&pass=%s&androidid=%s&lang=cn&ver=%s&buildver=%s&package=tv.newtv&devname=%s&cpu=%s&mem=%s", l.Name, l.Password, l.AndroidID, Version, Build, l.Device, l.CPU, l.Memory)
}

// AccountProof matches the HMAC request used by the account-status endpoint.
func AccountProof(name, androidID string, timestamp int64) url.Values {
	q := url.Values{
		"androidid": {androidID},
		"name":      {name},
		"product":   {Product},
		"timestamp": {strconv.FormatInt(timestamp, 10)},
		"ver":       {"Ver1000"},
	}
	key := name + strconv.FormatInt(timestamp, 10)
	for len(key) < 32 {
		key += "0"
	}
	mac := hmac.New(sha256.New, []byte(key))
	mac.Write([]byte("androidid=" + androidID + "&name=" + name + "&product=" + Product + "&timestamp=" + strconv.FormatInt(timestamp, 10) + "&ver=Ver1000"))
	q.Set("sign", hex.EncodeToString(mac.Sum(nil)))
	return q
}
