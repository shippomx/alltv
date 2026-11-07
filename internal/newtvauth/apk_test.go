package newtvauth

import (
	"archive/zip"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"os"
	"testing"
	"time"
)

func TestReadAPKProof(t *testing.T) {
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "fixture"}, NotBefore: time.Unix(0, 0), NotAfter: time.Unix(3600, 0)}
	cert, err := x509.CreateCertificate(rand.Reader, template, template, key.Public(), key)
	if err != nil {
		t.Fatal(err)
	}
	path := t.TempDir() + "/fixture.apk"
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	w, _ := zw.Create("META-INF/FIXTURE.RSA")
	_, _ = w.Write(cert)
	w, _ = zw.Create("classes.dex")
	_, _ = w.Write([]byte("fixture"))
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	proof, err := ReadAPKProof(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(proof.Sign) != 64 || len(proof.APKMD5) != 32 {
		t.Fatalf("proof = %#v", proof)
	}
}
