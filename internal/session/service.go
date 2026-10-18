package session

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/CypherZox/wedding-planner/backend-service/internal/platform/auth"
	"github.com/CypherZox/wedding-planner/backend-service/internal/platform/mongox"
	"github.com/CypherZox/wedding-planner/backend-service/internal/user"
)

const (
	defaultLocale = "en"
	// Supabase Auth hashes passwords with bcrypt, which reads at most 72 bytes.
	maxPasswordBytes = 72
)

// AuthAPI is the Supabase Auth email and password API.
type AuthAPI interface {
	SignUp(ctx context.Context, clientIP, email, password string, metadata map[string]any) (*auth.Session, error)
	SignIn(ctx context.Context, clientIP, email, password string) (auth.Session, error)
	Refresh(ctx context.Context, clientIP, refreshToken string) (auth.Session, error)
	SignOut(ctx context.Context, clientIP, accessToken, scope string) error
}

// TokenVerifier verifies a Supabase access token.
type TokenVerifier interface {
	Verify(ctx context.Context, token string) (auth.Claims, error)
}

// Users creates the app user for a Supabase account.
type Users interface {
	Ensure(ctx context.Context, account user.Account, displayName, locale string) (user.User, error)
}

// InputError is a request value rejected before it reaches Supabase.
type InputError struct {
	Message string
}

func (e *InputError) Error() string { return e.Message }

// Result is a session and, when the account is active, its app user.
// Session is nil when Supabase requires email confirmation first.
type Result struct {
	Session *auth.Session
	User    *user.User
}

// Service registers, signs in, refreshes, and signs out email and password users.
type Service struct {
	auth   AuthAPI
	tokens TokenVerifier
	users  Users
}

// NewService returns a session service.
func NewService(authAPI AuthAPI, tokens TokenVerifier, users Users) *Service {
	return &Service{auth: authAPI, tokens: tokens, users: users}
}

// Register creates the Supabase user and, when a session is issued, the app user.
func (s *Service) Register(ctx context.Context, clientIP, email, password, displayName, locale string) (Result, error) {
	email, err := checkCredentials(email, password)
	if err != nil {
		return Result{}, err
	}
	name, parsed, err := user.NormalizeProfile(displayName, locale)
	if err != nil {
		return Result{}, err
	}
	session, err := s.auth.SignUp(ctx, clientIP, email, password, map[string]any{
		"display_name": name,
		"locale":       parsed,
	})
	if err != nil {
		return Result{}, err
	}
	if session == nil {
		return Result{}, nil
	}
	return s.withUser(ctx, *session, name, parsed)
}

// Login signs in with email and password and creates the app user if it is missing.
// An offboarded account still gets a session, with no user.
func (s *Service) Login(ctx context.Context, clientIP, email, password string) (Result, error) {
	email, err := checkCredentials(email, password)
	if err != nil {
		return Result{}, err
	}
	session, err := s.auth.SignIn(ctx, clientIP, email, password)
	if err != nil {
		return Result{}, err
	}
	name, locale := profileFromMetadata(session.User)
	return s.withUser(ctx, session, name, locale)
}

// Refresh exchanges a refresh token for a new session.
func (s *Service) Refresh(ctx context.Context, clientIP, refreshToken string) (auth.Session, error) {
	refreshToken = strings.TrimSpace(refreshToken)
	if refreshToken == "" {
		return auth.Session{}, &InputError{Message: "refreshToken is required"}
	}
	return s.auth.Refresh(ctx, clientIP, refreshToken)
}

// Logout revokes the session's refresh tokens. scope is local, global, or others.
func (s *Service) Logout(ctx context.Context, clientIP, accessToken, scope string) error {
	switch scope {
	case "":
		scope = "local"
	case "local", "global", "others":
	default:
		return &InputError{Message: "scope must be local, global, or others"}
	}
	return s.auth.SignOut(ctx, clientIP, accessToken, scope)
}

func (s *Service) withUser(ctx context.Context, session auth.Session, displayName, locale string) (Result, error) {
	claims, err := s.tokens.Verify(ctx, session.AccessToken)
	if err != nil {
		return Result{}, err
	}
	account := user.Account{AuthUID: claims.Subject, Email: claims.Email, EmailVerified: claims.EmailVerified}
	u, err := s.users.Ensure(ctx, account, displayName, locale)
	if errors.Is(err, mongox.ErrNotFound) {
		return Result{Session: &session}, nil
	}
	if err != nil {
		return Result{}, err
	}
	return Result{Session: &session, User: &u}, nil
}

func checkCredentials(email, password string) (string, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" || utf8.RuneCountInString(email) > 320 || !strings.Contains(email, "@") {
		return "", &InputError{Message: "email is invalid"}
	}
	if password == "" {
		return "", &InputError{Message: "password is required"}
	}
	if len(password) > maxPasswordBytes {
		return "", &InputError{Message: "password must be at most 72 bytes"}
	}
	return email, nil
}

// profileFromMetadata reads the profile stored at registration. Accounts created
// outside this API may not have one, so it falls back to the email name and "en".
func profileFromMetadata(u auth.AuthUser) (string, string) {
	name, _ := u.UserMetadata["display_name"].(string)
	locale, _ := u.UserMetadata["locale"].(string)
	if _, _, err := user.NormalizeProfile("x", locale); err != nil {
		locale = defaultLocale
	}
	if _, _, err := user.NormalizeProfile(name, defaultLocale); err != nil {
		name, _, _ = strings.Cut(u.Email, "@")
	}
	return name, locale
}
