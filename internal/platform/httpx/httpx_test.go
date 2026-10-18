package httpx

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
	"golang.org/x/time/rate"

	"github.com/CypherZox/wedding-planner/backend-service/internal/platform/mongox"
)

func TestErrorEnvelope(t *testing.T) {
	rec := httptest.NewRecorder()
	Error(rec, http.StatusConflict, "version_conflict", "stale version", map[string]any{"version": 7})

	var body struct {
		Error struct {
			Code    string         `json:"code"`
			Message string         `json:"message"`
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if rec.Code != http.StatusConflict || body.Error.Code != "version_conflict" || body.Error.Details["version"] != float64(7) {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
}

func TestRejectOperators(t *testing.T) {
	if err := RejectOperators(map[string]any{"set": map[string]any{"title": "a"}}); err != nil {
		t.Fatalf("valid keys rejected: %v", err)
	}
	for _, body := range []any{
		map[string]any{"$set": 1},
		map[string]any{"set": map[string]any{"a.b": 1}},
	} {
		if err := RejectOperators(body); !errors.Is(err, ErrOperatorKey) {
			t.Fatalf("RejectOperators(%v) = %v", body, err)
		}
	}
}

func TestLimitBody(t *testing.T) {
	handler := LimitBody(8)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var dst map[string]any
		ReadJSON(w, r, &dst)
	}))

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"name":"too-long"}`))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "bad_request") {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
}

func TestRateLimit(t *testing.T) {
	limiter := NewLimiter(rate.Limit(1), 1)
	handler := limiter.Middleware(func(*http.Request) string { return "same" })(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	first := httptest.NewRecorder()
	handler.ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/", nil))
	second := httptest.NewRecorder()
	handler.ServeHTTP(second, httptest.NewRequest(http.MethodGet, "/", nil))
	if first.Code != http.StatusNoContent || second.Code != http.StatusTooManyRequests {
		t.Fatalf("first %d second %d", first.Code, second.Code)
	}
	if !strings.Contains(second.Body.String(), "rate_limited") {
		t.Fatalf("body %s", second.Body.String())
	}
}

func TestRecover(t *testing.T) {
	handler := Recover(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	}))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusInternalServerError || !strings.Contains(rec.Body.String(), "internal") {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
}

type fakeAuth struct{}

func (fakeAuth) Authenticate(context.Context, string) (Identity, error) {
	return Identity{UserID: bson.NewObjectID(), AuthUID: "uid"}, nil
}

func TestAuthenticate(t *testing.T) {
	var got bool
	handler := Authenticate(fakeAuth{})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, got = IdentityFrom(r.Context())
		w.WriteHeader(http.StatusNoContent)
	}))

	missing := httptest.NewRecorder()
	handler.ServeHTTP(missing, httptest.NewRequest(http.MethodGet, "/", nil))
	if missing.Code != http.StatusUnauthorized {
		t.Fatalf("missing token status %d", missing.Code)
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer token")
	ok := httptest.NewRecorder()
	handler.ServeHTTP(ok, req)
	if ok.Code != http.StatusNoContent || !got {
		t.Fatalf("status %d identity %v", ok.Code, got)
	}
}

type fakeMembers struct {
	calls int
}

func (f *fakeMembers) Load(context.Context, bson.ObjectID, bson.ObjectID) (Access, error) {
	f.calls++
	if f.calls == 1 {
		return Access{}, mongox.ErrNotFound
	}
	return Access{Role: "editor", CanViewBudget: false}, nil
}

func TestRequireMemberCachesAndHidesMissing(t *testing.T) {
	store := &fakeMembers{}
	cache := NewMembershipCache(store)
	userID := bson.NewObjectID()
	weddingID := bson.NewObjectID()

	handler := cache.RequireMember(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		access, ok := AccessFrom(r.Context())
		if !ok || access.Role != "editor" {
			t.Fatalf("access %+v ok %v", access, ok)
		}
		w.WriteHeader(http.StatusNoContent)
	}))

	denied := httptest.NewRequest(http.MethodGet, "/v1/weddings/"+weddingID.Hex()+"/guests", nil)
	denied = denied.WithContext(WithIdentity(denied.Context(), Identity{UserID: userID}))
	denied.SetPathValue("weddingId", weddingID.Hex())
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, denied)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing member status %d", rec.Code)
	}

	allowed := httptest.NewRequest(http.MethodGet, "/v1/weddings/"+weddingID.Hex()+"/guests", nil)
	allowed = allowed.WithContext(WithIdentity(allowed.Context(), Identity{UserID: userID}))
	allowed.SetPathValue("weddingId", weddingID.Hex())
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, allowed)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, allowed)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("member status %d", rec.Code)
	}
	if store.calls != 2 {
		t.Fatalf("loader calls = %d, want 2 (the second hit is cached)", store.calls)
	}

	cache.Invalidate(weddingID)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, allowed)
	if store.calls != 3 {
		t.Fatalf("loader calls after invalidate = %d", store.calls)
	}
}
