package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/CypherZox/wedding-planner/backend-service/internal/platform/auth"
	"github.com/CypherZox/wedding-planner/backend-service/internal/platform/clock"
	"github.com/CypherZox/wedding-planner/backend-service/internal/platform/config"
	"github.com/CypherZox/wedding-planner/backend-service/internal/platform/httpx"
	"github.com/CypherZox/wedding-planner/backend-service/internal/platform/mongox"
	"github.com/CypherZox/wedding-planner/backend-service/internal/session"
	"github.com/CypherZox/wedding-planner/backend-service/internal/user"
)

//	@title			Wedding Planner API
//	@version		0.1.0
//	@description	Backend for the wedding planner app. Get a session from /v1/auth/login or /v1/auth/register, then send its accessToken on the other routes.
//	@host			localhost:8080
//	@BasePath		/
//	@schemes		http

//	@securityDefinitions.apikey	SupabaseToken
//	@in							header
//	@name						Authorization
//	@description				Supabase Auth access token, sent as "Bearer <token>".

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	if err := run(); err != nil {
		slog.Error("server stopped", "err", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	verifier, err := auth.New(cfg.SupabaseURL, cfg.SupabasePublishableKey, cfg.SupabaseJWKSURL)
	if err != nil {
		return err
	}
	connectCtx, connectCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer connectCancel()
	if err := verifier.Connect(connectCtx); err != nil {
		return err
	}
	slog.Info("supabase auth connected", "url", cfg.SupabaseURL)

	client, err := mongox.Connect(cfg.MongoURI)
	if err != nil {
		return err
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := client.Disconnect(ctx); err != nil {
			slog.Error("disconnect mongodb", "err", err)
		}
	}()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", healthz)
	mux.HandleFunc("GET /readyz", readyz(client, verifier))

	authClient, err := auth.NewClient(cfg.SupabaseURL, cfg.SupabaseSecretKey, nil)
	if err != nil {
		return err
	}
	users := user.NewService(user.NewRepo(client.Database(cfg.MongoDB)), clock.Real{})
	user.NewHandler(users).Register(mux, verifier)
	session.NewHandler(session.NewService(authClient, verifier, users)).Register(mux)

	server := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           httpx.Recover(httpx.LimitBody(1 << 20)(mux)),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		slog.Info("listening", "addr", server.Addr)
		errCh <- server.ListenAndServe()
	}()

	sigCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	select {
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	case <-sigCtx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		return err
	}
	return nil
}

type status struct {
	Status string `json:"status" example:"ok"`
} //	@name	Status

// healthz godoc
//
//	@Summary	Process is running
//	@Tags		health
//	@Produce	json
//	@Success	200	{object}	status
//	@Router		/healthz [get]
func healthz(w http.ResponseWriter, _ *http.Request) {
	httpx.JSON(w, http.StatusOK, status{Status: "ok"})
}

// readyz godoc
//
//	@Summary	MongoDB and Supabase Auth are reachable
//	@Tags		health
//	@Produce	json
//	@Success	200	{object}	status
//	@Failure	503	{object}	httpx.ErrorEnvelope
//	@Router		/readyz [get]
func readyz(client *mongo.Client, verifier *auth.Verifier) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := client.Ping(r.Context(), nil); err != nil {
			httpx.Error(w, http.StatusServiceUnavailable, "internal", "mongodb unavailable", nil)
			return
		}
		if err := verifier.Ping(r.Context()); err != nil {
			httpx.Error(w, http.StatusServiceUnavailable, "internal", "supabase unavailable", nil)
			return
		}
		httpx.JSON(w, http.StatusOK, status{Status: "ok"})
	}
}
