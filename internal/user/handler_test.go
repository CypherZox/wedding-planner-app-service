package user

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/CypherZox/wedding-planner/backend-service/db"
	"github.com/CypherZox/wedding-planner/backend-service/internal/platform/auth"
	"github.com/CypherZox/wedding-planner/backend-service/internal/platform/clock"
	"github.com/CypherZox/wedding-planner/backend-service/internal/platform/mongotest"
	"github.com/CypherZox/wedding-planner/backend-service/internal/platform/mongox"
)

func TestAccountOnboardAndOffboard(t *testing.T) {
	mux := newMux(t, fakeTokens{
		"ada": {Subject: "ada-id", Email: "Ada@Example.com", EmailVerified: true},
		"bea": {Subject: "bea-id", Email: "ada@example.com", EmailVerified: true},
	})
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)

	rec := call(mux, http.MethodGet, "/v1/me", "ada", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET before onboard status %d body %s", rec.Code, rec.Body.String())
	}

	rec = call(mux, http.MethodPost, "/v1/me", "ada", `{"displayName":" Ada Lovelace ","locale":"en-US"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST status %d body %s", rec.Code, rec.Body.String())
	}
	created := decodeUser(t, rec)
	if created.Email != "ada@example.com" || created.DisplayName != "Ada Lovelace" || created.Locale != "en-US" || created.AuthProvider != "supabase" {
		t.Fatalf("created = %+v", created)
	}
	if !created.EmailVerified || created.LastSeenAt != now || created.CreatedAt != now {
		t.Fatalf("created times = %+v", created)
	}

	rec = call(mux, http.MethodPost, "/v1/me", "ada", `{"displayName":"Someone Else","locale":"fr"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("second POST status %d body %s", rec.Code, rec.Body.String())
	}
	again := decodeUser(t, rec)
	if again.ID != created.ID || again.DisplayName != "Ada Lovelace" {
		t.Fatalf("second onboard = %+v", again)
	}

	rec = call(mux, http.MethodGet, "/v1/me", "ada", "")
	if rec.Code != http.StatusOK || decodeUser(t, rec).ID != created.ID {
		t.Fatalf("GET status %d body %s", rec.Code, rec.Body.String())
	}

	rec = call(mux, http.MethodPatch, "/v1/me", "ada", `{"locale":"fr"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH status %d body %s", rec.Code, rec.Body.String())
	}
	patched := decodeUser(t, rec)
	if patched.Locale != "fr" || patched.DisplayName != "Ada Lovelace" {
		t.Fatalf("patched = %+v", patched)
	}

	rec = call(mux, http.MethodPost, "/v1/me", "bea", `{"displayName":"Bea","locale":"en"}`)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "duplicate") {
		t.Fatalf("duplicate email status %d body %s", rec.Code, rec.Body.String())
	}

	rec = call(mux, http.MethodDelete, "/v1/me", "ada", "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("DELETE status %d body %s", rec.Code, rec.Body.String())
	}
	rec = call(mux, http.MethodGet, "/v1/me", "ada", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET after offboard status %d body %s", rec.Code, rec.Body.String())
	}
	rec = call(mux, http.MethodPatch, "/v1/me", "ada", `{"displayName":"Nope"}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("PATCH after offboard status %d", rec.Code)
	}

	rec = call(mux, http.MethodPost, "/v1/me", "ada", `{"displayName":"Ada Again","locale":"en"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("restore status %d body %s", rec.Code, rec.Body.String())
	}
	restored := decodeUser(t, rec)
	if restored.ID != created.ID || restored.DisplayName != "Ada Again" || restored.Locale != "en" {
		t.Fatalf("restored = %+v", restored)
	}
}

func TestAccountRejectsBadRequests(t *testing.T) {
	mux := newMux(t, fakeTokens{
		"ada": {Subject: "ada-id", Email: "ada@example.com", EmailVerified: false},
	})

	rec := call(mux, http.MethodPost, "/v1/me", "", `{"displayName":"Ada","locale":"en"}`)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("missing token status %d", rec.Code)
	}
	rec = call(mux, http.MethodPost, "/v1/me", "nope", `{"displayName":"Ada","locale":"en"}`)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("bad token status %d", rec.Code)
	}
	rec = call(mux, http.MethodPost, "/v1/me", "ada", `{"displayName":"Ada","locale":"not a locale"}`)
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "validation_failed") {
		t.Fatalf("bad locale status %d body %s", rec.Code, rec.Body.String())
	}
	rec = call(mux, http.MethodPost, "/v1/me", "ada", `{"displayName":"Ada","locale":"en"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("onboard status %d body %s", rec.Code, rec.Body.String())
	}
	rec = call(mux, http.MethodPatch, "/v1/me", "ada", `{}`)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("empty patch status %d body %s", rec.Code, rec.Body.String())
	}
	rec = call(mux, http.MethodPatch, "/v1/me", "ada", `{"email":"other@example.com"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown field status %d body %s", rec.Code, rec.Body.String())
	}
}

type fakeTokens map[string]auth.Claims

func (f fakeTokens) Verify(_ context.Context, token string) (auth.Claims, error) {
	claims, ok := f[token]
	if !ok {
		return auth.Claims{}, context.Canceled
	}
	return claims, nil
}

type userJSON struct {
	ID            string    `json:"id"`
	AuthProvider  string    `json:"authProvider"`
	Email         string    `json:"email"`
	EmailVerified bool      `json:"emailVerified"`
	DisplayName   string    `json:"displayName"`
	Locale        string    `json:"locale"`
	LastSeenAt    time.Time `json:"lastSeenAt"`
	CreatedAt     time.Time `json:"createdAt"`
}

func newMux(t *testing.T, tokens fakeTokens) *http.ServeMux {
	t.Helper()
	client := connect(t)
	database := mongotest.Database(t, client)
	if err := db.Apply(context.Background(), database); err != nil {
		t.Fatalf("apply: %v", err)
	}
	mux := http.NewServeMux()
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	NewHandler(NewService(NewRepo(database), clock.Fixed(now))).Register(mux, tokens)
	return mux
}

func connect(t *testing.T) *mongo.Client {
	t.Helper()
	client, err := mongox.Connect(mongotest.URI())
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = client.Disconnect(context.Background()) })
	return client
}

func call(mux *http.ServeMux, method, path, token, body string) *httptest.ResponseRecorder {
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func decodeUser(t *testing.T, rec *httptest.ResponseRecorder) userJSON {
	t.Helper()
	var user userJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &user); err != nil {
		t.Fatalf("decode %s: %v", rec.Body.String(), err)
	}
	return user
}
