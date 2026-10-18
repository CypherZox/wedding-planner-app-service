package session

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/CypherZox/wedding-planner/backend-service/db"
	"github.com/CypherZox/wedding-planner/backend-service/internal/platform/auth"
	"github.com/CypherZox/wedding-planner/backend-service/internal/platform/clock"
	"github.com/CypherZox/wedding-planner/backend-service/internal/platform/mongotest"
	"github.com/CypherZox/wedding-planner/backend-service/internal/platform/mongox"
	"github.com/CypherZox/wedding-planner/backend-service/internal/user"
)

const secretKey = "sb_secret_test"

func TestRegisterLoginRefreshLogout(t *testing.T) {
	env := newEnv(t, false)

	rec := env.call(http.MethodPost, "/v1/auth/register", "", `{"email":" Ada@Example.com ","password":"correct-horse","displayName":"Ada","locale":"en-US"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("register status %d body %s", rec.Code, rec.Body.String())
	}
	registered := decode[authJSON](t, rec)
	if registered.Session.AccessToken == "" || registered.Session.RefreshToken == "" || registered.User == nil {
		t.Fatalf("register body %s", rec.Body.String())
	}
	if registered.User.Email != "ada@example.com" || registered.User.DisplayName != "Ada" || registered.User.Locale != "en-US" {
		t.Fatalf("registered user = %+v", registered.User)
	}
	signup := env.supabase.lastRequest("/auth/v1/signup")
	if signup.apikey != secretKey || signup.forwardedFor != "192.0.2.1" {
		t.Fatalf("signup headers = %+v", signup)
	}
	if signup.body["email"] != "ada@example.com" {
		t.Fatalf("signup email = %v", signup.body["email"])
	}
	if data, _ := signup.body["data"].(map[string]any); data["display_name"] != "Ada" || data["locale"] != "en-US" {
		t.Fatalf("signup metadata = %v", signup.body["data"])
	}

	rec = env.call(http.MethodPost, "/v1/auth/login", "", `{"email":"ada@example.com","password":"wrong"}`)
	if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), "unauthenticated") {
		t.Fatalf("wrong password status %d body %s", rec.Code, rec.Body.String())
	}

	rec = env.call(http.MethodPost, "/v1/auth/login", "", `{"email":"ada@example.com","password":"correct-horse"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("login status %d body %s", rec.Code, rec.Body.String())
	}
	loggedIn := decode[authJSON](t, rec)
	if loggedIn.User == nil || loggedIn.User.ID != registered.User.ID {
		t.Fatalf("login user = %+v, want id %s", loggedIn.User, registered.User.ID)
	}

	rec = env.call(http.MethodPost, "/v1/auth/refresh", "", `{"refreshToken":"`+loggedIn.Session.RefreshToken+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("refresh status %d body %s", rec.Code, rec.Body.String())
	}
	refreshed := decode[sessionJSON](t, rec)
	if refreshed.AccessToken == "" || refreshed.RefreshToken == loggedIn.Session.RefreshToken {
		t.Fatalf("refreshed = %+v", refreshed)
	}
	rec = env.call(http.MethodPost, "/v1/auth/refresh", "", `{"refreshToken":"`+loggedIn.Session.RefreshToken+`"}`)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("reused refresh token status %d body %s", rec.Code, rec.Body.String())
	}

	rec = env.call(http.MethodPost, "/v1/auth/logout", "", "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("logout without token status %d", rec.Code)
	}
	rec = env.call(http.MethodPost, "/v1/auth/logout?scope=everyone", refreshed.AccessToken, "")
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("logout bad scope status %d body %s", rec.Code, rec.Body.String())
	}
	rec = env.call(http.MethodPost, "/v1/auth/logout?scope=global", refreshed.AccessToken, "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("logout status %d body %s", rec.Code, rec.Body.String())
	}
	logout := env.supabase.lastRequest("/auth/v1/logout")
	if logout.authorization != "Bearer "+refreshed.AccessToken || logout.scope != "global" || logout.apikey != secretKey {
		t.Fatalf("logout request = %+v", logout)
	}
	rec = env.call(http.MethodPost, "/v1/auth/refresh", "", `{"refreshToken":"`+refreshed.RefreshToken+`"}`)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("refresh after logout status %d body %s", rec.Code, rec.Body.String())
	}
}

func TestRegisterWithEmailConfirmation(t *testing.T) {
	env := newEnv(t, true)

	rec := env.call(http.MethodPost, "/v1/auth/register", "", `{"email":"bea@example.com","password":"correct-horse","displayName":"Bea","locale":"fr"}`)
	if rec.Code != http.StatusAccepted || !strings.Contains(rec.Body.String(), `"confirmationRequired":true`) {
		t.Fatalf("register status %d body %s", rec.Code, rec.Body.String())
	}
	if _, err := env.users.Current(context.Background(), "uid-bea@example.com"); !errors.Is(err, mongox.ErrNotFound) {
		t.Fatalf("user created before confirmation: %v", err)
	}

	rec = env.call(http.MethodPost, "/v1/auth/login", "", `{"email":"bea@example.com","password":"correct-horse"}`)
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "email is not confirmed") {
		t.Fatalf("unconfirmed login status %d body %s", rec.Code, rec.Body.String())
	}

	env.supabase.confirm("bea@example.com")
	rec = env.call(http.MethodPost, "/v1/auth/login", "", `{"email":"bea@example.com","password":"correct-horse"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("confirmed login status %d body %s", rec.Code, rec.Body.String())
	}
	body := decode[authJSON](t, rec)
	if body.User == nil || body.User.DisplayName != "Bea" || body.User.Locale != "fr" {
		t.Fatalf("user from metadata = %+v", body.User)
	}
}

func TestAuthErrors(t *testing.T) {
	env := newEnv(t, false)

	cases := []struct {
		name   string
		path   string
		body   string
		status int
		want   string
	}{
		{"bad email", "/v1/auth/register", `{"email":"nope","password":"correct-horse","displayName":"A","locale":"en"}`, http.StatusUnprocessableEntity, "email is invalid"},
		{"bad locale", "/v1/auth/register", `{"email":"a@example.com","password":"correct-horse","displayName":"A","locale":"not a locale"}`, http.StatusUnprocessableEntity, "locale"},
		{"long password", "/v1/auth/register", `{"email":"a@example.com","password":"` + strings.Repeat("x", 73) + `","displayName":"A","locale":"en"}`, http.StatusUnprocessableEntity, "72 bytes"},
		{"weak password", "/v1/auth/register", `{"email":"a@example.com","password":"short","displayName":"A","locale":"en"}`, http.StatusUnprocessableEntity, `"reasons":["length"]`},
		{"unknown field", "/v1/auth/login", `{"email":"a@example.com","password":"x","remember":true}`, http.StatusBadRequest, "bad_request"},
		{"missing refresh token", "/v1/auth/refresh", `{"refreshToken":""}`, http.StatusUnprocessableEntity, "refreshToken is required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := env.call(http.MethodPost, tc.path, "", tc.body)
			if rec.Code != tc.status || !strings.Contains(rec.Body.String(), tc.want) {
				t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
			}
		})
	}

	rec := env.call(http.MethodPost, "/v1/auth/register", "", `{"email":"dup@example.com","password":"correct-horse","displayName":"A","locale":"en"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("first register status %d body %s", rec.Code, rec.Body.String())
	}
	rec = env.call(http.MethodPost, "/v1/auth/register", "", `{"email":"dup@example.com","password":"correct-horse","displayName":"A","locale":"en"}`)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "duplicate") {
		t.Fatalf("duplicate register status %d body %s", rec.Code, rec.Body.String())
	}

	env.supabase.rateLimited = true
	rec = env.call(http.MethodPost, "/v1/auth/login", "", `{"email":"dup@example.com","password":"correct-horse"}`)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("supabase rate limit status %d body %s", rec.Code, rec.Body.String())
	}
}

func TestLoginAfterOffboardReturnsNoUser(t *testing.T) {
	env := newEnv(t, false)
	rec := env.call(http.MethodPost, "/v1/auth/register", "", `{"email":"cy@example.com","password":"correct-horse","displayName":"Cy","locale":"en"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("register status %d body %s", rec.Code, rec.Body.String())
	}
	if err := env.users.Offboard(context.Background(), "uid-cy@example.com"); err != nil {
		t.Fatalf("offboard: %v", err)
	}
	rec = env.call(http.MethodPost, "/v1/auth/login", "", `{"email":"cy@example.com","password":"correct-horse"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("login status %d body %s", rec.Code, rec.Body.String())
	}
	body := decode[authJSON](t, rec)
	if body.Session.AccessToken == "" || body.User != nil {
		t.Fatalf("login after offboard = %s", rec.Body.String())
	}
}

func TestLocalRateLimit(t *testing.T) {
	env := newEnv(t, false)
	var last int
	for i := 0; i < 11; i++ {
		last = env.call(http.MethodPost, "/v1/auth/login", "", `{"email":"x@example.com","password":"wrong"}`).Code
	}
	if last != http.StatusTooManyRequests {
		t.Fatalf("11th login status %d", last)
	}
}

type env struct {
	mux      *http.ServeMux
	supabase *fakeSupabase
	users    *user.Service
}

func newEnv(t *testing.T, requireConfirmation bool) *env {
	t.Helper()
	client, err := mongox.Connect(mongotest.URI())
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = client.Disconnect(context.Background()) })
	database := mongotest.Database(t, client)
	if err := db.Apply(context.Background(), database); err != nil {
		t.Fatalf("apply: %v", err)
	}

	supabase := newFakeSupabase(requireConfirmation)
	srv := httptest.NewTLSServer(supabase)
	t.Cleanup(srv.Close)
	authClient, err := auth.NewClient(srv.URL, secretKey, srv.Client())
	if err != nil {
		t.Fatalf("auth client: %v", err)
	}

	users := user.NewService(user.NewRepo(database), clock.Fixed(time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)))
	mux := http.NewServeMux()
	NewHandler(NewService(authClient, supabase, users)).Register(mux)
	return &env{mux: mux, supabase: supabase, users: users}
}

func (e *env) call(method, path, token, body string) *httptest.ResponseRecorder {
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, path, nil)
	} else {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	e.mux.ServeHTTP(rec, req)
	return rec
}

type authJSON struct {
	Session sessionJSON `json:"session"`
	User    *struct {
		ID          string `json:"id"`
		Email       string `json:"email"`
		DisplayName string `json:"displayName"`
		Locale      string `json:"locale"`
	} `json:"user"`
}

type sessionJSON struct {
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken"`
}

func decode[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var out T
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode %s: %v", rec.Body.String(), err)
	}
	return out
}

type recorded struct {
	apikey        string
	forwardedFor  string
	authorization string
	scope         string
	body          map[string]any
}

type fakeAccount struct {
	password  string
	confirmed bool
	metadata  map[string]any
}

// fakeSupabase imitates the Supabase Auth email and password endpoints and
// doubles as the token verifier, since it knows which access tokens it issued.
type fakeSupabase struct {
	requireConfirmation bool
	rateLimited         bool

	mu       sync.Mutex
	accounts map[string]*fakeAccount
	access   map[string]string
	refresh  map[string]string
	requests map[string]recorded
	seq      int
}

func newFakeSupabase(requireConfirmation bool) *fakeSupabase {
	return &fakeSupabase{
		requireConfirmation: requireConfirmation,
		accounts:            map[string]*fakeAccount{},
		access:              map[string]string{},
		refresh:             map[string]string{},
		requests:            map[string]recorded{},
	}
}

func (f *fakeSupabase) Verify(_ context.Context, token string) (auth.Claims, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	email, ok := f.access[token]
	if !ok {
		return auth.Claims{}, errors.New("unknown token")
	}
	return auth.Claims{Subject: "uid-" + email, Email: email, EmailVerified: true, Role: "authenticated"}, nil
}

func (f *fakeSupabase) confirm(email string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.accounts[email].confirmed = true
}

func (f *fakeSupabase) lastRequest(path string) recorded {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.requests[path]
}

func (f *fakeSupabase) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()

	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)
	f.requests[r.URL.Path] = recorded{
		apikey:        r.Header.Get("apikey"),
		forwardedFor:  r.Header.Get("Sb-Forwarded-For"),
		authorization: r.Header.Get("Authorization"),
		scope:         r.URL.Query().Get("scope"),
		body:          body,
	}
	if r.Header.Get("apikey") != secretKey {
		f.fail(w, http.StatusUnauthorized, "no_authorization", "missing apikey")
		return
	}
	if f.rateLimited {
		f.fail(w, http.StatusTooManyRequests, "over_request_rate_limit", "Request rate limit reached")
		return
	}

	switch {
	case r.URL.Path == "/auth/v1/signup":
		email, _ := body["email"].(string)
		password, _ := body["password"].(string)
		if len(password) < 8 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnprocessableEntity)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 422, "error_code": "weak_password", "msg": "Password should be at least 8 characters.",
				"weak_password": map[string]any{"reasons": []string{"length"}},
			})
			return
		}
		if _, exists := f.accounts[email]; exists {
			f.fail(w, http.StatusUnprocessableEntity, "user_already_exists", "User already registered")
			return
		}
		metadata, _ := body["data"].(map[string]any)
		f.accounts[email] = &fakeAccount{password: password, confirmed: !f.requireConfirmation, metadata: metadata}
		if f.requireConfirmation {
			writeJSON(w, map[string]any{"id": "uid-" + email, "email": email})
			return
		}
		writeJSON(w, f.issue(email))
	case r.URL.Path == "/auth/v1/token" && r.URL.Query().Get("grant_type") == "password":
		email, _ := body["email"].(string)
		password, _ := body["password"].(string)
		account, ok := f.accounts[email]
		if !ok || account.password != password {
			f.fail(w, http.StatusBadRequest, "invalid_credentials", "Invalid login credentials")
			return
		}
		if !account.confirmed {
			f.fail(w, http.StatusBadRequest, "email_not_confirmed", "Email not confirmed")
			return
		}
		writeJSON(w, f.issue(email))
	case r.URL.Path == "/auth/v1/token" && r.URL.Query().Get("grant_type") == "refresh_token":
		token, _ := body["refresh_token"].(string)
		email, ok := f.refresh[token]
		if !ok {
			f.fail(w, http.StatusBadRequest, "refresh_token_already_used", "Invalid Refresh Token: Already Used")
			return
		}
		delete(f.refresh, token)
		writeJSON(w, f.issue(email))
	case r.URL.Path == "/auth/v1/logout":
		email, ok := f.access[strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")]
		if !ok {
			f.fail(w, http.StatusUnauthorized, "bad_jwt", "invalid JWT")
			return
		}
		for token, owner := range f.refresh {
			if owner == email {
				delete(f.refresh, token)
			}
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		http.NotFound(w, r)
	}
}

func (f *fakeSupabase) issue(email string) map[string]any {
	f.seq++
	access := "access-" + email + "-" + strings.Repeat("a", f.seq)
	refresh := "refresh-" + email + "-" + strings.Repeat("r", f.seq)
	f.access[access] = email
	f.refresh[refresh] = email
	return map[string]any{
		"access_token": access, "refresh_token": refresh, "token_type": "bearer",
		"expires_in": 3600, "expires_at": 1792339027,
		"user": map[string]any{"id": "uid-" + email, "email": email, "user_metadata": f.accounts[email].metadata},
	}
}

func (f *fakeSupabase) fail(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"code": status, "error_code": code, "msg": msg})
}

func writeJSON(w http.ResponseWriter, body any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(body)
}
