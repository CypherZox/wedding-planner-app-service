package mongox

import (
	"errors"
	"testing"

	"go.mongodb.org/mongo-driver/v2/mongo"
)

func TestMapError(t *testing.T) {
	dup := mongo.WriteException{WriteErrors: mongo.WriteErrors{{Code: 11000, Message: "E11000 duplicate key"}}}
	if !errors.Is(MapError(dup), ErrDuplicate) {
		t.Fatalf("duplicate key mapped to %v", MapError(dup))
	}
	if !errors.Is(MapError(mongo.ErrNoDocuments), ErrNotFound) {
		t.Fatal("ErrNoDocuments was not mapped to ErrNotFound")
	}
	if err := MapError(errors.New("boom")); err.Error() != "boom" {
		t.Fatalf("MapError() = %v", err)
	}
}
