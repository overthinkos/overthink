package main

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"encoding/xml"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestParseMoonPort(t *testing.T) {
	tests := []struct {
		name    string
		output  string
		scheme  string
		want    string
		wantErr bool
	}{
		{"http localhost", "127.0.0.1:47989\n", "http", "http://127.0.0.1:47989", false},
		{"https all interfaces", "0.0.0.0:47984\n", "https", "https://127.0.0.1:47984", false},
		{"ipv6 binding", "[::]:47989\n", "http", "http://127.0.0.1:47989", false},
		{"random high port", "0.0.0.0:49989\n", "http", "http://127.0.0.1:49989", false},
		{"multiple lines", "0.0.0.0:47984\n[::]:47984\n", "https", "https://127.0.0.1:47984", false},
		{"empty output", "", "http", "", true},
		{"only whitespace", "  \n", "http", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseMoonPort(tt.output, tt.scheme)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseMoonPort() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("parseMoonPort() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDeriveAESKey(t *testing.T) {
	salt := make([]byte, 16)
	for i := range salt {
		salt[i] = byte(i)
	}
	pin := []byte("1234")

	key := deriveAESKey(salt, pin)
	if len(key) != 16 {
		t.Fatalf("key length = %d, want 16", len(key))
	}

	// Verify deterministic: same inputs produce same output.
	key2 := deriveAESKey(salt, pin)
	if !equalBytes(key, key2) {
		t.Error("deriveAESKey not deterministic")
	}

	// Different PIN produces different key.
	key3 := deriveAESKey(salt, []byte("5678"))
	if equalBytes(key, key3) {
		t.Error("different PINs produced same key")
	}
}

func TestAESECBRoundtrip(t *testing.T) {
	key := make([]byte, 16)
	rand.Read(key)

	plaintext := make([]byte, 16)
	rand.Read(plaintext)

	encrypted := aesECBEncrypt(key, plaintext)
	if equalBytes(encrypted, plaintext) {
		t.Error("encrypted == plaintext")
	}

	decrypted := aesECBDecrypt(key, encrypted)
	if !equalBytes(decrypted, plaintext) {
		t.Error("roundtrip failed: decrypted != plaintext")
	}
}

func TestAESECBMultiBlock(t *testing.T) {
	key := make([]byte, 16)
	rand.Read(key)

	// 32 bytes = 2 blocks
	plaintext := make([]byte, 32)
	rand.Read(plaintext)

	encrypted := aesECBEncrypt(key, plaintext)
	decrypted := aesECBDecrypt(key, encrypted)
	if !equalBytes(decrypted, plaintext) {
		t.Error("multi-block roundtrip failed")
	}
}

func TestGenerateClientCert(t *testing.T) {
	tmpDir := t.TempDir()
	certPath := filepath.Join(tmpDir, "client.crt")
	keyPath := filepath.Join(tmpDir, "client.key")

	if err := generateMoonCert(certPath, keyPath); err != nil {
		t.Fatalf("generateMoonCert() error: %v", err)
	}

	// Verify cert exists and is valid.
	certPEM, err := os.ReadFile(certPath)
	if err != nil {
		t.Fatalf("reading cert: %v", err)
	}

	// Parse PEM to verify it's valid.
	block, _ := pem.Decode(certPEM)
	if block == nil {
		t.Fatal("no PEM block found in cert")
	}

	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("parsing cert: %v", err)
	}
	if cert.Subject.CommonName != "NVIDIA GameStream Client" {
		t.Errorf("CN = %q, want 'NVIDIA GameStream Client'", cert.Subject.CommonName)
	}
	if cert.PublicKeyAlgorithm != x509.RSA {
		t.Errorf("key algorithm = %v, want RSA", cert.PublicKeyAlgorithm)
	}
	rsaKey, ok := cert.PublicKey.(*rsa.PublicKey)
	if !ok {
		t.Fatal("public key is not RSA")
	}
	if rsaKey.N.BitLen() != 2048 {
		t.Errorf("key size = %d bits, want 2048", rsaKey.N.BitLen())
	}

	// Verify key exists.
	if _, err := os.Stat(keyPath); err != nil {
		t.Fatalf("key file missing: %v", err)
	}
}

func TestUniqueIDGeneration(t *testing.T) {
	tmpDir := t.TempDir()

	id1, err := loadOrGenerateUniqueID(tmpDir)
	if err != nil {
		t.Fatalf("first call error: %v", err)
	}
	if len(id1) != 16 {
		t.Errorf("ID length = %d, want 16", len(id1))
	}
	// Verify hex.
	if _, err := hex.DecodeString(id1); err != nil {
		t.Errorf("ID is not valid hex: %q", id1)
	}

	// Second call should return the same ID.
	id2, err := loadOrGenerateUniqueID(tmpDir)
	if err != nil {
		t.Fatalf("second call error: %v", err)
	}
	if id1 != id2 {
		t.Errorf("ID not persistent: %q != %q", id1, id2)
	}
}

func TestServerInfoParsing(t *testing.T) {
	xmlData := `<?xml version="1.0" encoding="utf-8"?>
<root status_code="200">
  <hostname>sunshine-test</hostname>
  <uniqueid>ABC123</uniqueid>
  <PairStatus>1</PairStatus>
  <currentgame>0</currentgame>
  <state>SUNSHINE_SERVER_FREE</state>
  <gputype>NVIDIA GeForce RTX 4080 SUPER</gputype>
  <appversion>7.1.431.0</appversion>
  <HttpsPort>47984</HttpsPort>
</root>`

	var info ServerInfo
	if err := xml.Unmarshal([]byte(xmlData), &info); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if info.Hostname != "sunshine-test" {
		t.Errorf("hostname = %q, want sunshine-test", info.Hostname)
	}
	if info.GPUType != "NVIDIA GeForce RTX 4080 SUPER" {
		t.Errorf("gpu = %q", info.GPUType)
	}
	if info.PairStatus != "1" {
		t.Errorf("pair status = %q, want 1", info.PairStatus)
	}
	if info.CurrentGame != 0 {
		t.Errorf("currentgame = %d, want 0", info.CurrentGame)
	}
	if info.HttpsPort != 47984 {
		t.Errorf("https port = %d, want 47984", info.HttpsPort)
	}
}

func TestAppListParsing(t *testing.T) {
	xmlData := `<?xml version="1.0" encoding="utf-8"?>
<root status_code="200">
  <App>
    <AppTitle>Desktop</AppTitle>
    <ID>1</ID>
    <IsHdrSupported>0</IsHdrSupported>
  </App>
  <App>
    <AppTitle>Steam</AppTitle>
    <ID>2</ID>
    <IsHdrSupported>1</IsHdrSupported>
  </App>
</root>`

	var root appListRoot
	if err := xml.Unmarshal([]byte(xmlData), &root); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if len(root.Apps) != 2 {
		t.Fatalf("got %d apps, want 2", len(root.Apps))
	}
	if root.Apps[0].Name != "Desktop" || root.Apps[0].ID != 1 {
		t.Errorf("app[0] = %+v", root.Apps[0])
	}
	if root.Apps[1].Name != "Steam" || root.Apps[1].ID != 2 {
		t.Errorf("app[1] = %+v", root.Apps[1])
	}
}

func TestSignAndVerify(t *testing.T) {
	privKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generating key: %v", err)
	}

	secret := make([]byte, 16)
	rand.Read(secret)

	// Sign with SHA-256.
	hashed := sha256.Sum256(secret)
	sig, err := rsa.SignPKCS1v15(rand.Reader, privKey, crypto.SHA256, hashed[:])
	if err != nil {
		t.Fatalf("signing: %v", err)
	}

	// Create a cert for verification.
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "test"},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(time.Hour),
	}
	certDER, _ := x509.CreateCertificate(rand.Reader, template, template, &privKey.PublicKey, privKey)
	cert, _ := x509.ParseCertificate(certDER)

	if err := verifyServerSignature(cert, secret, sig); err != nil {
		t.Errorf("verify failed: %v", err)
	}

	// Tamper with signature — should fail.
	sig[0] ^= 0xff
	if err := verifyServerSignature(cert, secret, sig); err == nil {
		t.Error("expected verification failure for tampered signature")
	}
}

func TestPadTo16Multiple(t *testing.T) {
	tests := []struct {
		inputLen int
		wantLen  int
	}{
		{16, 16},
		{32, 32},
		{1, 16},
		{15, 16},
		{17, 32},
		{31, 32},
	}
	for _, tt := range tests {
		data := make([]byte, tt.inputLen)
		padded := padTo16Multiple(data)
		if len(padded) != tt.wantLen {
			t.Errorf("padTo16Multiple(%d bytes) = %d bytes, want %d", tt.inputLen, len(padded), tt.wantLen)
		}
	}
}
