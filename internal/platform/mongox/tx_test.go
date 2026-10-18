package mongox

import (
	"context"
	"errors"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/CypherZox/wedding-planner/backend-service/internal/platform/mongotest"
)

func TestWithTxCommitsAndRollsBack(t *testing.T) {
	client, err := Connect(mongotest.URI())
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() {
		_ = client.Disconnect(context.Background())
	})

	database := mongotest.Database(t, client)
	coll := database.Collection("tx_probe")
	ctx := context.Background()

	err = WithTx(ctx, client, func(ctx context.Context) error {
		_, err := coll.InsertOne(ctx, bson.D{{Key: "_id", Value: "kept"}})
		return err
	})
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	if err := coll.FindOne(ctx, bson.D{{Key: "_id", Value: "kept"}}).Err(); err != nil {
		t.Fatalf("committed doc missing: %v", err)
	}

	err = WithTx(ctx, client, func(ctx context.Context) error {
		if _, err := coll.InsertOne(ctx, bson.D{{Key: "_id", Value: "dropped"}}); err != nil {
			return err
		}
		return errors.New("rollback")
	})
	if err == nil {
		t.Fatal("expected rollback error")
	}
	err = coll.FindOne(ctx, bson.D{{Key: "_id", Value: "dropped"}}).Err()
	if !errors.Is(err, mongo.ErrNoDocuments) {
		t.Fatalf("rolled-back doc lookup = %v", err)
	}
}
