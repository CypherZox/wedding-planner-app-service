package auth

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

func TestVerifierRejectsMismatchedHost(t *testing.T) {
	_, err := New(
		"https://project.supabase.co",
		"publishable",
		"https://other.supabase.co/auth/v1/.well-known/jwks.json",
	)
	if err == nil {
		t.Fatal("New() succeeded for a JWKS host that does not match the project")
	}
}

func TestVerifierChecksToken(t *testing.T) {
	priv := mustKey(t)
	other := mustKey(t)
	kid := "kid-1"
	var current jose.JSONWebKeySet
	current.Keys = []jose.JSONWebKey{publicJWK(priv, kid)}

	var hits int
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("apikey") != "publishable" {
			http.Error(w, "missing apikey", http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/auth/v1/health":
			writeJSON(t, w, map[string]string{"name": "GoTrue"})
		case "/.well-known/jwks.json":
			hits++
			writeJSON(t, w, current)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	v, err := New(srv.URL, "publishable", srv.URL+"/.well-known/jwks.json", WithHTTPClient(srv.Client()))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	ctx := context.Background()
	if err := v.Connect(ctx); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	if hits != 1 {
		t.Fatalf("jwks hits = %d", hits)
	}
	if err := v.Ping(ctx); err != nil {
		t.Fatalf("Ping() error = %v", err)
	}
	if hits != 1 {
		t.Fatalf("Ping() refetched jwks, hits = %d", hits)
	}

	claims, err := v.Verify(ctx, signToken(t, priv, kid, srv.URL+"/auth/v1", "user-1", time.Now().Add(time.Hour)))
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if claims.Subject != "user-1" || claims.Email != "a@b.co" || !claims.EmailVerified || claims.Role != "authenticated" {
		t.Fatalf("Verify() = %+v", claims)
	}
	if hits != 1 {
		t.Fatalf("Verify() refetched a known key, hits = %d", hits)
	}

	if _, err := v.Verify(ctx, signToken(t, other, kid, srv.URL+"/auth/v1", "user-1", time.Now().Add(time.Hour))); err == nil {
		t.Fatal("Verify() accepted a token signed by the wrong key")
	}
	if _, err := v.Verify(ctx, signToken(t, priv, kid, "https://other.example/auth/v1", "user-1", time.Now().Add(time.Hour))); err == nil {
		t.Fatal("Verify() accepted a token from another issuer")
	}
	if _, err := v.Verify(ctx, signToken(t, priv, kid, srv.URL+"/auth/v1", "user-1", time.Now().Add(-time.Hour))); err == nil {
		t.Fatal("Verify() accepted an expired token")
	}

	current.Keys = []jose.JSONWebKey{publicJWK(priv, "kid-2")}
	claims, err = v.Verify(ctx, signToken(t, priv, "kid-2", srv.URL+"/auth/v1", "user-2", time.Now().Add(time.Hour)))
	if err != nil {
		t.Fatalf("Verify() after key rotation error = %v", err)
	}
	if claims.Subject != "user-2" {
		t.Fatalf("Verify() subject = %s", claims.Subject)
	}
	if hits != 2 {
		t.Fatalf("jwks hits after unknown kid = %d", hits)
	}
}

func TestConnectLive(t *testing.T) {
	projectURL := os.Getenv("SUPABASE_URL")
	if projectURL == "" {
		t.Skip("SUPABASE_URL not set")
	}
	v, err := New(projectURL, os.Getenv("SUPABASE_PUBLISHABLE_KEY"), os.Getenv("SUPABASE_JWKS_URL"))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := v.Connect(ctx); err != nil {
		t.Fatal(err)
	}
}

func mustKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func publicJWK(key *ecdsa.PrivateKey, kid string) jose.JSONWebKey {
	return jose.JSONWebKey{
		Key:       &key.PublicKey,
		KeyID:     kid,
		Algorithm: string(jose.ES256),
		Use:       "sig",
	}
}

func signToken(t *testing.T, key *ecdsa.PrivateKey, kid, issuer, subject string, expiry time.Time) string {
	t.Helper()
	signer, err := jose.NewSigner(
		jose.SigningKey{Algorithm: jose.ES256, Key: key},
		(&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", kid),
	)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := jwt.Signed(signer).Claims(jwt.Claims{
		Issuer:   issuer,
		Subject:  subject,
		Audience: jwt.Audience{"authenticated"},
		Expiry:   jwt.NewNumericDate(expiry),
		IssuedAt: jwt.NewNumericDate(time.Now().Add(-time.Minute)),
	}).Claims(map[string]any{
		"email":          "a@b.co",
		"email_verified": true,
		"role":           "authenticated",
	}).Serialize()
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func writeJSON(t *testing.T, w http.ResponseWriter, body any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(body); err != nil {
		t.Fatal(err)
	}
}
