package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

const (
	jwksCacheTTL = 10 * time.Minute
	maxBodyBytes = 1 << 20
)

// Claims are the identity fields taken from a verified Supabase access token.
type Claims struct {
	Subject       string
	Email         string
	EmailVerified bool
	Role          string
}

// Verifier checks Supabase Auth and verifies access tokens with the project JWKS.
type Verifier struct {
	issuer         string
	publishableKey string
	jwksURL        string
	healthURL      string
	client         *http.Client
	cacheTTL       time.Duration

	mu        sync.Mutex
	keys      jose.JSONWebKeySet
	fetchedAt time.Time
}

// Option configures a Verifier.
type Option func(*Verifier)

// WithHTTPClient replaces the HTTP client. Tests use this to trust a local server.
func WithHTTPClient(client *http.Client) Option {
	return func(v *Verifier) {
		if client != nil {
			v.client = client
		}
	}
}

// New builds a verifier for one Supabase project.
// supabaseURL is the project URL, publishableKey is sent as the apikey header,
// and jwksURL is the project's public signing-key endpoint.
func New(supabaseURL, publishableKey, jwksURL string, opts ...Option) (*Verifier, error) {
	issuer, err := issuerURL(supabaseURL)
	if err != nil {
		return nil, err
	}
	if publishableKey == "" {
		return nil, fmt.Errorf("supabase publishable key is required")
	}
	parsedJWKS, err := parseHTTPS(jwksURL)
	if err != nil {
		return nil, fmt.Errorf("parse supabase jwks url: %w", err)
	}
	project, err := parseHTTPS(supabaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse supabase url: %w", err)
	}
	if parsedJWKS.Host != project.Host {
		return nil, fmt.Errorf("supabase jwks host %s does not match project host %s", parsedJWKS.Host, project.Host)
	}

	v := &Verifier{
		issuer:         issuer,
		publishableKey: publishableKey,
		jwksURL:        parsedJWKS.String(),
		healthURL:      issuer + "/health",
		client:         &http.Client{Timeout: 5 * time.Second},
		cacheTTL:       jwksCacheTTL,
	}
	for _, opt := range opts {
		opt(v)
	}
	return v, nil
}

// Connect checks Auth with the publishable key and loads the signing keys.
func (v *Verifier) Connect(ctx context.Context) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if err := v.refresh(ctx); err != nil {
		return fmt.Errorf("connect supabase: %w", err)
	}
	return nil
}

// Ping reports whether Auth and the signing keys are still usable.
// It reloads the JWKS once the 10 minute cache has expired.
func (v *Verifier) Ping(ctx context.Context) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if err := v.ensure(ctx, ""); err != nil {
		return fmt.Errorf("ping supabase: %w", err)
	}
	return nil
}

// Verify checks a Supabase access token and returns its identity claims.
func (v *Verifier) Verify(ctx context.Context, token string) (Claims, error) {
	parsed, err := jwt.ParseSigned(token, []jose.SignatureAlgorithm{jose.ES256, jose.RS256, jose.EdDSA})
	if err != nil {
		return Claims{}, fmt.Errorf("parse supabase token: %w", err)
	}
	if len(parsed.Headers) != 1 || parsed.Headers[0].KeyID == "" {
		return Claims{}, fmt.Errorf("supabase token is missing a signing key id")
	}

	key, err := v.key(ctx, parsed.Headers[0].KeyID)
	if err != nil {
		return Claims{}, err
	}

	var registered jwt.Claims
	var extra struct {
		Email         string `json:"email"`
		EmailVerified bool   `json:"email_verified"`
		Role          string `json:"role"`
	}
	if err := parsed.Claims(key.Key, &registered, &extra); err != nil {
		return Claims{}, fmt.Errorf("verify supabase token: %w", err)
	}
	if err := registered.Validate(jwt.Expected{
		Issuer:      v.issuer,
		AnyAudience: jwt.Audience{"authenticated"},
		Time:        time.Now(),
	}); err != nil {
		return Claims{}, fmt.Errorf("validate supabase token: %w", err)
	}
	if registered.Subject == "" {
		return Claims{}, fmt.Errorf("supabase token is missing sub")
	}
	if extra.Role != "authenticated" {
		return Claims{}, fmt.Errorf("supabase token role is %q", extra.Role)
	}
	return Claims{
		Subject:       registered.Subject,
		Email:         extra.Email,
		EmailVerified: extra.EmailVerified,
		Role:          extra.Role,
	}, nil
}

func (v *Verifier) key(ctx context.Context, kid string) (jose.JSONWebKey, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if err := v.ensure(ctx, kid); err != nil {
		return jose.JSONWebKey{}, err
	}
	matches := v.keys.Key(kid)
	if len(matches) == 0 {
		return jose.JSONWebKey{}, fmt.Errorf("supabase signing key %q was not found", kid)
	}
	return matches[0], nil
}

func (v *Verifier) ensure(ctx context.Context, kid string) error {
	fresh := !v.fetchedAt.IsZero() && time.Since(v.fetchedAt) < v.cacheTTL
	if fresh && (kid == "" || len(v.keys.Key(kid)) > 0) {
		return nil
	}
	return v.refresh(ctx)
}

func (v *Verifier) refresh(ctx context.Context) error {
	if err := v.checkHealth(ctx); err != nil {
		return err
	}
	body, err := v.get(ctx, v.jwksURL)
	if err != nil {
		return err
	}
	var set jose.JSONWebKeySet
	if err := json.Unmarshal(body, &set); err != nil {
		return fmt.Errorf("decode supabase jwks: %w", err)
	}
	if len(set.Keys) == 0 {
		return fmt.Errorf("supabase jwks has no asymmetric signing keys")
	}
	for _, key := range set.Keys {
		if key.KeyID == "" || !key.Valid() {
			return fmt.Errorf("supabase jwks contains an unusable signing key")
		}
	}
	v.keys = set
	v.fetchedAt = time.Now()
	return nil
}

func (v *Verifier) checkHealth(ctx context.Context) error {
	body, err := v.get(ctx, v.healthURL)
	if err != nil {
		return err
	}
	var health map[string]any
	if err := json.Unmarshal(body, &health); err != nil {
		return fmt.Errorf("decode supabase auth health: %w", err)
	}
	if health["name"] == "" || health["name"] == nil {
		return fmt.Errorf("supabase auth health response is missing a name")
	}
	return nil
}

func (v *Verifier) get(ctx context.Context, endpoint string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("build supabase request: %w", err)
	}
	req.Header.Set("apikey", v.publishableKey)
	req.Header.Set("Accept", "application/json")

	res, err := v.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("call supabase: %w", err)
	}
	defer res.Body.Close()

	body, err := io.ReadAll(io.LimitReader(res.Body, maxBodyBytes))
	if err != nil {
		return nil, fmt.Errorf("read supabase response: %w", err)
	}
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("supabase %s returned status %d", req.URL.Path, res.StatusCode)
	}
	return body, nil
}

func issuerURL(raw string) (string, error) {
	parsed, err := parseHTTPS(raw)
	if err != nil {
		return "", fmt.Errorf("parse supabase url: %w", err)
	}
	return strings.TrimRight(parsed.String(), "/") + "/auth/v1", nil
}

func parseHTTPS(raw string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, err
	}
	if parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
		return nil, fmt.Errorf("url must be https with a host")
	}
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed, nil
}
