package newtvauth

import (
	"archive/zip"
	"crypto/md5"
	"crypto/sha256"
	"crypto/x509"
	"encoding/asn1"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type APKProof struct {
	Sign   string
	APKMD5 string
}

// ReadAPKProof reproduces the APK certificate SHA-256 and whole-file MD5 used by the native client.
func ReadAPKProof(path string) (APKProof, error) {
	f, err := os.Open(path)
	if err != nil {
		return APKProof{}, err
	}
	h := md5.New()
	if _, err := io.Copy(h, f); err != nil {
		f.Close()
		return APKProof{}, fmt.Errorf("hash APK: %w", err)
	}
	if err := f.Close(); err != nil {
		return APKProof{}, err
	}
	zr, err := zip.OpenReader(path)
	if err != nil {
		return APKProof{}, fmt.Errorf("open APK: %w", err)
	}
	defer zr.Close()
	for _, entry := range zr.File {
		name := strings.ToUpper(filepath.ToSlash(entry.Name))
		if !strings.HasPrefix(name, "META-INF/") || (!strings.HasSuffix(name, ".RSA") && !strings.HasSuffix(name, ".DSA") && !strings.HasSuffix(name, ".EC")) {
			continue
		}
		r, err := entry.Open()
		if err != nil {
			return APKProof{}, err
		}
		der, err := io.ReadAll(io.LimitReader(r, 16<<20))
		closeErr := r.Close()
		if err != nil || closeErr != nil {
			return APKProof{}, fmt.Errorf("read APK signer: %v", firstError(err, closeErr))
		}
		cert := findCertificate(der)
		if cert == nil {
			continue
		}
		sign := sha256.Sum256(cert.Raw)
		return APKProof{Sign: hex.EncodeToString(sign[:]), APKMD5: hex.EncodeToString(h.Sum(nil))}, nil
	}
	return APKProof{}, fmt.Errorf("APK contains no parseable signing certificate")
}

func findCertificate(der []byte) *x509.Certificate {
	for len(der) > 0 {
		var raw asn1.RawValue
		rest, err := asn1.Unmarshal(der, &raw)
		if err != nil {
			return nil
		}
		if raw.Tag == asn1.TagSequence {
			if cert, err := x509.ParseCertificate(raw.FullBytes); err == nil {
				return cert
			}
		}
		if raw.IsCompound {
			if cert := findCertificate(raw.Bytes); cert != nil {
				return cert
			}
		}
		der = rest
	}
	return nil
}

func firstError(a, b error) error {
	if a != nil {
		return a
	}
	return b
}
