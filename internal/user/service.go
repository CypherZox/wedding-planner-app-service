package user

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"go.mongodb.org/mongo-driver/v2/bson"
	"golang.org/x/text/language"

	"github.com/CypherZox/wedding-planner/backend-service/internal/platform/clock"
	"github.com/CypherZox/wedding-planner/backend-service/internal/platform/mongox"
)

// ValidationError is a client-supplied profile value the schema would reject.
type ValidationError struct {
	Message string
}

func (e *ValidationError) Error() string { return e.Message }

// Service onboards, reads, updates, and offboards users.
type Service struct {
	repo  *Repo
	clock clock.Clock
}

// NewService returns a user service.
func NewService(repo *Repo, clock clock.Clock) *Service {
	return &Service{repo: repo, clock: clock}
}

// Onboard creates the account for this Supabase user, or returns the one that already exists.
// A soft-deleted account is restored. created is true only for the first insert.
func (s *Service) Onboard(ctx context.Context, account Account, displayName, locale string) (User, bool, error) {
	profile, err := newProfile(account, displayName, locale)
	if err != nil {
		return User{}, false, err
	}
	now := s.clock.Now()
	existing, err := s.repo.FindByAuthUID(ctx, profile.AuthUID)
	if errors.Is(err, mongox.ErrNotFound) {
		user := User{
			ID:            bson.NewObjectID(),
			AuthProvider:  authProvider,
			AuthUID:       profile.AuthUID,
			Email:         profile.Email,
			EmailVerified: profile.EmailVerified,
			DisplayName:   profile.DisplayName,
			Locale:        profile.Locale,
			LastSeenAt:    now,
			CreatedAt:     now,
			UpdatedAt:     now,
		}
		err = s.repo.Insert(ctx, user)
		if errors.Is(err, mongox.ErrDuplicate) {
			return s.onboardExisting(ctx, profile, now)
		}
		if err != nil {
			return User{}, false, err
		}
		return user, true, nil
	}
	if err != nil {
		return User{}, false, err
	}
	if existing.DeletedAt != nil {
		existing.Email = profile.Email
		existing.EmailVerified = profile.EmailVerified
		existing.DisplayName = profile.DisplayName
		existing.Locale = profile.Locale
		existing.LastSeenAt = now
		existing.UpdatedAt = now
		existing.DeletedAt = nil
		if err := s.repo.Restore(ctx, profile.AuthUID, existing); err != nil {
			return User{}, false, err
		}
		return existing, false, nil
	}
	if err := s.repo.SyncAuth(ctx, profile.AuthUID, profile.Email, profile.EmailVerified, now); err != nil {
		return User{}, false, err
	}
	existing.Email = profile.Email
	existing.EmailVerified = profile.EmailVerified
	existing.LastSeenAt = now
	existing.UpdatedAt = now
	return existing, false, nil
}

// Ensure returns the active user for a signed-in account, creating it when missing.
// An offboarded account returns mongox.ErrNotFound; POST /v1/me restores it.
func (s *Service) Ensure(ctx context.Context, account Account, displayName, locale string) (User, error) {
	existing, err := s.repo.FindByAuthUID(ctx, account.AuthUID)
	if err != nil && !errors.Is(err, mongox.ErrNotFound) {
		return User{}, err
	}
	if err == nil && existing.DeletedAt != nil {
		return User{}, mongox.ErrNotFound
	}
	user, _, err := s.Onboard(ctx, account, displayName, locale)
	return user, err
}

// NormalizeProfile validates displayName and locale the way the users schema requires.
func NormalizeProfile(displayName, locale string) (string, string, error) {
	name, err := normalizeDisplayName(displayName)
	if err != nil {
		return "", "", err
	}
	parsed, err := normalizeLocale(locale)
	if err != nil {
		return "", "", err
	}
	return name, parsed, nil
}

// Current returns the active account and records lastSeenAt.
func (s *Service) Current(ctx context.Context, authUID string) (User, error) {
	now := s.clock.Now()
	if err := s.repo.TouchLastSeen(ctx, authUID, now); err != nil {
		return User{}, err
	}
	user, err := s.repo.FindActive(ctx, authUID)
	if err != nil {
		return User{}, err
	}
	user.LastSeenAt = now
	return user, nil
}

// Update changes displayName, locale, or both.
func (s *Service) Update(ctx context.Context, authUID string, displayName, locale *string) (User, error) {
	set := bson.D{}
	if displayName != nil {
		name, err := normalizeDisplayName(*displayName)
		if err != nil {
			return User{}, err
		}
		set = append(set, bson.E{Key: "displayName", Value: name})
	}
	if locale != nil {
		parsed, err := normalizeLocale(*locale)
		if err != nil {
			return User{}, err
		}
		set = append(set, bson.E{Key: "locale", Value: parsed})
	}
	if len(set) == 0 {
		return User{}, &ValidationError{Message: "displayName or locale is required"}
	}
	now := s.clock.Now()
	set = append(set, bson.E{Key: "updatedAt", Value: now}, bson.E{Key: "lastSeenAt", Value: now})
	if err := s.repo.UpdateProfile(ctx, authUID, set); err != nil {
		return User{}, err
	}
	return s.repo.FindActive(ctx, authUID)
}

// Offboard soft-deletes the active account.
func (s *Service) Offboard(ctx context.Context, authUID string) error {
	return s.repo.SoftDelete(ctx, authUID, s.clock.Now())
}

type profile struct {
	AuthUID       string
	Email         string
	EmailVerified bool
	DisplayName   string
	Locale        string
}

func (s *Service) onboardExisting(ctx context.Context, profile profile, now time.Time) (User, bool, error) {
	existing, err := s.repo.FindByAuthUID(ctx, profile.AuthUID)
	if errors.Is(err, mongox.ErrNotFound) {
		return User{}, false, mongox.ErrDuplicate
	}
	if err != nil {
		return User{}, false, err
	}
	if existing.DeletedAt != nil {
		existing.Email = profile.Email
		existing.EmailVerified = profile.EmailVerified
		existing.DisplayName = profile.DisplayName
		existing.Locale = profile.Locale
		existing.LastSeenAt = now
		existing.UpdatedAt = now
		existing.DeletedAt = nil
		if err := s.repo.Restore(ctx, profile.AuthUID, existing); err != nil {
			return User{}, false, err
		}
		return existing, false, nil
	}
	if err := s.repo.SyncAuth(ctx, profile.AuthUID, profile.Email, profile.EmailVerified, now); err != nil {
		return User{}, false, err
	}
	existing.Email = profile.Email
	existing.EmailVerified = profile.EmailVerified
	existing.LastSeenAt = now
	existing.UpdatedAt = now
	return existing, false, nil
}

func newProfile(account Account, displayName, locale string) (profile, error) {
	if account.AuthUID == "" || utf8.RuneCountInString(account.AuthUID) > 128 {
		return profile{}, &ValidationError{Message: "auth subject is invalid"}
	}
	email, err := normalizeEmail(account.Email)
	if err != nil {
		return profile{}, err
	}
	name, err := normalizeDisplayName(displayName)
	if err != nil {
		return profile{}, err
	}
	parsed, err := normalizeLocale(locale)
	if err != nil {
		return profile{}, err
	}
	return profile{
		AuthUID:       account.AuthUID,
		Email:         email,
		EmailVerified: account.EmailVerified,
		DisplayName:   name,
		Locale:        parsed,
	}, nil
}

func normalizeEmail(email string) (string, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	if len(email) > 320 || !strings.Contains(email, "@") || strings.ContainsAny(email, " \t") {
		return "", &ValidationError{Message: "email from the auth provider is invalid"}
	}
	local, domain, ok := strings.Cut(email, "@")
	if !ok || local == "" || !strings.Contains(domain, ".") || strings.HasPrefix(domain, ".") || strings.HasSuffix(domain, ".") {
		return "", &ValidationError{Message: "email from the auth provider is invalid"}
	}
	return email, nil
}

func normalizeDisplayName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > 200 {
		return "", &ValidationError{Message: "displayName must be 1 to 200 characters"}
	}
	return name, nil
}

func normalizeLocale(locale string) (string, error) {
	locale = strings.TrimSpace(locale)
	tag, err := language.Parse(locale)
	if err != nil || utf8.RuneCountInString(tag.String()) > 35 {
		return "", &ValidationError{Message: "locale must be a BCP 47 language tag"}
	}
	return tag.String(), nil
}
