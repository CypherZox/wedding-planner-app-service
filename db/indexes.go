package db

import (
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

const deletedTTLSeconds int32 = 2592000

// CollectionIndexes is one collection's index set.
type CollectionIndexes struct {
	Collection string
	Models     []mongo.IndexModel
}

// Indexes returns every v1 index. Names are stable so CreateMany is idempotent.
func Indexes() []CollectionIndexes {
	return []CollectionIndexes{
		{Collection: "users", Models: []mongo.IndexModel{
			index("authUid_1", bson.D{{Key: "authUid", Value: int32(1)}}, options.Index().SetUnique(true)),
			index("email_1", bson.D{{Key: "email", Value: int32(1)}}, options.Index().SetUnique(true)),
		}},
		{Collection: "weddings", Models: []mongo.IndexModel{
			index("members.userId_1", bson.D{{Key: "members.userId", Value: int32(1)}}, nil),
		}},
		{Collection: "invites", Models: child(
			index("tokenHash_1", bson.D{{Key: "tokenHash", Value: int32(1)}}, options.Index().SetUnique(true)),
			index("expiresAt_1", bson.D{{Key: "expiresAt", Value: int32(1)}}, options.Index().SetExpireAfterSeconds(0)),
			index("weddingId_1_email_1", bson.D{{Key: "weddingId", Value: int32(1)}, {Key: "email", Value: int32(1)}}, options.Index().SetUnique(true)),
		)},
		{Collection: "households", Models: child(
			index("rsvpToken.hash_1", bson.D{{Key: "rsvpToken.hash", Value: int32(1)}}, options.Index().SetUnique(true).SetPartialFilterExpression(bson.D{
				{Key: "rsvpToken.hash", Value: bson.D{{Key: "$exists", Value: true}}},
			})),
		)},
		{Collection: "guests", Models: child(
			index("weddingId_1_sortName_1", bson.D{{Key: "weddingId", Value: int32(1)}, {Key: "sortName", Value: int32(1)}}, nil),
			index("weddingId_1_householdId_1", bson.D{{Key: "weddingId", Value: int32(1)}, {Key: "householdId", Value: int32(1)}}, nil),
			index("weddingId_1_seat.tableId_1_seat.seatNo_1", bson.D{
				{Key: "weddingId", Value: int32(1)},
				{Key: "seat.tableId", Value: int32(1)},
				{Key: "seat.seatNo", Value: int32(1)},
			}, options.Index().SetUnique(true).SetPartialFilterExpression(bson.D{
				{Key: "seat.tableId", Value: bson.D{{Key: "$exists", Value: true}}},
			})),
		)},
		{Collection: "seating_tables", Models: child(
			index("weddingId_1_order_1", bson.D{{Key: "weddingId", Value: int32(1)}, {Key: "order", Value: int32(1)}}, nil),
		)},
		{Collection: "tasks", Models: child(
			index("weddingId_1_done_1_dueDate_1", bson.D{
				{Key: "weddingId", Value: int32(1)},
				{Key: "done", Value: int32(1)},
				{Key: "dueDate", Value: int32(1)},
			}, nil),
			index("weddingId_1_phase_1_order_1", bson.D{
				{Key: "weddingId", Value: int32(1)},
				{Key: "phase", Value: int32(1)},
				{Key: "order", Value: int32(1)},
			}, nil),
		)},
		{Collection: "vendors", Models: child(
			index("weddingId_1_categoryId_1_isSelected_-1", bson.D{
				{Key: "weddingId", Value: int32(1)},
				{Key: "categoryId", Value: int32(1)},
				{Key: "isSelected", Value: int32(-1)},
			}, nil),
		)},
		{Collection: "payments", Models: child(
			index("weddingId_1_status_1_dueDate_1", bson.D{
				{Key: "weddingId", Value: int32(1)},
				{Key: "status", Value: int32(1)},
				{Key: "dueDate", Value: int32(1)},
			}, nil),
			index("weddingId_1_vendorId_1", bson.D{{Key: "weddingId", Value: int32(1)}, {Key: "vendorId", Value: int32(1)}}, nil),
		)},
		{Collection: "schedules", Models: child(
			index("weddingId_1_scope_1_date_1", bson.D{
				{Key: "weddingId", Value: int32(1)},
				{Key: "scope", Value: int32(1)},
				{Key: "date", Value: int32(1)},
			}, nil),
		)},
		{Collection: "media", Models: child(
			index("weddingId_1_owner.type_1_owner.id_1_order_1", bson.D{
				{Key: "weddingId", Value: int32(1)},
				{Key: "owner.type", Value: int32(1)},
				{Key: "owner.id", Value: int32(1)},
				{Key: "order", Value: int32(1)},
			}, nil),
			index("createdAt_1", bson.D{{Key: "createdAt", Value: int32(1)}}, options.Index().SetExpireAfterSeconds(86400).SetPartialFilterExpression(bson.D{
				{Key: "status", Value: "pending"},
			})),
		)},
	}
}

func child(extra ...mongo.IndexModel) []mongo.IndexModel {
	base := []mongo.IndexModel{
		index("weddingId_1_updatedAt_1", bson.D{{Key: "weddingId", Value: int32(1)}, {Key: "updatedAt", Value: int32(1)}}, nil),
		index("deletedAt_1", bson.D{{Key: "deletedAt", Value: int32(1)}}, options.Index().SetExpireAfterSeconds(deletedTTLSeconds)),
	}
	return append(base, extra...)
}

func index(name string, keys bson.D, opts *options.IndexOptionsBuilder) mongo.IndexModel {
	if opts == nil {
		opts = options.Index()
	}
	return mongo.IndexModel{Keys: keys, Options: opts.SetName(name)}
}
