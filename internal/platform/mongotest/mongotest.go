// Package mongotest starts one MongoDB 9.0 replica set per test package.
package mongotest

import (
	"context"
	"fmt"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go/modules/mongodb"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

// MongoDB publishes 9.0 as 9.0-ubi9, not the bare tag 9.0.
const image = "mongodb/mongodb-community-server:9.0-ubi9"

var (
	uri string
	seq atomic.Uint64
)

// Main starts MongoDB for this package and runs the tests.
func Main(m *testing.M) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	container, err := mongodb.Run(ctx, image, mongodb.WithReplicaSet("rs0"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "start mongodb: %v\n", err)
		os.Exit(1)
	}

	rawURI, err := container.ConnectionString(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mongodb connection string: %v\n", err)
		os.Exit(1)
	}
	// The replica set advertises the container IP. directConnection keeps the
	// host on the published port, which is the address this process can use.
	uri = rawURI + "&directConnection=true"

	code := m.Run()
	termCtx, termCancel := context.WithTimeout(context.Background(), 20*time.Second)
	_ = container.Terminate(termCtx)
	termCancel()
	os.Exit(code)
}

// URI returns the container connection string. It is empty before Main runs tests.
func URI() string {
	return uri
}

// Database returns a fresh database dropped when the test ends.
func Database(t *testing.T, client *mongo.Client) *mongo.Database {
	t.Helper()
	name := fmt.Sprintf("t%d", seq.Add(1))
	database := client.Database(name)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = database.Drop(ctx)
	})
	return database
}
