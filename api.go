package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type apiClient struct {
	base  string
	token string
	http  *http.Client
}

func newAPIClient(base, token string) *apiClient {
	return &apiClient{
		base:  strings.TrimRight(base, "/"),
		token: token,
		http:  &http.Client{Timeout: 30 * time.Second},
	}
}

type signupResp struct {
	UserUUID  string `json:"user_uuid"`
	Email     string `json:"email"`
	APIToken  string `json:"api_token"`
	CreatedAt string `json:"created_at"`
}

type shareResp struct {
	UUID      string `json:"uuid"`
	ShortID   string `json:"short_id"`
	Filename  string `json:"filename"`
	Watch     bool   `json:"watch"`
	URL       string `json:"url"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
	SizeBytes int    `json:"size_bytes"`
}

func (c *apiClient) do(method, path string, body, dst any) error {
	var rdr io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal: %w", err)
		}
		rdr = bytes.NewReader(buf)
	}
	req, err := http.NewRequest(method, c.base+path, rdr)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	if dst == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(dst); err != nil {
		return fmt.Errorf("decode: %w", err)
	}
	return nil
}

func (c *apiClient) Signup(email string) (*signupResp, error) {
	var out signupResp
	if err := c.do("POST", "/api/signup", map[string]string{"email": email}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *apiClient) CreateShare(filename, content string, watch bool) (*shareResp, error) {
	var out shareResp
	if err := c.do("POST", "/api/shares", map[string]any{
		"filename": filename,
		"content":  content,
		"watch":    watch,
	}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *apiClient) UpdateShare(uuid, content string) (*shareResp, error) {
	var out shareResp
	if err := c.do("PUT", fmt.Sprintf("/api/shares/%s", uuid), map[string]string{"content": content}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *apiClient) DeleteShare(uuid string) error {
	return c.do("DELETE", fmt.Sprintf("/api/shares/%s", uuid), nil, nil)
}

func (c *apiClient) ListShares() ([]shareResp, error) {
	var out []shareResp
	if err := c.do("GET", "/api/shares", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *apiClient) ListSharesByFilename(filename string) ([]shareResp, error) {
	var out []shareResp
	path := "/api/shares?filename=" + url.QueryEscape(filename)
	if err := c.do("GET", path, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *apiClient) GetShareByShortID(shortID string) (*shareResp, error) {
	all, err := c.ListShares()
	if err != nil {
		return nil, err
	}
	for i := range all {
		if all[i].ShortID == shortID {
			return &all[i], nil
		}
	}
	return nil, fmt.Errorf("share %s not found in your account", shortID)
}
