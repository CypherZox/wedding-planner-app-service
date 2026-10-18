package mongox

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// ScopedRepo is a collection bound to one wedding.
// Every filter and every insert includes that weddingId.
// Reads and versioned updates also exclude soft-deleted documents.
type ScopedRepo struct {
	coll      *mongo.Collection
	weddingID bson.ObjectID
}

// NewScoped binds a collection to a wedding.
func NewScoped(coll *mongo.Collection, weddingID bson.ObjectID) ScopedRepo {
	return ScopedRepo{coll: coll, weddingID: weddingID}
}

// WeddingID returns the tenant id this repository is bound to.
func (r ScopedRepo) WeddingID() bson.ObjectID {
	return r.weddingID
}

// ActiveFilter returns a filter scoped to this wedding, excluding soft-deleted documents.
func (r ScopedRepo) ActiveFilter(extra bson.D) bson.D {
	filter := bson.D{
		{Key: "weddingId", Value: r.weddingID},
		{Key: "deletedAt", Value: bson.D{{Key: "$exists", Value: false}}},
	}
	return append(filter, extra...)
}

// InsertOne inserts doc after forcing weddingId to this repository's wedding.
func (r ScopedRepo) InsertOne(ctx context.Context, doc any) (*mongo.InsertOneResult, error) {
	stamped, err := withWeddingID(doc, r.weddingID)
	if err != nil {
		return nil, fmt.Errorf("stamp weddingId: %w", err)
	}
	result, err := r.coll.InsertOne(ctx, stamped)
	if err != nil {
		return nil, fmt.Errorf("insert: %w", MapError(err))
	}
	return result, nil
}

// Find returns active documents matching extra.
func (r ScopedRepo) Find(ctx context.Context, extra bson.D, opts ...options.Lister[options.FindOptions]) (*mongo.Cursor, error) {
	cursor, err := r.coll.Find(ctx, r.ActiveFilter(extra), opts...)
	if err != nil {
		return nil, fmt.Errorf("find: %w", MapError(err))
	}
	return cursor, nil
}

// FindOne decodes one active document into out.
func (r ScopedRepo) FindOne(ctx context.Context, extra bson.D, out any) error {
	err := r.coll.FindOne(ctx, r.ActiveFilter(extra)).Decode(out)
	if err != nil {
		return fmt.Errorf("find one: %w", MapError(err))
	}
	return nil
}

// UpdateOne updates one active document. The filter is scoped before it reaches the driver.
func (r ScopedRepo) UpdateOne(ctx context.Context, extra bson.D, update any) (*mongo.UpdateResult, error) {
	result, err := r.coll.UpdateOne(ctx, r.ActiveFilter(extra), update)
	if err != nil {
		return nil, fmt.Errorf("update: %w", MapError(err))
	}
	return result, nil
}

// UpdateIfVersion updates an active document only when its version matches.
// A miss is ErrNotFound when no active document has that id, and ErrVersionConflict otherwise.
func (r ScopedRepo) UpdateIfVersion(ctx context.Context, id bson.ObjectID, version int32, set bson.D) error {
	filter := r.ActiveFilter(bson.D{
		{Key: "_id", Value: id},
		{Key: "version", Value: version},
	})
	update := bson.D{
		{Key: "$set", Value: set},
		{Key: "$inc", Value: bson.D{{Key: "version", Value: int32(1)}}},
		{Key: "$currentDate", Value: bson.D{{Key: "updatedAt", Value: true}}},
	}
	result, err := r.coll.UpdateOne(ctx, filter, update)
	if err != nil {
		return fmt.Errorf("versioned update: %w", MapError(err))
	}
	if result.MatchedCount == 0 {
		return r.miss(ctx, id)
	}
	return nil
}

// SoftDelete marks an active document deleted and bumps version and updatedAt.
func (r ScopedRepo) SoftDelete(ctx context.Context, id bson.ObjectID, version int32, userID bson.ObjectID, deletedAt time.Time) error {
	filter := r.ActiveFilter(bson.D{
		{Key: "_id", Value: id},
		{Key: "version", Value: version},
	})
	update := bson.D{
		{Key: "$set", Value: bson.D{
			{Key: "deletedAt", Value: deletedAt.UTC()},
			{Key: "updatedBy", Value: userID},
		}},
		{Key: "$inc", Value: bson.D{{Key: "version", Value: int32(1)}}},
		{Key: "$currentDate", Value: bson.D{{Key: "updatedAt", Value: true}}},
	}
	result, err := r.coll.UpdateOne(ctx, filter, update)
	if err != nil {
		return fmt.Errorf("soft delete: %w", MapError(err))
	}
	if result.MatchedCount == 0 {
		return r.miss(ctx, id)
	}
	return nil
}

// CountDocuments counts active documents matching extra.
func (r ScopedRepo) CountDocuments(ctx context.Context, extra bson.D) (int64, error) {
	n, err := r.coll.CountDocuments(ctx, r.ActiveFilter(extra))
	if err != nil {
		return 0, fmt.Errorf("count: %w", MapError(err))
	}
	return n, nil
}

// Aggregate runs pipeline after a $match that scopes the wedding and hides soft-deleted documents.
func (r ScopedRepo) Aggregate(ctx context.Context, pipeline mongo.Pipeline) (*mongo.Cursor, error) {
	scoped := make(mongo.Pipeline, 0, len(pipeline)+1)
	scoped = append(scoped, bson.D{{Key: "$match", Value: r.ActiveFilter(nil)}})
	scoped = append(scoped, pipeline...)
	cursor, err := r.coll.Aggregate(ctx, scoped)
	if err != nil {
		return nil, fmt.Errorf("aggregate: %w", MapError(err))
	}
	return cursor, nil
}

func (r ScopedRepo) miss(ctx context.Context, id bson.ObjectID) error {
	err := r.coll.FindOne(ctx, r.ActiveFilter(bson.D{{Key: "_id", Value: id}})).Err()
	if err == nil {
		return fmt.Errorf("versioned update: %w", ErrVersionConflict)
	}
	if errors.Is(MapError(err), ErrNotFound) {
		return fmt.Errorf("versioned update: %w", ErrNotFound)
	}
	return fmt.Errorf("versioned update lookup: %w", err)
}

func withWeddingID(doc any, weddingID bson.ObjectID) (bson.Raw, error) {
	raw, err := bson.Marshal(doc)
	if err != nil {
		return nil, err
	}
	var elements bson.D
	if err := bson.Unmarshal(raw, &elements); err != nil {
		return nil, err
	}
	replaced := false
	for i := range elements {
		if elements[i].Key == "weddingId" {
			elements[i].Value = weddingID
			replaced = true
			break
		}
	}
	if !replaced {
		elements = append(elements, bson.E{Key: "weddingId", Value: weddingID})
	}
	return bson.Marshal(bson.D(elements))
}
