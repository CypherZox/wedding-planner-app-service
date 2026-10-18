package session

import (
	"errors"
	"log/slog"
	"net/http"
	"time"

	"golang.org/x/time/rate"

	"github.com/CypherZox/wedding-planner/backend-service/internal/platform/auth"
	"github.com/CypherZox/wedding-planner/backend-service/internal/platform/httpx"
	"github.com/CypherZox/wedding-planner/backend-service/internal/platform/mongox"
	"github.com/CypherZox/wedding-planner/backend-service/internal/user"
)

// Handler serves the email and password auth routes.
type Handler struct {
	svc     *Service
	limiter *httpx.Limiter
}

// NewHandler allows 20 register, login, or refresh requests per minute per IP.
func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc, limiter: httpx.NewLimiter(rate.Every(3*time.Second), 10)}
}

// Register mounts the /v1/auth routes.
func (h *Handler) Register(mux *http.ServeMux) {
	limit := h.limiter.Middleware(httpx.ClientIP)
	mux.Handle("POST /v1/auth/register", limit(http.HandlerFunc(h.register)))
	mux.Handle("POST /v1/auth/login", limit(http.HandlerFunc(h.login)))
	mux.Handle("POST /v1/auth/refresh", limit(http.HandlerFunc(h.refresh)))
	mux.HandleFunc("POST /v1/auth/logout", h.logout)
}

type registerRequest struct {
	Email       string `json:"email" example:"ada@example.com"`
	Password    string `json:"password" example:"Correct-horse-9"`
	DisplayName string `json:"displayName" example:"Ada Lovelace" minLength:"1" maxLength:"200"`
	Locale      string `json:"locale" example:"en-US" maxLength:"35"`
} //	@name	RegisterRequest

type loginRequest struct {
	Email    string `json:"email" example:"ada@example.com"`
	Password string `json:"password" example:"Correct-horse-9"`
} //	@name	LoginRequest

type refreshRequest struct {
	RefreshToken string `json:"refreshToken"`
} //	@name	RefreshRequest

type sessionView struct {
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken"`
	TokenType    string `json:"tokenType" example:"bearer"`
	ExpiresIn    int    `json:"expiresIn" example:"3600"`
	ExpiresAt    int64  `json:"expiresAt" example:"1792339027"`
} //	@name	Session

type authResponse struct {
	Session sessionView `json:"session"`
	// User is missing when the account was offboarded. POST /v1/me restores it.
	User *user.View `json:"user,omitempty"`
} //	@name	AuthResponse

type confirmationResponse struct {
	ConfirmationRequired bool `json:"confirmationRequired" example:"true"`
} //	@name	ConfirmationResponse

// register godoc
//
//	@Summary		Register with email and password
//	@Description	Creates the Supabase Auth user and the app user. When the project requires email confirmation, no session is returned until the user confirms and logs in.
//	@Tags			auth
//	@Accept			json
//	@Produce		json
//	@Param			body	body		registerRequest			true	"Account"
//	@Success		201		{object}	authResponse			"Signed up and signed in"
//	@Success		202		{object}	confirmationResponse	"Check email to confirm, then log in"
//	@Failure		400		{object}	httpx.ErrorEnvelope
//	@Failure		403		{object}	httpx.ErrorEnvelope	"Sign-ups are disabled"
//	@Failure		409		{object}	httpx.ErrorEnvelope	"Email already registered"
//	@Failure		422		{object}	httpx.ErrorEnvelope	"Invalid email, weak password, or bad profile"
//	@Failure		429		{object}	httpx.ErrorEnvelope
//	@Router			/v1/auth/register [post]
func (h *Handler) register(w http.ResponseWriter, r *http.Request) {
	var body registerRequest
	if !httpx.ReadJSON(w, r, &body) {
		return
	}
	result, err := h.svc.Register(r.Context(), httpx.ClientIP(r), body.Email, body.Password, body.DisplayName, body.Locale)
	if err != nil {
		writeError(w, err)
		return
	}
	if result.Session == nil {
		httpx.JSON(w, http.StatusAccepted, confirmationResponse{ConfirmationRequired: true})
		return
	}
	httpx.JSON(w, http.StatusCreated, newAuthResponse(result))
}

// login godoc
//
//	@Summary		Log in with email and password
//	@Description	Returns a Supabase session. Send accessToken as "Authorization: Bearer <token>" on other routes, and use refreshToken with /v1/auth/refresh before it expires.
//	@Tags			auth
//	@Accept			json
//	@Produce		json
//	@Param			body	body		loginRequest	true	"Credentials"
//	@Success		200		{object}	authResponse
//	@Failure		400		{object}	httpx.ErrorEnvelope
//	@Failure		401		{object}	httpx.ErrorEnvelope	"Wrong email or password"
//	@Failure		403		{object}	httpx.ErrorEnvelope	"Email not confirmed"
//	@Failure		429		{object}	httpx.ErrorEnvelope
//	@Router			/v1/auth/login [post]
func (h *Handler) login(w http.ResponseWriter, r *http.Request) {
	var body loginRequest
	if !httpx.ReadJSON(w, r, &body) {
		return
	}
	result, err := h.svc.Login(r.Context(), httpx.ClientIP(r), body.Email, body.Password)
	if err != nil {
		writeError(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, newAuthResponse(result))
}

// refresh godoc
//
//	@Summary		Refresh the session
//	@Description	Each refresh token works once. Store the new refreshToken from the response.
//	@Tags			auth
//	@Accept			json
//	@Produce		json
//	@Param			body	body		refreshRequest	true	"Refresh token"
//	@Success		200		{object}	sessionView
//	@Failure		400		{object}	httpx.ErrorEnvelope
//	@Failure		401		{object}	httpx.ErrorEnvelope	"Refresh token is invalid or already used"
//	@Failure		429		{object}	httpx.ErrorEnvelope
//	@Router			/v1/auth/refresh [post]
func (h *Handler) refresh(w http.ResponseWriter, r *http.Request) {
	var body refreshRequest
	if !httpx.ReadJSON(w, r, &body) {
		return
	}
	session, err := h.svc.Refresh(r.Context(), httpx.ClientIP(r), body.RefreshToken)
	if err != nil {
		writeError(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, newSessionView(session))
}

// logout godoc
//
//	@Summary		Log out
//	@Description	Revokes the refresh tokens. The access token keeps working until it expires, so the app should delete both.
//	@Tags			auth
//	@Security		SupabaseToken
//	@Param			scope	query	string	false	"Which sessions to end"	Enums(local, global, others)	default(local)
//	@Success		204
//	@Failure		400	{object}	httpx.ErrorEnvelope
//	@Failure		401	{object}	httpx.ErrorEnvelope
//	@Router			/v1/auth/logout [post]
func (h *Handler) logout(w http.ResponseWriter, r *http.Request) {
	token, ok := httpx.BearerToken(r)
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "unauthenticated", "authentication required", nil)
		return
	}
	if err := h.svc.Logout(r.Context(), httpx.ClientIP(r), token, r.URL.Query().Get("scope")); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func newAuthResponse(result Result) authResponse {
	out := authResponse{Session: newSessionView(*result.Session)}
	if result.User != nil {
		v := user.NewView(*result.User)
		out.User = &v
	}
	return out
}

func newSessionView(s auth.Session) sessionView {
	return sessionView{
		AccessToken:  s.AccessToken,
		RefreshToken: s.RefreshToken,
		TokenType:    s.TokenType,
		ExpiresIn:    s.ExpiresIn,
		ExpiresAt:    s.ExpiresAt,
	}
}

func writeError(w http.ResponseWriter, err error) {
	var input *InputError
	var invalid *user.ValidationError
	var upstream *auth.Error
	switch {
	case errors.As(err, &input):
		httpx.Error(w, http.StatusUnprocessableEntity, "validation_failed", input.Error(), nil)
	case errors.As(err, &invalid):
		httpx.Error(w, http.StatusUnprocessableEntity, "validation_failed", invalid.Error(), nil)
	case errors.As(err, &upstream):
		writeAuthError(w, upstream)
	case errors.Is(err, mongox.ErrDuplicate):
		httpx.Error(w, http.StatusConflict, "duplicate", "an account with this email already exists", nil)
	default:
		slog.Error("auth request", "err", err)
		httpx.Error(w, http.StatusServiceUnavailable, "internal", "auth service unavailable", nil)
	}
}

func writeAuthError(w http.ResponseWriter, err *auth.Error) {
	switch err.Code {
	case "invalid_credentials", "invalid_grant":
		httpx.Error(w, http.StatusUnauthorized, "unauthenticated", "invalid email or password", nil)
	case "refresh_token_not_found", "refresh_token_already_used", "session_not_found", "session_expired", "bad_jwt":
		httpx.Error(w, http.StatusUnauthorized, "unauthenticated", "session is no longer valid", nil)
	case "email_not_confirmed":
		httpx.Error(w, http.StatusForbidden, "forbidden", "email is not confirmed", nil)
	case "signup_disabled", "email_provider_disabled":
		httpx.Error(w, http.StatusForbidden, "forbidden", "email sign-up is disabled", nil)
	case "user_already_exists", "email_exists":
		httpx.Error(w, http.StatusConflict, "duplicate", "an account with this email already exists", nil)
	case "weak_password":
		httpx.Error(w, http.StatusUnprocessableEntity, "validation_failed", err.Message, map[string]any{"reasons": err.Reasons})
	case "validation_failed", "email_address_invalid":
		httpx.Error(w, http.StatusUnprocessableEntity, "validation_failed", err.Message, nil)
	case "over_request_rate_limit", "over_email_send_rate_limit":
		httpx.Error(w, http.StatusTooManyRequests, "rate_limited", "too many requests", nil)
	default:
		switch {
		case err.Status == http.StatusTooManyRequests:
			httpx.Error(w, http.StatusTooManyRequests, "rate_limited", "too many requests", nil)
		case err.Status == http.StatusUnauthorized || err.Status == http.StatusForbidden:
			httpx.Error(w, http.StatusUnauthorized, "unauthenticated", "authentication required", nil)
		case err.Status >= 400 && err.Status < 500:
			slog.Warn("supabase auth rejected request", "status", err.Status, "code", err.Code)
			httpx.Error(w, http.StatusBadRequest, "bad_request", "request was rejected", nil)
		default:
			slog.Error("supabase auth failed", "status", err.Status, "code", err.Code)
			httpx.Error(w, http.StatusServiceUnavailable, "internal", "auth service unavailable", nil)
		}
	}
}
