package chatgpt

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"time"
)

const (
	ActionNext                = "next"
	ModelTextDavinci002Render = "text-davinci-002-render"
	RoleUser                  = "user"
	ContentTypeText           = "text"

	defaultUserAgent   = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/108.0.0.0 Safari/537.36"
	apiAddr            = "https://chat.openai.com/api"
	backendAPIAddr     = "https://chat.openai.com/backend-api"
	cookieSessionToken = "__Secure-next-auth.session-token"

	conversationEOF = "[DONE]"
	sseDataPrefix   = "data: "
)

type Client struct {
	httpClient *http.Client
	// TODO(selman): dump request/response to a custom output.
	apiAddr        string
	backendAPIAddr string
	userAgent      string
	autoRefresh    bool
	sessionToken   string
	accessToken    accessToken
	debug          bool
}

type ClientOption func(*Client)

func WithHTTPClient(httpClient *http.Client) ClientOption {
	return func(c *Client) {
		c.httpClient = httpClient
	}
}

func WithUserAgent(userAgent string) ClientOption {
	return func(c *Client) {
		c.userAgent = userAgent
	}
}

func WithDebug() ClientOption {
	return func(c *Client) {
		c.debug = true
	}
}

func WithAutoRefresh() ClientOption {
	return func(c *Client) {
		c.autoRefresh = true
	}
}

func WithAddr(addr string) ClientOption {
	return func(c *Client) {
		c.apiAddr = addr
		c.backendAPIAddr = addr
	}
}

func NewClient(sessionToken string, options ...ClientOption) *Client {
	c := &Client{
		httpClient:     http.DefaultClient,
		apiAddr:        apiAddr,
		backendAPIAddr: backendAPIAddr,
		userAgent:      defaultUserAgent,
		autoRefresh:    true,
		sessionToken:   sessionToken,
	}

	for _, option := range options {
		option(c)
	}

	return c
}

func (c *Client) Auth(ctx context.Context) error {
	return c.auth(ctx)
}

func (c *Client) Conversation(
	ctx context.Context,
	cq ConversationRequest,
	handler ConversationStreamHandler,
) error {
	return c.doConversation(ctx, cq, handler)
}

func (c *Client) doConversation(
	ctx context.Context,
	cq ConversationRequest,
	handler ConversationStreamHandler,
) error {
	if c.autoRefresh {
		err := c.authIfExpired(ctx)
		if err != nil {
			return err
		}
	}

	var buf bytes.Buffer
	err := json.NewEncoder(&buf).Encode(cq)
	if err != nil {
		return err
	}

	url, _ := url.JoinPath(c.backendAPIAddr, "conversation")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, &buf)
	if err != nil {
		return err
	}

	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", c.accessToken.value))
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// TODO(selman): check body for error message.
		return fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Bytes()

		if len(line) == 0 {
			continue
		}

		if bytes.HasPrefix(line, []byte(sseDataPrefix)) {
			data := bytes.TrimPrefix(line, []byte(sseDataPrefix))

			if bytes.Equal(data, []byte(conversationEOF)) {
				return nil
			}

			var cr ConversationResponse
			err := json.Unmarshal(data, &cr)

			handler(cr, err)
		} else {
			// TODO(selman): unknown
		}

	}

	if err := scanner.Err(); err != nil {
		return err
	}

	return nil
}

func (c *Client) auth(ctx context.Context) error {
	resp, err := c.doAuthSession(ctx)
	if err != nil {
		return err
	}

	expires, err := time.Parse(time.RFC3339, resp.Expires)
	if err != nil {
		return err
	}

	c.accessToken = accessToken{
		value:   resp.AccessToken,
		expires: expires,
	}

	return nil
}

func (c *Client) authIfExpired(ctx context.Context) error {
	if c.accessToken.value == "" || c.accessToken.isExpired() {
		return c.auth(ctx)
	}

	return nil
}

func (c *Client) doAuthSession(ctx context.Context) (*AuthSessionResponse, error) {
	/// :fingers-crossed:
	url, _ := url.JoinPath(c.apiAddr, "auth", "session")

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("User-Agent", c.userAgent)
	req.AddCookie(&http.Cookie{
		Name:  cookieSessionToken,
		Value: c.sessionToken,
	})

	resp, err := c.do(req)
	if err != nil {
		return nil, err
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// TODO(selman): check body for error message.
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	var authResponse AuthSessionResponse
	// TODO(selman): reuse decoder?
	err = json.NewDecoder(resp.Body).Decode(&authResponse)
	if err != nil {
		return nil, err
	}

	return &authResponse, nil
}

func (c *Client) do(req *http.Request) (*http.Response, error) {
	if c.debug {
		dump, _ := httputil.DumpRequestOut(req, true)
		fmt.Fprintf(os.Stderr, "%s", dump)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}

	if c.debug {
		dump, _ := httputil.DumpResponse(resp, true)
		fmt.Fprintf(os.Stderr, "%s", dump)
	}

	return resp, nil
}

type AuthSessionResponse struct {
	Expires     string `json:"expires"`
	AccessToken string `json:"accessToken"`
}

type ConversationRequest struct {
	Action          string    `json:"action,omitempty"`
	Messages        []Message `json:"messages,omitempty"`
	ConversationID  string    `json:"conversation_id,omitempty"`
	ParentMessageID string    `json:"parent_message_id,omitempty"`
	Model           string    `json:"model,omitempty"`
}

type Message struct {
	ID      string  `json:"id,omitempty"`
	Role    string  `json:"role,omitempty"`
	Content Content `json:"content,omitempty"`
}

type Content struct {
	ContentType string   `json:"content_type,omitempty"`
	Parts       []string `json:"parts,omitempty"`
}

type ConversationResponse struct {
	Message        Message `json:"message"`
	ConversationID string  `json:"conversation_id"`
	Error          string  `json:"error"`
}

type ConversationStreamHandler func(ConversationResponse, error)

type accessToken struct {
	value   string
	expires time.Time
}

func (a *accessToken) isExpired() bool {
	return time.Now().After(a.expires)
}
