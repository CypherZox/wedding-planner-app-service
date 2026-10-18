package db

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/CypherZox/wedding-planner/backend-service/internal/platform/mongotest"
)

func TestValidatorsAcceptMinimalDocuments(t *testing.T) {
	client := connect(t)
	database := mongotest.Database(t, client)
	ctx := context.Background()
	if err := Apply(ctx, database); err != nil {
		t.Fatalf("apply: %v", err)
	}

	now := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	userID := bson.NewObjectID()
	weddingID := bson.NewObjectID()
	householdID := bson.NewObjectID()
	vendorID := bson.NewObjectID()
	mediaID := bson.NewObjectID()

	docs := []struct {
		collection string
		doc        bson.D
	}{
		{"users", userDoc(userID, now)},
		{"weddings", weddingDoc(weddingID, userID, now)},
		{"invites", inviteDoc(weddingID, userID, now)},
		{"households", append(base(bson.NewObjectID(), weddingID, userID, now),
			bson.E{Key: "name", Value: "Parents"},
			bson.E{Key: "plusOnesAllowed", Value: int32(2)},
		)},
		{"guests", guestDoc(weddingID, userID, householdID, now, "Ada", "adult", bson.A{})},
		{"seating_tables", append(base(bson.NewObjectID(), weddingID, userID, now),
			bson.E{Key: "name", Value: "Table 1"},
			bson.E{Key: "number", Value: int32(1)},
			bson.E{Key: "kind", Value: "round"},
			bson.E{Key: "capacity", Value: int32(10)},
			bson.E{Key: "order", Value: int32(0)},
		)},
		{"tasks", append(base(bson.NewObjectID(), weddingID, userID, now),
			bson.E{Key: "kind", Value: "checklist"},
			bson.E{Key: "phase", Value: "m12_plus"},
			bson.E{Key: "title", Value: "Book the venue"},
			bson.E{Key: "priority", Value: int32(3)},
			bson.E{Key: "done", Value: false},
			bson.E{Key: "assigneeIds", Value: bson.A{}},
			bson.E{Key: "order", Value: float64(1)},
		)},
		{"vendors", vendorDoc(weddingID, userID, now, "vendor", false)},
		{"vendors", vendorDoc(weddingID, userID, now, "venue", true)},
		{"payments", paymentDoc(weddingID, userID, now, "expense", "paid", bson.NilObjectID)},
		{"payments", paymentDoc(weddingID, userID, now, "installment", "scheduled", vendorID)},
		{"schedules", append(base(bson.NewObjectID(), weddingID, userID, now),
			bson.E{Key: "scope", Value: "wedding"},
			bson.E{Key: "title", Value: "Wedding day"},
			bson.E{Key: "date", Value: "2027-06-01"},
			bson.E{Key: "order", Value: int32(0)},
			bson.E{Key: "items", Value: bson.A{}},
		)},
		{"media", append(base(mediaID, weddingID, userID, now),
			bson.E{Key: "owner", Value: bson.D{{Key: "type", Value: "vendor"}, {Key: "id", Value: vendorID}}},
			bson.E{Key: "storageKey", Value: fmt.Sprintf("w/%s/%s.jpg", weddingID.Hex(), mediaID.Hex())},
			bson.E{Key: "mime", Value: "image/jpeg"},
			bson.E{Key: "bytes", Value: int64(128)},
			bson.E{Key: "order", Value: int32(0)},
			bson.E{Key: "status", Value: "pending"},
			bson.E{Key: "uploadedBy", Value: userID},
		)},
	}

	for _, doc := range docs {
		if _, err := database.Collection(doc.collection).InsertOne(ctx, doc.doc); err != nil {
			t.Fatalf("insert %s: %v", doc.collection, err)
		}
	}
}

func TestValidatorRejectsRuleCategories(t *testing.T) {
	client := connect(t)
	database := mongotest.Database(t, client)
	ctx := context.Background()
	if err := Apply(ctx, database); err != nil {
		t.Fatalf("apply: %v", err)
	}

	now := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	userID := bson.NewObjectID()
	weddingID := bson.NewObjectID()
	householdID := bson.NewObjectID()

	tags := bson.A{}
	for i := 0; i < 11; i++ {
		tags = append(tags, "aaaaaaaa")
	}
	longName := strings.Repeat("a", 201)

	cases := []struct {
		name       string
		collection string
		doc        bson.D
	}{
		{"missing required", "guests", without(guestDoc(weddingID, userID, householdID, now, "Ada", "adult", bson.A{}), "firstName")},
		{"wrong bson type", "guests", with(guestDoc(weddingID, userID, householdID, now, "Ada", "adult", bson.A{}), "version", "1")},
		{"bad enum", "guests", guestDoc(weddingID, userID, householdID, now, "Ada", "toddler", bson.A{})},
		{"max length", "guests", guestDoc(weddingID, userID, householdID, now, longName, "adult", bson.A{})},
		{"max items", "guests", guestDoc(weddingID, userID, householdID, now, "Ada", "adult", tags)},
		{"additional property", "guests", append(guestDoc(weddingID, userID, householdID, now, "Ada", "adult", bson.A{}), bson.E{Key: "nope", Value: true})},
		{"venue required", "vendors", vendorDoc(weddingID, userID, now, "venue", false)},
		{"venue forbidden", "vendors", vendorDoc(weddingID, userID, now, "vendor", true)},
		{"installment needs vendor", "payments", paymentDoc(weddingID, userID, now, "installment", "scheduled", bson.NilObjectID)},
		{"expense forbids vendor", "payments", paymentDoc(weddingID, userID, now, "expense", "paid", bson.NewObjectID())},
		{"scheduled needs due date", "payments", without(paymentDoc(weddingID, userID, now, "expense", "scheduled", bson.NilObjectID), "dueDate")},
	}

	scheduled := without(paymentDoc(weddingID, userID, now, "expense", "paid", bson.NilObjectID), "paidOn")
	scheduled = with(scheduled, "status", "scheduled")
	cases[10].doc = scheduled

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := database.Collection(tc.collection).InsertOne(ctx, tc.doc)
			if !validationFailed(err) {
				t.Fatalf("insert error = %v, want document validation failure", err)
			}
		})
	}
}

func validationFailed(err error) bool {
	var server mongo.ServerError
	return err != nil && errors.As(err, &server) && server.HasErrorCode(121)
}

func base(id, weddingID, userID bson.ObjectID, now time.Time) bson.D {
	return bson.D{
		{Key: "_id", Value: id},
		{Key: "weddingId", Value: weddingID},
		{Key: "sv", Value: int32(1)},
		{Key: "version", Value: int32(1)},
		{Key: "createdAt", Value: now},
		{Key: "updatedAt", Value: now},
		{Key: "createdBy", Value: userID},
		{Key: "updatedBy", Value: userID},
	}
}

func userDoc(id bson.ObjectID, now time.Time) bson.D {
	return bson.D{
		{Key: "_id", Value: id},
		{Key: "authProvider", Value: "supabase"},
		{Key: "authUid", Value: "supabase-user-id"},
		{Key: "email", Value: "ada@example.com"},
		{Key: "emailVerified", Value: true},
		{Key: "displayName", Value: "Ada"},
		{Key: "locale", Value: "en"},
		{Key: "lastSeenAt", Value: now},
		{Key: "createdAt", Value: now},
		{Key: "updatedAt", Value: now},
	}
}

func weddingDoc(id, userID bson.ObjectID, now time.Time) bson.D {
	return bson.D{
		{Key: "_id", Value: id},
		{Key: "sv", Value: int32(1)},
		{Key: "version", Value: int32(1)},
		{Key: "createdAt", Value: now},
		{Key: "updatedAt", Value: now},
		{Key: "createdBy", Value: userID},
		{Key: "updatedBy", Value: userID},
		{Key: "title", Value: "Ada and Grace"},
		{Key: "partners", Value: bson.A{
			bson.D{{Key: "key", Value: "p1"}, {Key: "firstName", Value: "Ada"}, {Key: "lastName", Value: "Lovelace"}, {Key: "label", Value: "bride"}},
			bson.D{{Key: "key", Value: "p2"}, {Key: "firstName", Value: "Grace"}, {Key: "lastName", Value: "Hopper"}, {Key: "label", Value: "groom"}},
		}},
		{Key: "date", Value: "2027-06-01"},
		{Key: "timezone", Value: "Asia/Dubai"},
		{Key: "location", Value: bson.D{{Key: "name", Value: "The Venue"}, {Key: "city", Value: "Dubai"}, {Key: "country", Value: "AE"}}},
		{Key: "currency", Value: "AED"},
		{Key: "currencyDecimals", Value: int32(2)},
		{Key: "status", Value: "planning"},
		{Key: "members", Value: bson.A{bson.D{
			{Key: "userId", Value: userID},
			{Key: "role", Value: "owner"},
			{Key: "partnerKey", Value: nil},
			{Key: "canViewBudget", Value: true},
			{Key: "displayName", Value: "Ada"},
			{Key: "joinedAt", Value: now},
		}}},
		{Key: "lists", Value: bson.D{
			{Key: "guestTags", Value: bson.A{}},
			{Key: "accommodations", Value: bson.A{}},
			{Key: "partyRoles", Value: bson.A{}},
			{Key: "musicCategories", Value: bson.A{}},
			{Key: "mealChoices", Value: bson.A{}},
			{Key: "vendorCategories", Value: bson.A{}},
		}},
		{Key: "budget", Value: bson.D{
			{Key: "contributors", Value: bson.A{}},
			{Key: "categories", Value: bson.A{}},
		}},
		{Key: "rsvp", Value: bson.D{
			{Key: "enabled", Value: false},
			{Key: "askMeal", Value: false},
			{Key: "askDietary", Value: false},
			{Key: "allowPlusOnes", Value: false},
		}},
	}
}

func inviteDoc(weddingID, userID bson.ObjectID, now time.Time) bson.D {
	return append(base(bson.NewObjectID(), weddingID, userID, now),
		bson.E{Key: "email", Value: "guest@example.com"},
		bson.E{Key: "role", Value: "editor"},
		bson.E{Key: "canViewBudget", Value: false},
		bson.E{Key: "tokenHash", Value: bytes32()},
		bson.E{Key: "invitedBy", Value: userID},
		bson.E{Key: "expiresAt", Value: now.Add(14 * 24 * time.Hour)},
	)
}

func guestDoc(weddingID, userID, householdID bson.ObjectID, now time.Time, firstName, ageGroup string, tags bson.A) bson.D {
	return append(base(bson.NewObjectID(), weddingID, userID, now),
		bson.E{Key: "householdId", Value: householdID},
		bson.E{Key: "firstName", Value: firstName},
		bson.E{Key: "lastName", Value: "Lovelace"},
		bson.E{Key: "sortName", Value: "lovelace " + strings.ToLower(firstName)},
		bson.E{Key: "isHouseholdContact", Value: true},
		bson.E{Key: "ageGroup", Value: ageGroup},
		bson.E{Key: "isPlusOne", Value: false},
		bson.E{Key: "tagIds", Value: tags},
		bson.E{Key: "saveTheDate", Value: bson.D{{Key: "sent", Value: false}}},
		bson.E{Key: "invitation", Value: bson.D{{Key: "sent", Value: false}}},
		bson.E{Key: "rsvp", Value: bson.D{{Key: "status", Value: "pending"}}},
		bson.E{Key: "giftReceived", Value: false},
	)
}

func vendorDoc(weddingID, userID bson.ObjectID, now time.Time, kind string, withVenue bool) bson.D {
	doc := append(base(bson.NewObjectID(), weddingID, userID, now),
		bson.E{Key: "type", Value: kind},
		bson.E{Key: "categoryId", Value: "venuecat"},
		bson.E{Key: "name", Value: "Hall"},
		bson.E{Key: "priceMinor", Value: int64(100)},
		bson.E{Key: "status", Value: "considering"},
		bson.E{Key: "isSelected", Value: false},
	)
	if withVenue {
		doc = append(doc, bson.E{Key: "venue", Value: bson.D{
			{Key: "costs", Value: bson.A{}},
			{Key: "pros", Value: bson.A{}},
			{Key: "cons", Value: bson.A{}},
		}})
	}
	return doc
}

func paymentDoc(weddingID, userID bson.ObjectID, now time.Time, kind, status string, vendorID bson.ObjectID) bson.D {
	doc := append(base(bson.NewObjectID(), weddingID, userID, now),
		bson.E{Key: "scope", Value: "wedding"},
		bson.E{Key: "kind", Value: kind},
		bson.E{Key: "categoryId", Value: "budget01"},
		bson.E{Key: "description", Value: "Deposit"},
		bson.E{Key: "amountMinor", Value: int64(2500)},
		bson.E{Key: "status", Value: status},
	)
	if vendorID != bson.NilObjectID {
		doc = append(doc, bson.E{Key: "vendorId", Value: vendorID})
	}
	if status == "paid" {
		doc = append(doc, bson.E{Key: "paidOn", Value: "2026-10-01"})
	}
	if status == "scheduled" {
		doc = append(doc, bson.E{Key: "dueDate", Value: "2026-11-01"})
	}
	return doc
}

func with(doc bson.D, key string, value any) bson.D {
	for i := range doc {
		if doc[i].Key == key {
			doc[i].Value = value
			return doc
		}
	}
	return append(doc, bson.E{Key: key, Value: value})
}

func without(doc bson.D, key string) bson.D {
	out := make(bson.D, 0, len(doc))
	for _, el := range doc {
		if el.Key != key {
			out = append(out, el)
		}
	}
	return out
}

func bytes32() []byte {
	return []byte("0123456789abcdef0123456789abcdef")
}
