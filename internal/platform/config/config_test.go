package config

import "testing"

func TestLoadRequiresEveryVariable(t *testing.T) {
	keys := []string{
		"MONGODB_URI",
		"MONGODB_DB",
		"HTTP_ADDR",
		"PUBLIC_APP_URL",
		"SUPABASE_URL",
		"SUPABASE_PUBLISHABLE_KEY",
		"SUPABASE_SECRET_KEY",
		"SUPABASE_JWKS_URL",
	}
	for _, key := range keys {
		t.Setenv(key, "set")
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.MongoDB != "set" || cfg.HTTPAddr != "set" || cfg.PublicAppURL != "set" {
		t.Fatalf("Load() = %+v", cfg)
	}

	t.Setenv("MONGODB_DB", "")
	if _, err := Load(); err == nil {
		t.Fatal("Load() succeeded with MONGODB_DB empty")
	}
}
