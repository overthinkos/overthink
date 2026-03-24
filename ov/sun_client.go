package main

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"time"
)

// SunshineClient is an HTTP client for the Sunshine REST API (port 47990, HTTPS).
type SunshineClient struct {
	BaseURL  string // e.g. "https://127.0.0.1:47990"
	Username string
	Password string
	client   *http.Client
}

// NewSunshineClient creates a client with TLS InsecureSkipVerify (self-signed cert).
func NewSunshineClient(baseURL, username, password string) *SunshineClient {
	return &SunshineClient{
		BaseURL:  baseURL,
		Username: username,
		Password: password,
		client: &http.Client{
			Timeout: 10 * time.Second,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			},
		},
	}
}

// doRequest executes an HTTP request with Basic Auth.
func (c *SunshineClient) doRequest(method, path string, body io.Reader) (*http.Response, error) {
	url := c.BaseURL + path
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		return nil, err
	}
	if c.Username != "" || c.Password != "" {
		req.SetBasicAuth(c.Username, c.Password)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("connecting to Sunshine API at %s: %w", url, err)
	}
	return resp, nil
}

// SunshineAPIResponse is the standard response envelope.
type SunshineAPIResponse struct {
	Status bool   `json:"status"`
	Error  string `json:"error,omitempty"`
}

// SunshineApp represents a GameStream application.
type SunshineApp struct {
	Name  string `json:"name"`
	Cmd   string `json:"cmd,omitempty"`
	Index int    `json:"index,omitempty"`
}

// SunshineClientInfo represents a paired Moonlight client.
type SunshineClientInfo struct {
	UUID string `json:"uuid,omitempty"`
	Name string `json:"name,omitempty"`
}

// GetConfig returns the current Sunshine configuration as a map.
func (c *SunshineClient) GetConfig() (map[string]any, error) {
	resp, err := c.doRequest("GET", "/api/config", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == 401 {
		return nil, fmt.Errorf("authentication failed (run 'ov sun passwd' to set credentials)")
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("unexpected status %d from GET /api/config", resp.StatusCode)
	}

	var result map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding config response: %w", err)
	}
	return result, nil
}

// SubmitPIN sends a pairing PIN to Sunshine.
func (c *SunshineClient) SubmitPIN(pin, name string) error {
	payload := map[string]string{"pin": pin}
	if name != "" {
		payload["name"] = name
	}
	body, _ := json.Marshal(payload)

	resp, err := c.doRequest("POST", "/api/pin", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == 401 {
		return fmt.Errorf("authentication failed (run 'ov sun passwd' to set credentials)")
	}

	var result SunshineAPIResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("decoding PIN response: %w", err)
	}
	if !result.Status {
		msg := "pairing failed"
		if result.Error != "" {
			msg += ": " + result.Error
		}
		return fmt.Errorf("%s (ensure Moonlight is waiting for PIN and try again)", msg)
	}
	return nil
}

// GetClients returns the list of paired Moonlight clients.
func (c *SunshineClient) GetClients() ([]SunshineClientInfo, error) {
	resp, err := c.doRequest("GET", "/api/clients/list", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == 401 {
		return nil, fmt.Errorf("authentication failed (run 'ov sun passwd' to set credentials)")
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("unexpected status %d from GET /api/clients/list", resp.StatusCode)
	}

	var result struct {
		NamedCerts []SunshineClientInfo `json:"named_certs"`
		Status     bool                 `json:"status"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding clients response: %w", err)
	}
	return result.NamedCerts, nil
}

// UnpairClient unpairs a specific client by UUID.
func (c *SunshineClient) UnpairClient(uuid string) error {
	body, _ := json.Marshal(map[string]string{"uuid": uuid})
	resp, err := c.doRequest("POST", "/api/clients/unpair", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == 401 {
		return fmt.Errorf("authentication failed")
	}

	var result SunshineAPIResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("decoding unpair response: %w", err)
	}
	if !result.Status {
		return fmt.Errorf("unpair failed (UUID may not exist)")
	}
	return nil
}

// UnpairAll unpairs all clients.
func (c *SunshineClient) UnpairAll() error {
	resp, err := c.doRequest("POST", "/api/clients/unpair-all", nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == 401 {
		return fmt.Errorf("authentication failed")
	}
	return nil
}

// GetApps returns the list of configured GameStream applications.
func (c *SunshineClient) GetApps() ([]SunshineApp, error) {
	resp, err := c.doRequest("GET", "/api/apps", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == 401 {
		return nil, fmt.Errorf("authentication failed (run 'ov sun passwd' to set credentials)")
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("unexpected status %d from GET /api/apps", resp.StatusCode)
	}

	var result struct {
		Apps []SunshineApp `json:"apps"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding apps response: %w", err)
	}
	return result.Apps, nil
}

// PostConfig updates Sunshine configuration.
func (c *SunshineClient) PostConfig(settings map[string]string) error {
	body, _ := json.Marshal(settings)
	resp, err := c.doRequest("POST", "/api/config", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == 401 {
		return fmt.Errorf("authentication failed")
	}

	var result SunshineAPIResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("decoding config response: %w", err)
	}
	if !result.Status {
		return fmt.Errorf("config update failed")
	}
	return nil
}

// SetPassword sets or changes Sunshine Web UI credentials via the API.
// For first-time setup, currentUser/currentPass should be empty strings.
func (c *SunshineClient) SetPassword(currentUser, currentPass, newUser, newPass string) error {
	payload := map[string]string{
		"currentUsername":    currentUser,
		"currentPassword":   currentPass,
		"newUsername":        newUser,
		"newPassword":        newPass,
		"confirmNewPassword": newPass,
	}
	body, _ := json.Marshal(payload)

	resp, err := c.doRequest("POST", "/api/password", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	data, _ := io.ReadAll(resp.Body)

	if resp.StatusCode == 401 {
		return fmt.Errorf("authentication failed — current credentials are incorrect")
	}
	if resp.StatusCode != 200 {
		return fmt.Errorf("password change failed (status %d): %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}

	var result SunshineAPIResponse
	if err := json.Unmarshal(data, &result); err == nil && !result.Status {
		return fmt.Errorf("password change failed")
	}
	return nil
}

// Restart requests Sunshine to restart via the API.
func (c *SunshineClient) Restart() error {
	resp, err := c.doRequest("POST", "/api/restart", nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return nil
}

// --- Resolution functions ---

func resolveSunContainer(image, instance string) (engine, name string, err error) {
	return resolveContainer(image, instance)
}

func resolveSunAddress(engine, containerName string) (string, error) {
	if engine == "" {
		return "https://127.0.0.1:47990", nil
	}
	cmd := exec.Command(engine, "port", containerName, "47990")
	output, err := cmd.Output()
	if err != nil {
		if isHostNetworked(engine, containerName) {
			return "https://127.0.0.1:47990", nil
		}
		return "", fmt.Errorf("no port mapping found for 47990 (is sunshine layer included?)")
	}
	return parseSunPort(string(output))
}

func parseSunPort(output string) (string, error) {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) == 0 || lines[0] == "" {
		return "", fmt.Errorf("no port mapping found for 47990")
	}
	hostPort := strings.TrimSpace(lines[0])
	hostPort = strings.Replace(hostPort, "0.0.0.0", "127.0.0.1", 1)
	if strings.HasPrefix(hostPort, "[::]:") {
		hostPort = "127.0.0.1:" + strings.TrimPrefix(hostPort, "[::]:")
	}
	return "https://" + hostPort, nil
}

func resolveSunCredentials(imageName, instance string) (username, password string, err error) {
	key := imageName
	if instance != "" {
		key = imageName + "-" + instance
	}

	// Resolve username: instance-specific key first, then image-level
	username, _ = ResolveCredential("SUNSHINE_USER", CredServiceSunshineUser, key, "")
	if username == "" && instance != "" {
		username, _ = ResolveCredential("SUNSHINE_USER", CredServiceSunshineUser, imageName, "")
	}

	// Resolve password: instance-specific key first, then image-level
	password, _ = ResolveCredential("SUNSHINE_PASSWORD", CredServiceSunshinePassword, key, "")
	if password == "" && instance != "" {
		password, _ = ResolveCredential("SUNSHINE_PASSWORD", CredServiceSunshinePassword, imageName, "")
	}

	if username == "" || password == "" {
		return "", "", fmt.Errorf("no Sunshine credentials for %s (run 'ov sun passwd %s' first)", key, imageName)
	}
	return username, password, nil
}

// connectSunshine resolves container, address, and credentials, returning a connected client.
func connectSunshine(image, instance string) (*SunshineClient, error) {
	engine, name, err := resolveSunContainer(image, instance)
	if err != nil {
		return nil, err
	}
	baseURL, err := resolveSunAddress(engine, name)
	if err != nil {
		return nil, err
	}
	imageName := resolveImageName(image)
	username, password, err := resolveSunCredentials(imageName, instance)
	if err != nil {
		return nil, err
	}
	return NewSunshineClient(baseURL, username, password), nil
}

// connectSunshineNoAuth resolves container and address only (for passwd bootstrap).
func connectSunshineNoAuth(image, instance string) (baseURL string, err error) {
	engine, name, err := resolveSunContainer(image, instance)
	if err != nil {
		return "", err
	}
	return resolveSunAddress(engine, name)
}
