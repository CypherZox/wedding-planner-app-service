package httpx

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"runtime/debug"
	"sync"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"golang.org/x/time/rate"

	"github.com/CypherZox/wedding-planner/backend-service/internal/platform/mongox"
)

type identityKey struct{}
type accessKey struct{}
type sessionKey struct{}

// Session is a verified Supabase access token, before a user document exists.
type Session struct {
	AuthUID       string
	Email         string
	EmailVerified bool
}

// Identity is the authenticated user.
type Identity struct {
	UserID  bson.ObjectID
	AuthUID string
}

// Access is the caller's membership in one wedding.
type Access struct {
	UserID        bson.ObjectID
	WeddingID     bson.ObjectID
	Role          string
	CanViewBudget bool
}

// Authenticator verifies a bearer token. Supabase access tokens are checked here.
type Authenticator interface {
	Authenticate(ctx context.Context, token string) (Identity, error)
}

// MemberStore loads one membership. A missing membership returns mongox.ErrNotFound.
type MemberStore interface {
	Load(ctx context.Context, weddingID, userID bson.ObjectID) (Access, error)
}

// WithIdentity stores the authenticated user on the context.
func WithIdentity(ctx context.Context, id Identity) context.Context {
	return context.WithValue(ctx, identityKey{}, id)
}

// IdentityFrom returns the authenticated user, if middleware stored one.
func IdentityFrom(ctx context.Context) (Identity, bool) {
	id, ok := ctx.Value(identityKey{}).(Identity)
	return id, ok
}

// WithAccess stores a membership on the context.
func WithAccess(ctx context.Context, access Access) context.Context {
	return context.WithValue(ctx, accessKey{}, access)
}

// AccessFrom returns the membership, if middleware stored one.
func AccessFrom(ctx context.Context) (Access, bool) {
	access, ok := ctx.Value(accessKey{}).(Access)
	return access, ok
}

// WithSession stores a verified token on the context.
func WithSession(ctx context.Context, session Session) context.Context {
	return context.WithValue(ctx, sessionKey{}, session)
}

// SessionFrom returns the verified token, if middleware stored one.
func SessionFrom(ctx context.Context) (Session, bool) {
	session, ok := ctx.Value(sessionKey{}).(Session)
	return session, ok
}

// RequireToken verifies a bearer token and stores the session.
// These routes can run before a user document exists.
func RequireToken(verify func(context.Context, string) (Session, error)) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token, ok := bearer(r.Header.Get("Authorization"))
			if !ok {
				Error(w, http.StatusUnauthorized, "unauthenticated", "authentication required", nil)
				return
			}
			session, err := verify(r.Context(), token)
			if err != nil || session.AuthUID == "" {
				slog.Error("authentication failed")
				Error(w, http.StatusUnauthorized, "unauthenticated", "authentication required", nil)
				return
			}
			next.ServeHTTP(w, r.WithContext(WithSession(r.Context(), session)))
		})
	}
}

// Recover turns a panic into a 500 response.
func Recover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			rec := recover()
			if rec == nil {
				return
			}
			slog.Error("panic", "err", rec, "stack", string(debug.Stack()))
			Error(w, http.StatusInternalServerError, "internal", "internal error", nil)
		}()
		next.ServeHTTP(w, r)
	})
}

// LimitBody caps the request body. Readers see http.MaxBytesError past this size.
func LimitBody(maxBytes int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Body != nil {
				r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
			}
			next.ServeHTTP(w, r)
		})
	}
}

// Limiter is an in-memory per-key rate limiter.
type Limiter struct {
	mu    sync.Mutex
	limit rate.Limit
	burst int
	keys  map[string]*rate.Limiter
}

// NewLimiter returns a limiter that allows limit events per second with the given burst.
func NewLimiter(limit rate.Limit, burst int) *Limiter {
	return &Limiter{limit: limit, burst: burst, keys: map[string]*rate.Limiter{}}
}

// Middleware rejects requests whose key is over the limit.
func (l *Limiter) Middleware(key func(*http.Request) string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !l.allow(key(r)) {
				Error(w, http.StatusTooManyRequests, "rate_limited", "too many requests", nil)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func (l *Limiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	limiter := l.keys[key]
	if limiter == nil {
		limiter = rate.NewLimiter(l.limit, l.burst)
		l.keys[key] = limiter
	}
	return limiter.Allow()
}

// Authenticate requires a bearer token and stores the identity.
func Authenticate(authn Authenticator) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token, ok := bearer(r.Header.Get("Authorization"))
			if !ok {
				Error(w, http.StatusUnauthorized, "unauthenticated", "authentication required", nil)
				return
			}
			identity, err := authn.Authenticate(r.Context(), token)
			if err != nil {
				slog.Error("authentication failed")
				Error(w, http.StatusUnauthorized, "unauthenticated", "authentication required", nil)
				return
			}
			next.ServeHTTP(w, r.WithContext(WithIdentity(r.Context(), identity)))
		})
	}
}

// BearerToken returns the token from the Authorization header.
func BearerToken(r *http.Request) (string, bool) {
	return bearer(r.Header.Get("Authorization"))
}

// ClientIP is the address of the TCP peer. Forwarded headers are not trusted.
func ClientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func bearer(header string) (string, bool) {
	const prefix = "Bearer "
	if len(header) <= len(prefix) || header[:len(prefix)] != prefix {
		return "", false
	}
	token := header[len(prefix):]
	if token == "" {
		return "", false
	}
	return token, true
}

type cachedAccess struct {
	access  Access
	expires time.Time
}

// MembershipCache caches membership lookups for one TTL.
type MembershipCache struct {
	store MemberStore
	ttl   time.Duration
	now   func() time.Time

	mu      sync.Mutex
	entries map[string]cachedAccess
}

// NewMembershipCache caches store lookups for 60 seconds.
func NewMembershipCache(store MemberStore) *MembershipCache {
	return &MembershipCache{
		store:   store,
		ttl:     60 * time.Second,
		now:     time.Now,
		entries: map[string]cachedAccess{},
	}
}

// Invalidate drops cached memberships for a wedding.
func (c *MembershipCache) Invalidate(weddingID bson.ObjectID) {
	c.mu.Lock()
	defer c.mu.Unlock()
	prefix := weddingID.Hex() + ":"
	for key := range c.entries {
		if len(key) >= len(prefix) && key[:len(prefix)] == prefix {
			delete(c.entries, key)
		}
	}
}

// RequireMember loads the caller's membership and stores it on the context.
// Someone who is not a member gets 404.
func (c *MembershipCache) RequireMember(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		identity, ok := IdentityFrom(r.Context())
		if !ok {
			Error(w, http.StatusUnauthorized, "unauthenticated", "authentication required", nil)
			return
		}
		weddingID, err := bson.ObjectIDFromHex(r.PathValue("weddingId"))
		if err != nil {
			Error(w, http.StatusBadRequest, "bad_request", "weddingId must be a 24-character hex string", nil)
			return
		}
		access, err := c.load(r.Context(), weddingID, identity.UserID)
		if err != nil {
			if errors.Is(err, mongox.ErrNotFound) {
				Error(w, http.StatusNotFound, "not_found", "wedding not found", nil)
				return
			}
			slog.Error("load membership", "err", err)
			Error(w, http.StatusInternalServerError, "internal", "internal error", nil)
			return
		}
		next.ServeHTTP(w, r.WithContext(WithAccess(r.Context(), access)))
	})
}

func (c *MembershipCache) load(ctx context.Context, weddingID, userID bson.ObjectID) (Access, error) {
	key := weddingID.Hex() + ":" + userID.Hex()
	c.mu.Lock()
	if entry, ok := c.entries[key]; ok && c.now().Before(entry.expires) {
		access := entry.access
		c.mu.Unlock()
		return access, nil
	}
	c.mu.Unlock()

	access, err := c.store.Load(ctx, weddingID, userID)
	if err != nil {
		return Access{}, err
	}
	c.mu.Lock()
	c.entries[key] = cachedAccess{access: access, expires: c.now().Add(c.ttl)}
	c.mu.Unlock()
	return access, nil
}
