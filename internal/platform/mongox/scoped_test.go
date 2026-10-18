package mongox

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/event"
	"go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/CypherZox/wedding-planner/backend-service/internal/platform/mongotest"
)

func TestScopedRepoCommandsIncludeWeddingID(t *testing.T) {
	var mu sync.Mutex
	var commands []recorded
	monitor := &event.CommandMonitor{
		Started: func(_ context.Context, evt *event.CommandStartedEvent) {
			if evt.CommandName == "endSessions" {
				return
			}
			value, err := evt.Command.LookupErr(evt.CommandName)
			if err != nil {
				return
			}
			name, ok := value.StringValueOK()
			if !ok || name != "scoped_probe" {
				return
			}
			mu.Lock()
			commands = append(commands, recorded{name: evt.CommandName, cmd: evt.Command})
			mu.Unlock()
		},
	}

	client, err := mongo.Connect(Options(mongotest.URI()).SetMonitor(monitor))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = client.Disconnect(context.Background()) })

	database := mongotest.Database(t, client)
	weddingID := bson.NewObjectID()
	otherWedding := bson.NewObjectID()
	repo := NewScoped(database.Collection("scoped_probe"), weddingID)
	ctx := context.Background()
	docID := bson.NewObjectID()

	if _, err := repo.InsertOne(ctx, bson.D{
		{Key: "_id", Value: docID},
		{Key: "weddingId", Value: otherWedding},
		{Key: "version", Value: int32(1)},
		{Key: "name", Value: "Ada"},
	}); err != nil {
		t.Fatalf("insert: %v", err)
	}

	var stored struct {
		WeddingID bson.ObjectID `bson:"weddingId"`
		Name      string        `bson:"name"`
	}
	if err := repo.FindOne(ctx, bson.D{{Key: "_id", Value: docID}}, &stored); err != nil {
		t.Fatalf("find one: %v", err)
	}
	if stored.WeddingID != weddingID || stored.Name != "Ada" {
		t.Fatalf("stored %+v, wedding %s", stored, weddingID.Hex())
	}

	cursor, err := repo.Find(ctx, bson.D{})
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if err := cursor.Close(ctx); err != nil {
		t.Fatalf("close: %v", err)
	}
	if _, err := repo.UpdateOne(ctx, bson.D{{Key: "_id", Value: docID}}, bson.D{
		{Key: "$set", Value: bson.D{{Key: "name", Value: "Ann"}}},
	}); err != nil {
		t.Fatalf("update: %v", err)
	}
	if _, err := repo.CountDocuments(ctx, nil); err != nil {
		t.Fatalf("count: %v", err)
	}
	agg, err := repo.Aggregate(ctx, mongo.Pipeline{
		bson.D{{Key: "$project", Value: bson.D{{Key: "name", Value: 1}}}},
	})
	if err != nil {
		t.Fatalf("aggregate: %v", err)
	}
	if err := agg.Close(ctx); err != nil {
		t.Fatalf("close aggregate: %v", err)
	}
	if err := repo.UpdateIfVersion(ctx, docID, 1, bson.D{{Key: "name", Value: "Bea"}}); err != nil {
		t.Fatalf("versioned update: %v", err)
	}
	if err := repo.UpdateIfVersion(ctx, docID, 1, bson.D{{Key: "name", Value: "nope"}}); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("stale version error = %v", err)
	}
	if err := repo.UpdateIfVersion(ctx, bson.NewObjectID(), 1, bson.D{{Key: "name", Value: "missing"}}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing doc error = %v", err)
	}
	if err := repo.SoftDelete(ctx, docID, 2, bson.NewObjectID(), time.Now().UTC()); err != nil {
		t.Fatalf("soft delete: %v", err)
	}
	if err := repo.FindOne(ctx, bson.D{{Key: "_id", Value: docID}}, &stored); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted doc lookup = %v", err)
	}

	seen := map[string]int{}
	for _, command := range commands {
		assertWeddingID(t, command, weddingID)
		seen[command.name]++
	}
	for _, name := range []string{"insert", "find", "update", "aggregate"} {
		if seen[name] == 0 {
			t.Fatalf("no %s command observed; saw %v", name, seen)
		}
	}
}

type recorded struct {
	name string
	cmd  bson.Raw
}

func assertWeddingID(t *testing.T, command recorded, weddingID bson.ObjectID) {
	t.Helper()
	paths := map[string][]string{
		"insert":    {"documents", "0", "weddingId"},
		"find":      {"filter", "weddingId"},
		"update":    {"updates", "0", "q", "weddingId"},
		"aggregate": {"pipeline", "0", "$match", "weddingId"},
	}
	path, ok := paths[command.name]
	if !ok {
		t.Fatalf("unexpected command %s: %s", command.name, command.cmd.String())
	}
	raw, err := command.cmd.LookupErr(path...)
	if err != nil {
		t.Fatalf("%s missing weddingId at %v: %s", command.name, path, command.cmd.String())
	}
	got, ok := raw.ObjectIDOK()
	if !ok || got != weddingID {
		t.Fatalf("%s weddingId = %v, want %s; command %s", command.name, raw, weddingID.Hex(), command.cmd.String())
	}
}
