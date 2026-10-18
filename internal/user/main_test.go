package user

import (
	"testing"

	"github.com/CypherZox/wedding-planner/backend-service/internal/platform/mongotest"
)

func TestMain(m *testing.M) {
	mongotest.Main(m)
}
