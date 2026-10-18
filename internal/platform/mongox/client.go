package mongox

import (
	"context"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// Options builds the process-wide client options.
// Server API version 1 is non-strict: SetStrict is left at its default, false.
func Options(uri string) *options.ClientOptions {
	return options.Client().
		ApplyURI(uri).
		SetAppName("wedding-api").
		SetServerAPIOptions(options.ServerAPI(options.ServerAPIVersion1)).
		SetTimeout(5 * time.Second).
		SetMinPoolSize(5)
}

// Connect opens one client and pings it.
func Connect(uri string) (*mongo.Client, error) {
	client, err := mongo.Connect(Options(uri))
	if err != nil {
		return nil, fmt.Errorf("connect mongodb: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.Ping(ctx, nil); err != nil {
		_ = client.Disconnect(context.Background())
		return nil, fmt.Errorf("ping mongodb: %w", err)
	}
	return client, nil
}
