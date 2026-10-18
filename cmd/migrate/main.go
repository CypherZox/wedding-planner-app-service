package main

import (
	"context"
	"log/slog"
	"os"
	"time"

	"github.com/CypherZox/wedding-planner/backend-service/db"
	"github.com/CypherZox/wedding-planner/backend-service/internal/platform/config"
	"github.com/CypherZox/wedding-planner/backend-service/internal/platform/mongox"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	if err := run(); err != nil {
		slog.Error("migrate failed", "err", err)
		os.Exit(1)
	}
	slog.Info("migrate complete")
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
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

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	return db.Apply(ctx, client.Database(cfg.MongoDB))
}
