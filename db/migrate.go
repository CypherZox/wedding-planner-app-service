package db

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

//go:embed schemas/*.json
var schemaFS embed.FS

// Migration is one numbered data migration.
type Migration struct {
	ID string
	Up func(ctx context.Context, database *mongo.Database) error
}

var migrations = []Migration{
	{ID: "0001_init", Up: func(context.Context, *mongo.Database) error { return nil }},
}

// Apply creates validators, indexes, and any unrecorded data migrations.
// It is safe to run on every deploy.
func Apply(ctx context.Context, database *mongo.Database) error {
	if err := applyValidators(ctx, database); err != nil {
		return err
	}
	if err := applyIndexes(ctx, database); err != nil {
		return err
	}
	if err := applyMigrations(ctx, database); err != nil {
		return err
	}
	return nil
}

func applyValidators(ctx context.Context, database *mongo.Database) error {
	for _, name := range Collections {
		schema, err := LoadSchema(name)
		if err != nil {
			return err
		}
		validator := bson.D{{Key: "$jsonSchema", Value: schema}}
		exists, err := collectionExists(ctx, database, name)
		if err != nil {
			return err
		}
		if !exists {
			err = database.CreateCollection(ctx, name, options.CreateCollection().
				SetValidator(validator).
				SetValidationAction("error").
				SetValidationLevel("strict"))
			if err != nil {
				return fmt.Errorf("create %s: %w", name, err)
			}
			continue
		}
		err = database.RunCommand(ctx, bson.D{
			{Key: "collMod", Value: name},
			{Key: "validator", Value: validator},
			{Key: "validationLevel", Value: "strict"},
			{Key: "validationAction", Value: "error"},
		}).Err()
		if err != nil {
			return fmt.Errorf("collMod %s: %w", name, err)
		}
	}
	return nil
}

func applyIndexes(ctx context.Context, database *mongo.Database) error {
	for _, spec := range Indexes() {
		if _, err := database.Collection(spec.Collection).Indexes().CreateMany(ctx, spec.Models); err != nil {
			return fmt.Errorf("create indexes %s: %w", spec.Collection, err)
		}
	}
	return nil
}

func applyMigrations(ctx context.Context, database *mongo.Database) error {
	coll := database.Collection(migrationsCollection)
	for _, migration := range migrations {
		err := coll.FindOne(ctx, bson.D{{Key: "_id", Value: migration.ID}}).Err()
		if err == nil {
			continue
		}
		if !errors.Is(err, mongo.ErrNoDocuments) {
			return fmt.Errorf("lookup migration %s: %w", migration.ID, err)
		}
		if err := migration.Up(ctx, database); err != nil {
			return fmt.Errorf("run migration %s: %w", migration.ID, err)
		}
		_, err = coll.InsertOne(ctx, bson.D{
			{Key: "_id", Value: migration.ID},
			{Key: "appliedAt", Value: time.Now().UTC()},
		})
		if err != nil && !mongo.IsDuplicateKeyError(err) {
			return fmt.Errorf("record migration %s: %w", migration.ID, err)
		}
	}
	return nil
}

func collectionExists(ctx context.Context, database *mongo.Database, name string) (bool, error) {
	names, err := database.ListCollectionNames(ctx, bson.D{{Key: "name", Value: name}})
	if err != nil {
		return false, fmt.Errorf("list collections: %w", err)
	}
	return len(names) > 0, nil
}

// LoadSchema reads the $jsonSchema document for a collection.
func LoadSchema(collection string) (bson.M, error) {
	raw, err := schemaFS.ReadFile("schemas/" + collection + ".json")
	if err != nil {
		return nil, fmt.Errorf("read schema %s: %w", collection, err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var decoded any
	if err := dec.Decode(&decoded); err != nil {
		return nil, fmt.Errorf("decode schema %s: %w", collection, err)
	}
	schema, ok := normalize(decoded).(bson.M)
	if !ok {
		return nil, fmt.Errorf("schema %s must be a JSON object", collection)
	}
	return schema, nil
}

func normalize(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		doc := make(bson.M, len(typed))
		for key, child := range typed {
			doc[key] = normalize(child)
		}
		return doc
	case []any:
		items := make(bson.A, len(typed))
		for i, child := range typed {
			items[i] = normalize(child)
		}
		return items
	case json.Number:
		if integer, err := typed.Int64(); err == nil {
			if integer >= math.MinInt32 && integer <= math.MaxInt32 {
				return int32(integer)
			}
			return integer
		}
		float, err := typed.Float64()
		if err != nil {
			return typed.String()
		}
		return float
	default:
		return value
	}
}
