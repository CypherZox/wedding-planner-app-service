package user

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/CypherZox/wedding-planner/backend-service/internal/platform/mongox"
)

// Repo stores users.
type Repo struct {
	coll *mongo.Collection
}

// NewRepo uses the users collection in database.
func NewRepo(database *mongo.Database) *Repo {
	return &Repo{coll: database.Collection("users")}
}

// FindByAuthUID returns the user, including a soft-deleted one.
func (r *Repo) FindByAuthUID(ctx context.Context, authUID string) (User, error) {
	return r.find(ctx, bson.D{{Key: "authUid", Value: authUID}})
}

// FindActive returns the user when the account has not been offboarded.
func (r *Repo) FindActive(ctx context.Context, authUID string) (User, error) {
	return r.find(ctx, activeFilter(authUID))
}

// Insert creates a user. A unique authUid or email returns mongox.ErrDuplicate.
func (r *Repo) Insert(ctx context.Context, user User) error {
	_, err := r.coll.InsertOne(ctx, user)
	return mongox.MapError(err)
}

// SyncAuth updates the email fields and lastSeenAt of an active user.
func (r *Repo) SyncAuth(ctx context.Context, authUID, email string, emailVerified bool, now time.Time) error {
	_, err := r.coll.UpdateOne(ctx, activeFilter(authUID), bson.D{{Key: "$set", Value: bson.D{
		{Key: "email", Value: email},
		{Key: "emailVerified", Value: emailVerified},
		{Key: "lastSeenAt", Value: now},
		{Key: "updatedAt", Value: now},
	}}})
	return mongox.MapError(err)
}

// Restore clears deletedAt and writes the profile from a new onboarding.
func (r *Repo) Restore(ctx context.Context, authUID string, user User) error {
	filter := bson.D{
		{Key: "authUid", Value: authUID},
		{Key: "deletedAt", Value: bson.D{{Key: "$exists", Value: true}}},
	}
	result, err := r.coll.UpdateOne(ctx, filter, bson.D{
		{Key: "$set", Value: bson.D{
			{Key: "email", Value: user.Email},
			{Key: "emailVerified", Value: user.EmailVerified},
			{Key: "displayName", Value: user.DisplayName},
			{Key: "locale", Value: user.Locale},
			{Key: "lastSeenAt", Value: user.LastSeenAt},
			{Key: "updatedAt", Value: user.UpdatedAt},
		}},
		{Key: "$unset", Value: bson.D{{Key: "deletedAt", Value: ""}}},
	})
	if err != nil {
		return mongox.MapError(err)
	}
	if result.MatchedCount == 0 {
		return mongox.ErrNotFound
	}
	return nil
}

// TouchLastSeen records that an active user called the API.
func (r *Repo) TouchLastSeen(ctx context.Context, authUID string, now time.Time) error {
	result, err := r.coll.UpdateOne(ctx, activeFilter(authUID), bson.D{{Key: "$set", Value: bson.D{
		{Key: "lastSeenAt", Value: now},
	}}})
	if err != nil {
		return mongox.MapError(err)
	}
	if result.MatchedCount == 0 {
		return mongox.ErrNotFound
	}
	return nil
}

// UpdateProfile writes the whitelisted profile fields of an active user.
func (r *Repo) UpdateProfile(ctx context.Context, authUID string, set bson.D) error {
	result, err := r.coll.UpdateOne(ctx, activeFilter(authUID), bson.D{{Key: "$set", Value: set}})
	if err != nil {
		return mongox.MapError(err)
	}
	if result.MatchedCount == 0 {
		return mongox.ErrNotFound
	}
	return nil
}

// SoftDelete marks an active user offboarded.
func (r *Repo) SoftDelete(ctx context.Context, authUID string, now time.Time) error {
	result, err := r.coll.UpdateOne(ctx, activeFilter(authUID), bson.D{{Key: "$set", Value: bson.D{
		{Key: "deletedAt", Value: now},
		{Key: "updatedAt", Value: now},
	}}})
	if err != nil {
		return mongox.MapError(err)
	}
	if result.MatchedCount == 0 {
		return mongox.ErrNotFound
	}
	return nil
}

func (r *Repo) find(ctx context.Context, filter bson.D) (User, error) {
	var user User
	err := r.coll.FindOne(ctx, filter).Decode(&user)
	if err != nil {
		return User{}, mongox.MapError(err)
	}
	return user, nil
}

func activeFilter(authUID string) bson.D {
	return bson.D{
		{Key: "authUid", Value: authUID},
		{Key: "deletedAt", Value: bson.D{{Key: "$exists", Value: false}}},
	}
}
