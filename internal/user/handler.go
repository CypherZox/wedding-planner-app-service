package user

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/CypherZox/wedding-planner/backend-service/internal/platform/auth"
	"github.com/CypherZox/wedding-planner/backend-service/internal/platform/httpx"
	"github.com/CypherZox/wedding-planner/backend-service/internal/platform/mongox"
)

// Handler serves the account routes.
type Handler struct {
	svc *Service
}

// NewHandler returns the account handler.
func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

// Register mounts POST, GET, PATCH, and DELETE /v1/me.
// Users have no version field, so PATCH takes displayName and locale directly.
func (h *Handler) Register(mux *http.ServeMux, tokens interface {
	Verify(ctx context.Context, token string) (auth.Claims, error)
}) {
	protect := httpx.RequireToken(func(ctx context.Context, token string) (httpx.Session, error) {
		claims, err := tokens.Verify(ctx, token)
		if err != nil {
			return httpx.Session{}, err
		}
		return httpx.Session{
			AuthUID:       claims.Subject,
			Email:         claims.Email,
			EmailVerified: claims.EmailVerified,
		}, nil
	})
	mux.Handle("POST /v1/me", protect(http.HandlerFunc(h.onboard)))
	mux.Handle("GET /v1/me", protect(http.HandlerFunc(h.current)))
	mux.Handle("PATCH /v1/me", protect(http.HandlerFunc(h.update)))
	mux.Handle("DELETE /v1/me", protect(http.HandlerFunc(h.offboard)))
}

type onboardRequest struct {
	DisplayName string `json:"displayName" example:"Ada Lovelace" minLength:"1" maxLength:"200"`
	Locale      string `json:"locale" example:"en-US" maxLength:"35"`
} //	@name	OnboardRequest

type updateRequest struct {
	DisplayName *string `json:"displayName,omitempty" example:"Ada Lovelace" minLength:"1" maxLength:"200"`
	Locale      *string `json:"locale,omitempty" example:"fr" maxLength:"35"`
} //	@name	UpdateRequest

// onboard godoc
//
//	@Summary		Onboard the signed-in user
//	@Description	Creates the user from the Supabase token. Calling it again returns the existing user, and calling it after offboarding restores that user.
//	@Tags			account
//	@Accept			json
//	@Produce		json
//	@Security		SupabaseToken
//	@Param			body	body		onboardRequest	true	"Profile"
//	@Success		201		{object}	View			"User created"
//	@Success		200		{object}	View			"Existing or restored user"
//	@Failure		400		{object}	httpx.ErrorEnvelope
//	@Failure		401		{object}	httpx.ErrorEnvelope
//	@Failure		409		{object}	httpx.ErrorEnvelope	"Email belongs to another account"
//	@Failure		422		{object}	httpx.ErrorEnvelope
//	@Router			/v1/me [post]
func (h *Handler) onboard(w http.ResponseWriter, r *http.Request) {
	account, ok := accountFrom(w, r)
	if !ok {
		return
	}
	var body onboardRequest
	if !httpx.ReadJSON(w, r, &body) {
		return
	}
	user, created, err := h.svc.Onboard(r.Context(), account, body.DisplayName, body.Locale)
	if err != nil {
		writeError(w, err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	httpx.JSON(w, status, NewView(user))
}

// current godoc
//
//	@Summary	Get the signed-in user
//	@Tags		account
//	@Produce	json
//	@Security	SupabaseToken
//	@Success	200	{object}	View
//	@Failure	401	{object}	httpx.ErrorEnvelope
//	@Failure	404	{object}	httpx.ErrorEnvelope	"Not onboarded, or offboarded"
//	@Router		/v1/me [get]
func (h *Handler) current(w http.ResponseWriter, r *http.Request) {
	account, ok := accountFrom(w, r)
	if !ok {
		return
	}
	user, err := h.svc.Current(r.Context(), account.AuthUID)
	if err != nil {
		writeError(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, NewView(user))
}

// update godoc
//
//	@Summary	Update the signed-in user's profile
//	@Tags		account
//	@Accept		json
//	@Produce	json
//	@Security	SupabaseToken
//	@Param		body	body		updateRequest	true	"Fields to change"
//	@Success	200		{object}	View
//	@Failure	400		{object}	httpx.ErrorEnvelope
//	@Failure	401		{object}	httpx.ErrorEnvelope
//	@Failure	404		{object}	httpx.ErrorEnvelope
//	@Failure	422		{object}	httpx.ErrorEnvelope
//	@Router		/v1/me [patch]
func (h *Handler) update(w http.ResponseWriter, r *http.Request) {
	account, ok := accountFrom(w, r)
	if !ok {
		return
	}
	var body updateRequest
	if !httpx.ReadJSON(w, r, &body) {
		return
	}
	user, err := h.svc.Update(r.Context(), account.AuthUID, body.DisplayName, body.Locale)
	if err != nil {
		writeError(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, NewView(user))
}

// offboard godoc
//
//	@Summary		Offboard the signed-in user
//	@Description	Soft-deletes the user in this API. The Supabase Auth user is not deleted.
//	@Tags			account
//	@Security		SupabaseToken
//	@Success		204
//	@Failure		401	{object}	httpx.ErrorEnvelope
//	@Failure		404	{object}	httpx.ErrorEnvelope
//	@Router			/v1/me [delete]
func (h *Handler) offboard(w http.ResponseWriter, r *http.Request) {
	account, ok := accountFrom(w, r)
	if !ok {
		return
	}
	if err := h.svc.Offboard(r.Context(), account.AuthUID); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func accountFrom(w http.ResponseWriter, r *http.Request) (Account, bool) {
	session, ok := httpx.SessionFrom(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "unauthenticated", "authentication required", nil)
		return Account{}, false
	}
	return Account{
		AuthUID:       session.AuthUID,
		Email:         session.Email,
		EmailVerified: session.EmailVerified,
	}, true
}

// View is a user as the API returns it.
type View struct {
	ID             string    `json:"id"`
	AuthProvider   string    `json:"authProvider"`
	AuthUID        string    `json:"authUid"`
	Email          string    `json:"email"`
	EmailVerified  bool      `json:"emailVerified"`
	DisplayName    string    `json:"displayName"`
	AvatarMediaKey string    `json:"avatarMediaKey,omitempty"`
	Locale         string    `json:"locale"`
	LastWeddingID  string    `json:"lastWeddingId,omitempty"`
	LastSeenAt     time.Time `json:"lastSeenAt"`
	CreatedAt      time.Time `json:"createdAt"`
	UpdatedAt      time.Time `json:"updatedAt"`
} //	@name	User

// NewView is the JSON shape of a user.
func NewView(user User) View {
	out := View{
		ID:             user.ID.Hex(),
		AuthProvider:   user.AuthProvider,
		AuthUID:        user.AuthUID,
		Email:          user.Email,
		EmailVerified:  user.EmailVerified,
		DisplayName:    user.DisplayName,
		AvatarMediaKey: user.AvatarMediaKey,
		Locale:         user.Locale,
		LastSeenAt:     user.LastSeenAt,
		CreatedAt:      user.CreatedAt,
		UpdatedAt:      user.UpdatedAt,
	}
	if !user.LastWeddingID.IsZero() {
		out.LastWeddingID = user.LastWeddingID.Hex()
	}
	return out
}

func writeError(w http.ResponseWriter, err error) {
	var invalid *ValidationError
	switch {
	case errors.As(err, &invalid):
		httpx.Error(w, http.StatusUnprocessableEntity, "validation_failed", invalid.Error(), nil)
	case errors.Is(err, mongox.ErrNotFound):
		httpx.Error(w, http.StatusNotFound, "not_found", "user not found", nil)
	case errors.Is(err, mongox.ErrDuplicate):
		httpx.Error(w, http.StatusConflict, "duplicate", "an account with this email already exists", nil)
	default:
		slog.Error("user request", "err", err)
		httpx.Error(w, http.StatusInternalServerError, "internal", "internal error", nil)
	}
}
