package config

import (
	"fmt"
	"os"
	"strings"
)

// Config is the process configuration. Every field is required.
type Config struct {
	MongoURI               string
	MongoDB                string
	HTTPAddr               string
	PublicAppURL           string
	SupabaseURL            string
	SupabasePublishableKey string
	SupabaseSecretKey      string
	SupabaseJWKSURL        string
}

// Load reads the required environment variables.
func Load() (Config, error) {
	cfg := Config{}
	fields := []struct {
		key string
		dst *string
	}{
		{"MONGODB_URI", &cfg.MongoURI},
		{"MONGODB_DB", &cfg.MongoDB},
		{"HTTP_ADDR", &cfg.HTTPAddr},
		{"PUBLIC_APP_URL", &cfg.PublicAppURL},
		{"SUPABASE_URL", &cfg.SupabaseURL},
		{"SUPABASE_PUBLISHABLE_KEY", &cfg.SupabasePublishableKey},
		{"SUPABASE_SECRET_KEY", &cfg.SupabaseSecretKey},
		{"SUPABASE_JWKS_URL", &cfg.SupabaseJWKSURL},
	}

	var missing []string
	for _, field := range fields {
		value := os.Getenv(field.key)
		if value == "" {
			missing = append(missing, field.key)
			continue
		}
		*field.dst = value
	}
	if len(missing) > 0 {
		return Config{}, fmt.Errorf("missing required environment variables: %s", strings.Join(missing, ", "))
	}
	return cfg, nil
}
