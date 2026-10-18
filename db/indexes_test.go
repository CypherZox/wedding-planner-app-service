package db

import "testing"

func TestIndexesCoverEveryCollection(t *testing.T) {
	got := map[string]int{}
	for _, spec := range Indexes() {
		got[spec.Collection] = len(spec.Models)
	}

	want := map[string]int{
		"users":          2,
		"weddings":       1,
		"invites":        5,
		"households":     3,
		"guests":         5,
		"seating_tables": 3,
		"tasks":          4,
		"vendors":        3,
		"payments":       4,
		"schedules":      3,
		"media":          4,
	}
	if len(got) != len(want) {
		t.Fatalf("index collections = %d, want %d", len(got), len(want))
	}
	for name, count := range want {
		if got[name] != count {
			t.Fatalf("%s indexes = %d, want %d", name, got[name], count)
		}
	}
	for _, name := range Collections {
		if _, ok := got[name]; !ok {
			t.Fatalf("collection %s has no indexes", name)
		}
	}
}
