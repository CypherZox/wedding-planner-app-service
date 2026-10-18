package mongox

import (
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/mongo"
)

// WithTx runs fn inside a transaction. The callback must use the context it receives.
func WithTx(ctx context.Context, client *mongo.Client, fn func(ctx context.Context) error) error {
	session, err := client.StartSession()
	if err != nil {
		return fmt.Errorf("start session: %w", err)
	}
	defer session.EndSession(ctx)

	_, err = session.WithTransaction(ctx, func(ctx context.Context) (any, error) {
		if err := fn(ctx); err != nil {
			return nil, err
		}
		return nil, nil
	})
	if err != nil {
		return fmt.Errorf("transaction: %w", err)
	}
	return nil
}
