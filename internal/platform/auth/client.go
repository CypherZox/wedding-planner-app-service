package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// Session is a Supabase Auth session.
type Session struct {
	AccessToken  string   `json:"access_token"`
	RefreshToken string   `json:"refresh_token"`
	TokenType    string   `json:"token_type"`
	ExpiresIn    int      `json:"expires_in"`
	ExpiresAt    int64    `json:"expires_at"`
	User         AuthUser `json:"user"`
}

// AuthUser is the Supabase Auth user returned with a session.
type AuthUser struct {
	ID           string         `json:"id"`
	Email        string         `json:"email"`
	UserMetadata map[string]any `json:"user_metadata"`
}

// Error is a non-2xx response from Supabase Auth.
type Error struct {
	Status  int
	Code    string
	Message string
	Reasons []string
}

func (e *Error) Error() string {
	return fmt.Sprintf("supabase auth %d %s: %s", e.Status, e.Code, e.Message)
}

// Client calls Supabase Auth for email and password accounts.
// Requests use the secret key so Auth can rate-limit by the forwarded end-user IP.
type Client struct {
	baseURL   string
	secretKey string
	http      *http.Client
}

// NewClient builds a client for one project. A nil httpClient uses a 10 second timeout.
func NewClient(supabaseURL, secretKey string, httpClient *http.Client) (*Client, error) {
	base, err := issuerURL(supabaseURL)
	if err != nil {
		return nil, err
	}
	if secretKey == "" {
		return nil, fmt.Errorf("supabase secret key is required")
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}
	return &Client{baseURL: base, secretKey: secretKey, http: httpClient}, nil
}

// SignUp creates an email and password user. It returns a nil session when
// the project requires email confirmation before the first sign-in.
func (c *Client) SignUp(ctx context.Context, clientIP, email, password string, metadata map[string]any) (*Session, error) {
	body := map[string]any{"email": email, "password": password, "data": metadata}
	var out Session
	if err := c.do(ctx, "/signup", nil, clientIP, "", body, &out); err != nil {
		return nil, err
	}
	if out.AccessToken == "" {
		return nil, nil
	}
	return &out, nil
}

// SignIn exchanges an email and password for a session.
func (c *Client) SignIn(ctx context.Context, clientIP, email, password string) (Session, error) {
	var out Session
	query := url.Values{"grant_type": {"password"}}
	err := c.do(ctx, "/token", query, clientIP, "", map[string]string{"email": email, "password": password}, &out)
	return out, err
}

// Refresh exchanges a refresh token for a new session. Each refresh token works once.
func (c *Client) Refresh(ctx context.Context, clientIP, refreshToken string) (Session, error) {
	var out Session
	query := url.Values{"grant_type": {"refresh_token"}}
	err := c.do(ctx, "/token", query, clientIP, "", map[string]string{"refresh_token": refreshToken}, &out)
	return out, err
}

// SignOut revokes refresh tokens for the session. scope is local, global, or others.
func (c *Client) SignOut(ctx context.Context, clientIP, accessToken, scope string) error {
	return c.do(ctx, "/logout", url.Values{"scope": {scope}}, clientIP, accessToken, nil, nil)
}

func (c *Client) do(ctx context.Context, path string, query url.Values, clientIP, accessToken string, body, out any) error {
	endpoint := c.baseURL + path
	if len(query) > 0 {
		endpoint += "?" + query.Encode()
	}
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode supabase request: %w", err)
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, reader)
	if err != nil {
		return fmt.Errorf("build supabase request: %w", err)
	}
	req.Header.Set("apikey", c.secretKey)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if accessToken != "" {
		req.Header.Set("Authorization", "Bearer "+accessToken)
	}
	if clientIP != "" {
		req.Header.Set("Sb-Forwarded-For", clientIP)
	}

	res, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("call supabase auth: %w", err)
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, maxBodyBytes))
	if err != nil {
		return fmt.Errorf("read supabase auth response: %w", err)
	}
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return parseError(res.StatusCode, raw)
	}
	if out == nil || len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("decode supabase auth response: %w", err)
	}
	return nil
}

func parseError(status int, raw []byte) error {
	var body struct {
		ErrorCode        string `json:"error_code"`
		Msg              string `json:"msg"`
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
		WeakPassword     struct {
			Reasons []string `json:"reasons"`
		} `json:"weak_password"`
	}
	_ = json.Unmarshal(raw, &body)
	out := &Error{Status: status, Code: body.ErrorCode, Message: body.Msg, Reasons: body.WeakPassword.Reasons}
	if out.Code == "" {
		out.Code = body.Error
	}
	if out.Message == "" {
		out.Message = body.ErrorDescription
	}
	return out
}
