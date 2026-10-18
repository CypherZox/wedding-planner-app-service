package db

// Collections are the v1 collections, in migrate order.
var Collections = []string{
	"users",
	"weddings",
	"invites",
	"households",
	"guests",
	"seating_tables",
	"tasks",
	"vendors",
	"payments",
	"schedules",
	"media",
}

const migrationsCollection = "migrations"
