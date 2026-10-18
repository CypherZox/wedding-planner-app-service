package db

import (
	"context"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/CypherZox/wedding-planner/backend-service/internal/platform/mongotest"
	"github.com/CypherZox/wedding-planner/backend-service/internal/platform/mongox"
)

func TestApplyIsIdempotent(t *testing.T) {
	client := connect(t)
	database := mongotest.Database(t, client)
	ctx := context.Background()

	if err := Apply(ctx, database); err != nil {
		t.Fatalf("first apply: %v", err)
	}
	if err := Apply(ctx, database); err != nil {
		t.Fatalf("second apply: %v", err)
	}

	n, err := database.Collection(migrationsCollection).CountDocuments(ctx, bson.D{{Key: "_id", Value: "0001_init"}})
	if err != nil {
		t.Fatalf("count migrations: %v", err)
	}
	if n != 1 {
		t.Fatalf("migration records = %d, want 1", n)
	}

	assertIndex := func(collection, name string, expire *int32, unique bool, partial string) {
		t.Helper()
		doc := indexDocument(t, ctx, database.Collection(collection), name)
		if expire != nil && number(doc["expireAfterSeconds"]) != int64(*expire) {
			t.Fatalf("%s.%s expireAfterSeconds = %v, want %d", collection, name, doc["expireAfterSeconds"], *expire)
		}
		if unique && doc["unique"] != true {
			t.Fatalf("%s.%s unique = %v", collection, name, doc["unique"])
		}
		if partial != "" && !documentHasKey(doc["partialFilterExpression"], partial) {
			t.Fatalf("%s.%s partial = %#v, want key %s", collection, name, doc["partialFilterExpression"], partial)
		}
	}

	deleted := deletedTTLSeconds
	zero := int32(0)
	day := int32(86400)
	assertIndex("guests", "deletedAt_1", &deleted, false, "")
	assertIndex("guests", "weddingId_1_seat.tableId_1_seat.seatNo_1", nil, true, "seat.tableId")
	assertIndex("households", "rsvpToken.hash_1", nil, true, "rsvpToken.hash")
	assertIndex("invites", "expiresAt_1", &zero, false, "")
	assertIndex("invites", "tokenHash_1", nil, true, "")
	assertIndex("users", "email_1", nil, true, "")
	assertIndex("media", "createdAt_1", &day, false, "status")
	assertIndex("weddings", "members.userId_1", nil, false, "")
}

func connect(t *testing.T) *mongo.Client {
	t.Helper()
	client, err := mongox.Connect(mongotest.URI())
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = client.Disconnect(context.Background()) })
	return client
}

func indexDocument(t *testing.T, ctx context.Context, coll *mongo.Collection, name string) bson.M {
	t.Helper()
	cursor, err := coll.Indexes().List(ctx)
	if err != nil {
		t.Fatalf("list indexes: %v", err)
	}
	var docs []bson.M
	if err := cursor.All(ctx, &docs); err != nil {
		t.Fatalf("read indexes: %v", err)
	}
	for _, doc := range docs {
		if doc["name"] == name {
			return doc
		}
	}
	t.Fatalf("missing index %s in %v", name, docs)
	return nil
}

func documentHasKey(value any, key string) bool {
	switch doc := value.(type) {
	case bson.M:
		_, ok := doc[key]
		return ok
	case bson.D:
		for _, el := range doc {
			if el.Key == key {
				return true
			}
		}
	}
	return false
}

func number(value any) int64 {
	switch n := value.(type) {
	case int32:
		return int64(n)
	case int64:
		return n
	case int:
		return int64(n)
	default:
		return -1
	}
}
