package mongox

import (
	"errors"

	"go.mongodb.org/mongo-driver/v2/mongo"
)

var (
	// ErrNotFound is a missing document.
	ErrNotFound = errors.New("not found")
	// ErrDuplicate is a unique-index collision.
	ErrDuplicate = errors.New("duplicate")
	// ErrVersionConflict is an update whose version filter matched nothing while the document still exists.
	ErrVersionConflict = errors.New("version conflict")
)

// MapError converts driver errors into sentinel errors.
func MapError(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, mongo.ErrNoDocuments) || errors.Is(err, ErrNotFound):
		return ErrNotFound
	case errors.Is(err, ErrDuplicate) || mongo.IsDuplicateKeyError(err):
		return ErrDuplicate
	case errors.Is(err, ErrVersionConflict):
		return ErrVersionConflict
	default:
		return err
	}
}
