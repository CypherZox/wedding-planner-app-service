package user

import (
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

const authProvider = "supabase"

// User is one account. Users have no weddingId and no version.
type User struct {
	ID             bson.ObjectID `bson:"_id" json:"-"`
	AuthProvider   string        `bson:"authProvider" json:"-"`
	AuthUID        string        `bson:"authUid" json:"-"`
	Email          string        `bson:"email" json:"-"`
	EmailVerified  bool          `bson:"emailVerified" json:"-"`
	DisplayName    string        `bson:"displayName" json:"-"`
	AvatarMediaKey string        `bson:"avatarMediaKey,omitempty" json:"-"`
	Locale         string        `bson:"locale" json:"-"`
	LastWeddingID  bson.ObjectID `bson:"lastWeddingId,omitempty" json:"-"`
	LastSeenAt     time.Time     `bson:"lastSeenAt" json:"-"`
	CreatedAt      time.Time     `bson:"createdAt" json:"-"`
	UpdatedAt      time.Time     `bson:"updatedAt" json:"-"`
	DeletedAt      *time.Time    `bson:"deletedAt,omitempty" json:"-"`
}

// Account is the caller taken from a verified Supabase access token.
type Account struct {
	AuthUID       string
	Email         string
	EmailVerified bool
}
