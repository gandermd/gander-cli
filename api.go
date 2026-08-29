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

type signupIntentResp struct {
	IntentID  string `json:"intent_id"`
	SignupURL string `json:"signup_url"`
	ExpiresAt string `json:"expires_at"`
}

type signupIntentPollResp struct {
	Status   string `json:"status"`
	APIToken string `json:"api_token,omitempty"`
}

type shareResp struct {
	UUID            string `json:"uuid"`
	ShortID         string `json:"short_id"`
	Filename        string `json:"filename"`
	Path            string `json:"path,omitempty"`
	Watch           bool   `json:"watch"`
	URL             string `json:"url"`
	CreatedAt       string `json:"created_at"`
	UpdatedAt       string `json:"updated_at"`
	SizeBytes       int    `json:"size_bytes"`
	UnresolvedCount int    `json:"unresolved_count"`
}

type commentView struct {
	UUID       string `json:"uuid"`
	AuthorKind string `json:"author_kind"`
	AuthorName string `json:"author_name"`
	Body       string `json:"body"`
	CreatedAt  string `json:"created_at"`
}

type threadView struct {
	UUID           string        `json:"uuid"`
	Anchor         string        `json:"anchor"`
	CurrentAnchor  string        `json:"current_anchor"`
	AnchorType     string        `json:"anchor_type"`
	Quote          string        `json:"quote"`
	Orphaned       bool          `json:"orphaned"`
	Resolved       bool          `json:"resolved"`
	CreatedVersion int           `json:"created_version"`
	Comments       []commentView `json:"comments"`
}

type threadsResp struct {
	Threads []threadView `json:"threads"`
}

func (c *apiClient) do(method, path string, body, dst any) error {
	status, err := c.doStatus(method, path, body, dst)
	if err != nil {
		return err
	}
	_ = status
	return nil
}

func (c *apiClient) doStatus(method, path string, body, dst any) (int, error) {
	var rdr io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return 0, fmt.Errorf("marshal: %w", err)
		}
		rdr = bytes.NewReader(buf)
	}
	req, err := http.NewRequest(method, c.base+path, rdr)
	if err != nil {
		return 0, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, fmt.Errorf("request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return resp.StatusCode, fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	if dst == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return resp.StatusCode, nil
	}
	if err := json.NewDecoder(resp.Body).Decode(dst); err != nil {
		return resp.StatusCode, fmt.Errorf("decode: %w", err)
	}
	return resp.StatusCode, nil
}

func (c *apiClient) Signup(email string) (*signupIntentResp, error) {
	var out signupIntentResp
	if err := c.do("POST", "/api/signup/intent", map[string]string{"email": email}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *apiClient) PollSignupIntent(id string) (*signupIntentPollResp, error) {
	var out signupIntentPollResp
	if err := c.do("GET", "/api/signup/intent/"+id, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

type manageIntentResp struct {
	IntentID     string `json:"intent_id"`
	DashboardURL string `json:"dashboard_url"`
	ExpiresAt    string `json:"expires_at"`
}

func (c *apiClient) OpenManageIntent() (*manageIntentResp, error) {
	var out manageIntentResp
	if err := c.do("POST", "/api/manage/intent", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *apiClient) CreateShare(filename, path, content string, watch bool) (*shareResp, bool, error) {
	var out shareResp
	status, err := c.doStatus("POST", "/api/shares", map[string]any{
		"filename": filename,
		"path":     path,
		"content":  content,
		"watch":    watch,
	}, &out)
	if err != nil {
		return nil, false, err
	}
	return &out, status == http.StatusCreated, nil
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

func (c *apiClient) ValidateToken() error {
	return c.do("GET", "/api/shares", nil, nil)
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

func (c *apiClient) ListComments(shareUUID string, unresolved bool) ([]threadView, error) {
	path := fmt.Sprintf("/api/shares/%s/comments", shareUUID)
	if unresolved {
		path += "?unresolved=1"
	}
	var out threadsResp
	if err := c.do("GET", path, nil, &out); err != nil {
		return nil, err
	}
	return out.Threads, nil
}

func (c *apiClient) ReplyComment(shareUUID, threadUUID, body string) (*threadView, error) {
	var out threadView
	path := fmt.Sprintf("/api/shares/%s/comments/%s/replies", shareUUID, threadUUID)
	if err := c.do("POST", path, map[string]string{"body": body}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *apiClient) ResolveThread(shareUUID, threadUUID string) error {
	return c.do("POST", fmt.Sprintf("/api/shares/%s/comments/%s/resolve", shareUUID, threadUUID), nil, nil)
}

func (c *apiClient) UnresolveThread(shareUUID, threadUUID string) error {
	return c.do("POST", fmt.Sprintf("/api/shares/%s/comments/%s/unresolve", shareUUID, threadUUID), nil, nil)
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
