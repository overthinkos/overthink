package main

import (
	"crypto"
	"crypto/aes"
	"crypto/rand"
	"errors"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"encoding/xml"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// GameStreamClient implements the GameStream HTTPS control plane (ports 47984/47989).
type GameStreamClient struct {
	HTTPBaseURL  string // "http://127.0.0.1:<47989>"
	HTTPSBaseURL string // "https://127.0.0.1:<47984>"
	UniqueID     string // 16 hex chars
	CertDir      string // directory for cert/key/server files
	clientCert   tls.Certificate
	clientKey    *rsa.PrivateKey
	clientCertDER []byte // raw DER of client cert for signature extraction
}

// ServerInfo represents the XML response from /serverinfo.
type ServerInfo struct {
	XMLName     xml.Name `xml:"root"`
	StatusCode  int      `xml:"status_code,attr"`
	Hostname    string   `xml:"hostname"`
	UniqueID    string   `xml:"uniqueid"`
	PairStatus  string   `xml:"PairStatus"`
	CurrentGame int      `xml:"currentgame"`
	State       string   `xml:"state"`
	GPUType     string   `xml:"gputype"`
	AppVersion  string   `xml:"appversion"`
	HttpsPort   int      `xml:"HttpsPort"`
}

// GameStreamApp represents an app from /applist.
type GameStreamApp struct {
	Name string `xml:"AppTitle"`
	ID   int    `xml:"ID"`
}

type appListRoot struct {
	XMLName xml.Name        `xml:"root"`
	Apps    []GameStreamApp `xml:"App"`
}

// pairResponse is the XML response for pairing phases.
type pairResponse struct {
	XMLName           xml.Name `xml:"root"`
	Paired            int      `xml:"paired"`
	PlainCert         string   `xml:"plaincert"`
	ChallengeResponse string   `xml:"challengeresponse"`
	PairingSecret     string   `xml:"pairingsecret"`
}

// launchResponse is the XML response for /launch.
type launchResponse struct {
	XMLName     xml.Name `xml:"root"`
	SessionURL  string   `xml:"sessionUrl0"`
	GameSession int      `xml:"gamesession"`
}

// cancelResponse is the XML response for /cancel.
type cancelResponse struct {
	XMLName xml.Name `xml:"root"`
	Cancel  int      `xml:"cancel"`
}

// --- Certificate management ---

// ensureMoonCert generates or loads client cert + unique ID. Returns the cert directory.
func ensureMoonCert(image, instance string) (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("determining config directory: %w", err)
	}
	key := resolveImageName(image)
	if instance != "" {
		key = key + "-" + instance
	}
	certDir := filepath.Join(configDir, "ov", "moonlight", key)
	if err := os.MkdirAll(certDir, 0700); err != nil {
		return "", fmt.Errorf("creating cert directory: %w", err)
	}

	certPath := filepath.Join(certDir, "client.crt")
	keyPath := filepath.Join(certDir, "client.key")

	if _, err := os.Stat(certPath); os.IsNotExist(err) {
		if err := generateMoonCert(certPath, keyPath); err != nil {
			return "", err
		}
	}

	return certDir, nil
}

func generateMoonCert(certPath, keyPath string) error {
	privKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return fmt.Errorf("generating RSA key: %w", err)
	}

	template := &x509.Certificate{
		SerialNumber: big.NewInt(0),
		Subject:      pkix.Name{CommonName: "NVIDIA GameStream Client"},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(10 * 365 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &privKey.PublicKey, privKey)
	if err != nil {
		return fmt.Errorf("creating certificate: %w", err)
	}

	certFile, err := os.Create(certPath)
	if err != nil {
		return err
	}
	defer certFile.Close()
	if err := pem.Encode(certFile, &pem.Block{Type: "CERTIFICATE", Bytes: certDER}); err != nil {
		return err
	}

	keyFile, err := os.OpenFile(keyPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	defer keyFile.Close()
	return pem.Encode(keyFile, &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(privKey)})
}

func loadOrGenerateUniqueID(certDir string) (string, error) {
	path := filepath.Join(certDir, "uniqueid")
	data, err := os.ReadFile(path)
	if err == nil {
		id := strings.TrimSpace(string(data))
		if len(id) == 16 {
			return id, nil
		}
	}
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	id := strings.ToUpper(hex.EncodeToString(b))
	return id, os.WriteFile(path, []byte(id), 0600)
}

// --- Port resolution ---

func resolveMoonContainer(image, instance string) (engine, name string, err error) {
	return resolveContainer(image, instance)
}

func resolveMoonHTTPAddress(engine, containerName string) (string, error) {
	if engine == "" {
		return "http://127.0.0.1:47989", nil
	}
	cmd := exec.Command(engine, "port", containerName, "47989")
	output, err := cmd.Output()
	if err != nil {
		if isHostNetworked(engine, containerName) {
			return "http://127.0.0.1:47989", nil
		}
		return "", fmt.Errorf("no port mapping found for 47989 (is sunshine layer included?)")
	}
	return parseMoonPort(string(output), "http")
}

func resolveMoonHTTPSAddress(engine, containerName string) (string, error) {
	if engine == "" {
		return "https://127.0.0.1:47984", nil
	}
	cmd := exec.Command(engine, "port", containerName, "47984")
	output, err := cmd.Output()
	if err != nil {
		if isHostNetworked(engine, containerName) {
			return "https://127.0.0.1:47984", nil
		}
		return "", fmt.Errorf("no port mapping found for 47984")
	}
	return parseMoonPort(string(output), "https")
}

func parseMoonPort(output, scheme string) (string, error) {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) == 0 || lines[0] == "" {
		return "", fmt.Errorf("no port mapping found")
	}
	hostPort := strings.TrimSpace(lines[0])
	hostPort = strings.Replace(hostPort, "0.0.0.0", "127.0.0.1", 1)
	if strings.HasPrefix(hostPort, "[::]:") {
		hostPort = "127.0.0.1:" + strings.TrimPrefix(hostPort, "[::]:")
	}
	return scheme + "://" + hostPort, nil
}

// --- Client constructor ---

func newGameStreamClient(image, instance string) (*GameStreamClient, error) {
	engine, name, err := resolveMoonContainer(image, instance)
	if err != nil {
		return nil, err
	}

	httpURL, err := resolveMoonHTTPAddress(engine, name)
	if err != nil {
		return nil, err
	}
	httpsURL, err := resolveMoonHTTPSAddress(engine, name)
	if err != nil {
		return nil, err
	}

	certDir, err := ensureMoonCert(image, instance)
	if err != nil {
		return nil, err
	}

	uniqueID, err := loadOrGenerateUniqueID(certDir)
	if err != nil {
		return nil, err
	}

	certPath := filepath.Join(certDir, "client.crt")
	keyPath := filepath.Join(certDir, "client.key")

	clientCert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return nil, fmt.Errorf("loading client certificate: %w", err)
	}

	// Load private key for signing operations.
	keyPEM, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, err
	}
	keyBlock, _ := pem.Decode(keyPEM)
	privKey, err := x509.ParsePKCS1PrivateKey(keyBlock.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parsing private key: %w", err)
	}

	// Load raw cert DER for signature extraction.
	certPEM, err := os.ReadFile(certPath)
	if err != nil {
		return nil, err
	}
	certBlock, _ := pem.Decode(certPEM)

	return &GameStreamClient{
		HTTPBaseURL:   httpURL,
		HTTPSBaseURL:  httpsURL,
		UniqueID:      uniqueID,
		CertDir:       certDir,
		clientCert:    clientCert,
		clientKey:     privKey,
		clientCertDER: certBlock.Bytes,
	}, nil
}

// --- HTTP helpers ---

func (c *GameStreamClient) httpGet(baseURL, path string, useMutualTLS bool) ([]byte, error) {
	return c.httpGetTimeout(baseURL, path, useMutualTLS, 15*time.Second)
}

func (c *GameStreamClient) httpGetTimeout(baseURL, path string, useMutualTLS bool, timeout time.Duration) ([]byte, error) {
	transport := &http.Transport{
		TLSClientConfig:   &tls.Config{InsecureSkipVerify: true},
		DisableKeepAlives: true,
	}
	if useMutualTLS {
		transport.TLSClientConfig.Certificates = []tls.Certificate{c.clientCert}
	}
	client := &http.Client{Timeout: timeout, Transport: transport}

	req, err := http.NewRequest("GET", baseURL+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Connection", "close")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

func (c *GameStreamClient) buildQuery(params map[string]string) string {
	v := url.Values{}
	v.Set("uniqueid", c.UniqueID)
	v.Set("uuid", generateUUID())
	for k, val := range params {
		v.Set(k, val)
	}
	return "?" + v.Encode()
}

func generateUUID() string {
	b := make([]byte, 16)
	rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

// --- Protocol operations ---

// ServerInfo queries /serverinfo (no pairing required).
func (c *GameStreamClient) ServerInfo() (*ServerInfo, error) {
	query := c.buildQuery(nil)
	data, err := c.httpGet(c.HTTPBaseURL, "/serverinfo"+query, false)
	if err != nil {
		return nil, err
	}
	var info ServerInfo
	if err := xml.Unmarshal(data, &info); err != nil {
		return nil, fmt.Errorf("parsing serverinfo: %w", err)
	}
	return &info, nil
}

// AppList queries /applist (requires pairing, mutual TLS).
func (c *GameStreamClient) AppList() ([]GameStreamApp, error) {
	query := c.buildQuery(nil)
	data, err := c.httpGet(c.HTTPSBaseURL, "/applist"+query, true)
	if err != nil {
		return nil, fmt.Errorf("applist: %w", err)
	}
	var root appListRoot
	if err := xml.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("parsing applist: %w", err)
	}
	return root.Apps, nil
}

// Launch starts a streaming session for an app.
func (c *GameStreamClient) Launch(appID int) error {
	rikey := make([]byte, 16)
	rand.Read(rikey)
	rikeyidBytes := make([]byte, 4)
	rand.Read(rikeyidBytes)
	rikeyid := int(rikeyidBytes[0])<<24 | int(rikeyidBytes[1])<<16 | int(rikeyidBytes[2])<<8 | int(rikeyidBytes[3])

	query := c.buildQuery(map[string]string{
		"appid":            fmt.Sprintf("%d", appID),
		"mode":             "1920x1080x60",
		"additionalStates": "1",
		"sops":             "1",
		"rikey":            hex.EncodeToString(rikey),
		"rikeyid":          fmt.Sprintf("%d", rikeyid),
		"localAudioPlayMode": "0",
		"surroundAudioInfo": fmt.Sprintf("%d", (0x3<<16)|2), // stereo
	})

	data, err := c.httpGet(c.HTTPSBaseURL, "/launch"+query, true)
	if err != nil {
		return fmt.Errorf("launch: %w", err)
	}
	var resp launchResponse
	if err := xml.Unmarshal(data, &resp); err != nil {
		return fmt.Errorf("parsing launch response: %w", err)
	}
	if resp.GameSession == 0 {
		return fmt.Errorf("launch failed (server may be busy or app not found)")
	}
	return nil
}

// Quit sends /cancel to stop the running app.
func (c *GameStreamClient) Quit() error {
	query := c.buildQuery(nil)
	data, err := c.httpGet(c.HTTPSBaseURL, "/cancel"+query, true)
	if err != nil {
		return fmt.Errorf("cancel: %w", err)
	}
	var resp cancelResponse
	if err := xml.Unmarshal(data, &resp); err != nil {
		return fmt.Errorf("parsing cancel response: %w", err)
	}
	if resp.Cancel == 0 {
		return fmt.Errorf("quit failed (no app running?)")
	}
	return nil
}

// Unpair removes this client's pairing.
func (c *GameStreamClient) Unpair() error {
	query := c.buildQuery(nil)
	_, err := c.httpGet(c.HTTPBaseURL, "/unpair"+query, false)
	if err != nil {
		return fmt.Errorf("unpair: %w", err)
	}
	// Remove stored server cert.
	os.Remove(filepath.Join(c.CertDir, "server.crt"))
	return nil
}

// IsPaired checks if server.crt exists (we've previously paired).
func (c *GameStreamClient) IsPaired() bool {
	_, err := os.Stat(filepath.Join(c.CertDir, "server.crt"))
	return err == nil
}

// --- Pairing handshake ---

// Pair performs the 5-phase GameStream pairing handshake.
// pin must be a 4-digit string (e.g., "1234").
func (c *GameStreamClient) Pair(pin string) error {
	// Clear stale pairing state from previous aborted attempts.
	// Sunshine maintains an exclusive map_id_sess — only one pairing can be in-flight.
	// Without this, a previous aborted handshake leaves the server in a bad state,
	// causing getservercert to get an immediate EOF (connection closed).
	c.httpGet(c.HTTPBaseURL, "/unpair"+c.buildQuery(nil), false)

	// Phase 1: getservercert
	salt := make([]byte, 16)
	rand.Read(salt)

	certPEM, err := os.ReadFile(filepath.Join(c.CertDir, "client.crt"))
	if err != nil {
		return fmt.Errorf("reading client cert: %w", err)
	}

	query := c.buildQuery(map[string]string{
		"devicename":  "ov",
		"updateState": "1",
		"phrase":      "getservercert",
		"salt":        hex.EncodeToString(salt),
		"clientcert":  hex.EncodeToString(certPEM),
	})
	// Phase 1 blocks by design: Sunshine holds the HTTP response open until the PIN
	// is submitted via POST /api/pin (port 47990). The real Moonlight client uses
	// timeout=0 (infinite). We use 5 minutes as a safety net.
	data, err := c.httpGetTimeout(c.HTTPBaseURL, "/pair"+query, false, 5*time.Minute)
	if err != nil {
		if isEOFError(err) {
			return fmt.Errorf("pairing rejected — server closed connection (stale state?). Try: ov sun restart <image>")
		}
		return fmt.Errorf("phase 1 (getservercert): %w", err)
	}
	var phase1 pairResponse
	if err := xml.Unmarshal(data, &phase1); err != nil {
		return fmt.Errorf("parsing phase 1: %w", err)
	}
	if phase1.Paired == 0 {
		return fmt.Errorf("phase 1 failed (server rejected pairing request)")
	}
	if phase1.PlainCert == "" {
		return fmt.Errorf("phase 1: no server cert received (another client may be pairing)")
	}

	// Parse server certificate. Sunshine sends the PEM cert hex-encoded.
	serverCertBytes, err := hex.DecodeString(phase1.PlainCert)
	if err != nil {
		return fmt.Errorf("decoding server cert hex: %w", err)
	}
	// Try PEM decode first (Sunshine sends PEM hex-encoded).
	var serverCertDER []byte
	if block, _ := pem.Decode(serverCertBytes); block != nil {
		serverCertDER = block.Bytes
	} else {
		// Fallback: assume raw DER.
		serverCertDER = serverCertBytes
	}
	serverCert, err := x509.ParseCertificate(serverCertDER)
	if err != nil {
		return fmt.Errorf("parsing server cert: %w", err)
	}

	// Derive AES key from salt + PIN.
	aesKey := deriveAESKey(salt, []byte(pin))

	// Phase 2: clientchallenge
	challengeData := make([]byte, 16)
	rand.Read(challengeData)
	challengeEnc := aesECBEncrypt(aesKey, challengeData)

	query = c.buildQuery(map[string]string{
		"devicename":      "ov",
		"updateState":     "1",
		"phrase":          "clientchallenge",
		"clientchallenge": hex.EncodeToString(challengeEnc),
	})
	data, err = c.httpGet(c.HTTPBaseURL, "/pair"+query, false)
	if err != nil {
		return fmt.Errorf("phase 2 (clientchallenge): %w", err)
	}
	var phase2 pairResponse
	if err := xml.Unmarshal(data, &phase2); err != nil {
		return fmt.Errorf("parsing phase 2: %w", err)
	}
	if phase2.Paired == 0 {
		return fmt.Errorf("phase 2 failed")
	}

	// Decrypt challenge response.
	challengeRespEnc, err := hex.DecodeString(phase2.ChallengeResponse)
	if err != nil {
		return fmt.Errorf("decoding challenge response: %w", err)
	}
	challengeRespDec := aesECBDecrypt(aesKey, challengeRespEnc)
	hashLen := 32 // SHA-256
	if len(challengeRespDec) < hashLen+16 {
		return fmt.Errorf("challenge response too short (%d bytes)", len(challengeRespDec))
	}
	serverResponse := challengeRespDec[:hashLen]
	serverChallenge := challengeRespDec[hashLen : hashLen+16]

	// Phase 3: serverchallengeresp
	clientSecret := make([]byte, 16)
	rand.Read(clientSecret)

	// Extract client cert signature.
	clientCertX509, err := x509.ParseCertificate(c.clientCertDER)
	if err != nil {
		return fmt.Errorf("parsing client cert DER: %w", err)
	}
	clientCertSig := clientCertX509.Signature

	// Build hash: SHA256(serverChallenge || clientCertSig || clientSecret)
	h := sha256.New()
	h.Write(serverChallenge)
	h.Write(clientCertSig)
	h.Write(clientSecret)
	clientHash := h.Sum(nil)

	// Pad to 32 bytes if needed (SHA-256 is already 32).
	clientHashEnc := aesECBEncrypt(aesKey, padTo16Multiple(clientHash))

	query = c.buildQuery(map[string]string{
		"devicename":          "ov",
		"updateState":         "1",
		"phrase":              "serverchallengeresp",
		"serverchallengeresp": hex.EncodeToString(clientHashEnc),
	})
	data, err = c.httpGet(c.HTTPBaseURL, "/pair"+query, false)
	if err != nil {
		return fmt.Errorf("phase 3 (serverchallengeresp): %w", err)
	}
	var phase3 pairResponse
	if err := xml.Unmarshal(data, &phase3); err != nil {
		return fmt.Errorf("parsing phase 3: %w", err)
	}
	if phase3.Paired == 0 {
		return fmt.Errorf("phase 3 failed")
	}

	// Verify server's pairing secret.
	pairingSecretBytes, err := hex.DecodeString(phase3.PairingSecret)
	if err != nil {
		return fmt.Errorf("decoding pairing secret: %w", err)
	}
	if len(pairingSecretBytes) < 16 {
		return fmt.Errorf("pairing secret too short")
	}
	serverSecret := pairingSecretBytes[:16]
	serverSignature := pairingSecretBytes[16:]

	// Verify server signature over serverSecret using server's public key.
	if err := verifyServerSignature(serverCert, serverSecret, serverSignature); err != nil {
		return fmt.Errorf("MITM check failed — server signature invalid: %w", err)
	}

	// Verify PIN: SHA256(challengeData || serverCertSig || serverSecret) == serverResponse
	serverCertSig := serverCert.Signature
	pinHash := sha256.New()
	pinHash.Write(challengeData)
	pinHash.Write(serverCertSig)
	pinHash.Write(serverSecret)
	expectedResponse := pinHash.Sum(nil)

	if !equalBytes(expectedResponse, serverResponse) {
		return fmt.Errorf("pairing failed: PIN mismatch (verify PIN and try again)")
	}

	// Phase 4: clientpairingsecret — sign clientSecret with SHA-256 + RSA PKCS#1 v1.5.
	secretHash := sha256.Sum256(clientSecret)
	signature, err := rsa.SignPKCS1v15(rand.Reader, c.clientKey, crypto.SHA256, secretHash[:])
	if err != nil {
		return fmt.Errorf("signing client secret: %w", err)
	}
	clientPairingSecret := append(clientSecret, signature...)

	query = c.buildQuery(map[string]string{
		"devicename":          "ov",
		"updateState":         "1",
		"phrase":              "clientpairingsecret",
		"clientpairingsecret": hex.EncodeToString(clientPairingSecret),
	})
	data, err = c.httpGet(c.HTTPBaseURL, "/pair"+query, false)
	if err != nil {
		return fmt.Errorf("phase 4 (clientpairingsecret): %w", err)
	}
	var phase4 pairResponse
	if err := xml.Unmarshal(data, &phase4); err != nil {
		return fmt.Errorf("parsing phase 4: %w", err)
	}
	if phase4.Paired == 0 {
		return fmt.Errorf("phase 4 failed (server rejected client secret)")
	}

	// Phase 5: pairchallenge (HTTPS with mutual TLS)
	query = c.buildQuery(map[string]string{
		"devicename":  "ov",
		"updateState": "1",
		"phrase":      "pairchallenge",
	})
	data, err = c.httpGet(c.HTTPSBaseURL, "/pair"+query, true)
	if err != nil {
		return fmt.Errorf("phase 5 (pairchallenge HTTPS): %w", err)
	}
	var phase5 pairResponse
	if err := xml.Unmarshal(data, &phase5); err != nil {
		return fmt.Errorf("parsing phase 5: %w", err)
	}
	if phase5.Paired == 0 {
		return fmt.Errorf("phase 5 failed (HTTPS verification rejected)")
	}

	// Store pinned server cert.
	serverCertPath := filepath.Join(c.CertDir, "server.crt")
	if err := os.WriteFile(serverCertPath, serverCertDER, 0600); err != nil {
		return fmt.Errorf("saving server cert: %w", err)
	}

	return nil
}

// --- Crypto helpers ---

// deriveAESKey derives a 16-byte AES key from salt + PIN using SHA-256.
func deriveAESKey(salt, pin []byte) []byte {
	h := sha256.New()
	h.Write(salt)
	h.Write(pin)
	return h.Sum(nil)[:16]
}

// aesECBEncrypt encrypts data using AES-128-ECB (no padding). Data must be multiple of 16 bytes.
func aesECBEncrypt(key, data []byte) []byte {
	block, _ := aes.NewCipher(key)
	out := make([]byte, len(data))
	for i := 0; i < len(data); i += aes.BlockSize {
		block.Encrypt(out[i:i+aes.BlockSize], data[i:i+aes.BlockSize])
	}
	return out
}

// aesECBDecrypt decrypts data using AES-128-ECB (no padding). Data must be multiple of 16 bytes.
func aesECBDecrypt(key, data []byte) []byte {
	block, _ := aes.NewCipher(key)
	out := make([]byte, len(data))
	for i := 0; i < len(data); i += aes.BlockSize {
		block.Decrypt(out[i:i+aes.BlockSize], data[i:i+aes.BlockSize])
	}
	return out
}

// padTo16Multiple pads data to a multiple of 16 bytes with zeros.
func padTo16Multiple(data []byte) []byte {
	if len(data)%16 == 0 {
		return data
	}
	padded := make([]byte, ((len(data)/16)+1)*16)
	copy(padded, data)
	return padded
}

func verifyServerSignature(serverCert *x509.Certificate, data, sig []byte) error {
	pubKey, ok := serverCert.PublicKey.(*rsa.PublicKey)
	if !ok {
		return fmt.Errorf("server cert has non-RSA public key")
	}
	// Try SHA-256 first (modern Sunshine/GFE gen >= 7).
	hashed := sha256.Sum256(data)
	if err := rsa.VerifyPKCS1v15(pubKey, crypto.SHA256, hashed[:], sig); err == nil {
		return nil
	}
	// Fallback: raw signature without hashing (some implementations).
	return rsa.VerifyPKCS1v15(pubKey, 0, data, sig)
}

func isEOFError(err error) bool {
	return err != nil && (errors.Is(err, io.EOF) || strings.Contains(err.Error(), "EOF"))
}

func equalBytes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
