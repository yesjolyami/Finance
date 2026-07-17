package config

import (
	"os"
	"strings"
	"testing"
)

func setValidAuthEnvironment(t *testing.T) {
	t.Helper()
	t.Setenv("AUTH_ISSUER", "https://project.test/auth/v1")
	t.Setenv("AUTH_AUDIENCE", "authenticated")
	t.Setenv("AUTH_JWKS_URL", "https://project.test/auth/v1/.well-known/jwks.json")
}

func setValidProductionEnvironment(t *testing.T) {
	t.Helper()
	t.Setenv("APP_ENV", "production")
	t.Setenv("HTTP_HOST", "127.0.0.1")
	t.Setenv("HTTP_PORT", "8080")
	setProductionDatabaseURL(t, "postgresql://finance:strong-password@db.finance.internal:5432/finance?sslmode=require")
	t.Setenv("FRONTEND_ORIGINS", "https://app.finance.example")
	t.Setenv("AUTH_ISSUER", "https://auth.finance.example/auth/v1")
	t.Setenv("AUTH_AUDIENCE", "authenticated")
	t.Setenv("AUTH_JWKS_URL", "https://auth.finance.example/auth/v1/.well-known/jwks.json")
	t.Setenv("PRODUCTION_SECURITY_PROFILE", productionProfileV1)
	t.Setenv("PRODUCTION_SECURITY_ACK", productionSecurityAck)
	t.Setenv("API_REPLICA_COUNT", "1")
}

func setProductionDatabaseURL(t *testing.T, value string) {
	t.Helper()
	t.Setenv("DATABASE_URL", "")
	t.Setenv("DATABASE_URL_FILE", writeDatabaseURLFile(t, value+"\n", 0o400))
}

func TestLoadDefaults(t *testing.T) {
	setValidAuthEnvironment(t)
	t.Setenv("APP_ENV", "")
	t.Setenv("HTTP_HOST", "")
	t.Setenv("HTTP_PORT", "")
	t.Setenv("DATABASE_URL", "")
	t.Setenv("FRONTEND_ORIGINS", "")

	config, err := Load()
	if err != nil {
		t.Fatalf("Load() returned an error: %v", err)
	}
	if config.AppEnv != defaultAppEnv || config.Address() != "127.0.0.1:8080" {
		t.Fatalf("unexpected defaults: %#v", config)
	}
	if config.DatabaseURL != defaultDatabaseURL {
		t.Fatal("unexpected default DATABASE_URL")
	}
	if len(config.FrontendOrigins) != 1 || config.FrontendOrigins[0] != "http://127.0.0.1:5173" {
		t.Fatalf("unexpected origins: %#v", config.FrontendOrigins)
	}
	if config.ImportBackupV5Enabled || config.ImportHMACActiveKeyID != "" || config.ImportHMACKeyringFile != "" {
		t.Fatalf("backup import must default to disabled: %#v", config)
	}
	if config.APIReplicaCount != 1 {
		t.Fatalf("unexpected local replica default: %#v", config)
	}
}

func TestBackupImportConfigurationIsFailClosed(t *testing.T) {
	setValidAuthEnvironment(t)
	t.Setenv("APP_ENV", "test")
	t.Setenv("IMPORT_HMAC_ACTIVE_KEY_ID", "active")
	t.Setenv("IMPORT_HMAC_KEYRING_FILE", "/path/that/must/not/be/read.json")

	config, err := Load()
	if err != nil {
		t.Fatalf("disabled import validated or read keyring configuration: %v", err)
	}
	if config.ImportBackupV5Enabled {
		t.Fatal("backup import unexpectedly enabled")
	}

	t.Setenv("IMPORT_BACKUP_V5_ENABLED", "true")
	for _, environment := range []string{"local", "development", "test"} {
		t.Setenv("APP_ENV", environment)
		config, err = Load()
		if err != nil {
			t.Fatalf("enabled %s import rejected: %v", environment, err)
		}
		if !config.ImportBackupV5Enabled || config.ImportHMACActiveKeyID != "active" ||
			config.ImportHMACKeyringFile != "/path/that/must/not/be/read.json" {
			t.Fatalf("enabled import configuration mismatch: %#v", config)
		}
	}
}

func TestBackupImportConfigurationRejectsUnsafeValues(t *testing.T) {
	tests := []struct {
		name        string
		environment string
		enabled     string
		activeID    string
		path        string
	}{
		{name: "invalid boolean", environment: "test", enabled: "1", activeID: "active", path: "/tmp/keys.json"},
		{name: "missing active key", environment: "test", enabled: "true", path: "/tmp/keys.json"},
		{name: "padded active key", environment: "test", enabled: "true", activeID: " active ", path: "/tmp/keys.json"},
		{name: "missing keyring file", environment: "test", enabled: "true", activeID: "active"},
		{name: "relative keyring file", environment: "test", enabled: "true", activeID: "active", path: "keys.json"},
		{name: "production missing acknowledgement", environment: "production", enabled: "true", activeID: "active", path: "/tmp/keys.json"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			setValidAuthEnvironment(t)
			t.Setenv("APP_ENV", test.environment)
			if test.environment == "production" {
				setValidProductionEnvironment(t)
				t.Setenv("IMPORT_PRODUCTION_ACK", "")
			}
			t.Setenv("IMPORT_BACKUP_V5_ENABLED", test.enabled)
			t.Setenv("IMPORT_HMAC_ACTIVE_KEY_ID", test.activeID)
			t.Setenv("IMPORT_HMAC_KEYRING_FILE", test.path)
			if _, err := Load(); err == nil {
				t.Fatal("unsafe backup import configuration accepted")
			}
		})
	}
}

func TestProductionImportRequiresAllAcknowledgements(t *testing.T) {
	setValidProductionEnvironment(t)
	t.Setenv("IMPORT_BACKUP_V5_ENABLED", "true")
	t.Setenv("IMPORT_HMAC_ACTIVE_KEY_ID", "active")
	t.Setenv("IMPORT_HMAC_KEYRING_FILE", "/run/secrets/import-keyring.json")
	t.Setenv("IMPORT_PRODUCTION_ACK", productionImportAck)

	config, err := Load()
	if err != nil {
		t.Fatalf("valid production import configuration rejected: %v", err)
	}
	if !config.ImportBackupV5Enabled || config.ImportProductionAck != productionImportAck {
		t.Fatalf("production import was not enabled: %#v", config)
	}

	for _, key := range []string{"PRODUCTION_SECURITY_PROFILE", "PRODUCTION_SECURITY_ACK", "IMPORT_PRODUCTION_ACK"} {
		t.Run(key, func(t *testing.T) {
			setValidProductionEnvironment(t)
			t.Setenv("IMPORT_BACKUP_V5_ENABLED", "true")
			t.Setenv("IMPORT_HMAC_ACTIVE_KEY_ID", "active")
			t.Setenv("IMPORT_HMAC_KEYRING_FILE", "/run/secrets/import-keyring.json")
			t.Setenv("IMPORT_PRODUCTION_ACK", productionImportAck)
			t.Setenv(key, "wrong")
			if _, err := Load(); err == nil {
				t.Fatalf("production import accepted invalid %s", key)
			}
		})
	}
}

func TestLoadRequiresAuthConfiguration(t *testing.T) {
	for _, key := range []string{"AUTH_ISSUER", "AUTH_AUDIENCE", "AUTH_JWKS_URL"} {
		t.Run(key, func(t *testing.T) {
			setValidAuthEnvironment(t)
			t.Setenv(key, "")
			if _, err := Load(); err == nil {
				t.Fatalf("Load() accepted missing %s", key)
			}
		})
	}
}

func TestLoadFromEnvironment(t *testing.T) {
	setValidAuthEnvironment(t)
	t.Setenv("APP_ENV", "test")
	t.Setenv("HTTP_HOST", "0.0.0.0")
	t.Setenv("HTTP_PORT", "9090")
	t.Setenv("DATABASE_URL", "postgresql://user:password@localhost:5544/test?sslmode=disable")
	t.Setenv("FRONTEND_ORIGINS", "http://127.0.0.1:5173, https://app.test")

	config, err := Load()
	if err != nil {
		t.Fatalf("Load() returned an error: %v", err)
	}
	if config.AppEnv != "test" || config.Address() != "0.0.0.0:9090" {
		t.Fatalf("environment values were not loaded: %#v", config)
	}
	if len(config.FrontendOrigins) != 2 || config.FrontendOrigins[1] != "https://app.test" {
		t.Fatalf("origin allowlist was not loaded: %#v", config.FrontendOrigins)
	}
}

func TestLoadRejectsInvalidGeneralConfiguration(t *testing.T) {
	tests := []struct {
		name  string
		key   string
		value string
	}{
		{name: "environment", key: "APP_ENV", value: "staging"},
		{name: "port", key: "HTTP_PORT", value: "70000"},
		{name: "database URL", key: "DATABASE_URL", value: "sqlite:///tmp/finance.db"},
		{name: "origin path", key: "FRONTEND_ORIGINS", value: "https://example.test/path"},
		{name: "origin wildcard", key: "FRONTEND_ORIGINS", value: "https://*.example.test"},
		{name: "origin duplicate", key: "FRONTEND_ORIGINS", value: "https://app.test,https://app.test"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			setValidAuthEnvironment(t)
			t.Setenv("APP_ENV", "test")
			t.Setenv("HTTP_PORT", "8080")
			t.Setenv("DATABASE_URL", defaultDatabaseURL)
			t.Setenv("FRONTEND_ORIGINS", defaultFrontendOrigins)
			t.Setenv(test.key, test.value)
			if _, err := Load(); err == nil {
				t.Fatal("Load() accepted invalid configuration")
			}
		})
	}
}

func TestLoadRejectsUnsafeAuthURLsAndDurations(t *testing.T) {
	tests := []struct {
		name  string
		key   string
		value string
	}{
		{name: "production HTTP issuer", key: "AUTH_ISSUER", value: "http://project.test/auth/v1"},
		{name: "production HTTP JWKS", key: "AUTH_JWKS_URL", value: "http://project.test/jwks"},
		{name: "production issuer credentials", key: "AUTH_ISSUER", value: "https://user:password@auth.finance.example/auth/v1"},
		{name: "production JWKS query", key: "AUTH_JWKS_URL", value: "https://auth.finance.example/jwks?secret=value"},
		{name: "placeholder production issuer", key: "AUTH_ISSUER", value: "https://your-project-ref.supabase.co/auth/v1"},
		{name: "TTL over limit", key: "AUTH_JWKS_CACHE_TTL", value: "11m"},
		{name: "cooldown too short", key: "AUTH_JWKS_REFRESH_COOLDOWN", value: "500ms"},
		{name: "cooldown too long", key: "AUTH_JWKS_REFRESH_COOLDOWN", value: "61s"},
		{name: "negative skew", key: "AUTH_CLOCK_SKEW", value: "-1s"},
		{name: "skew over limit", key: "AUTH_CLOCK_SKEW", value: "121s"},
		{name: "zero timeout", key: "AUTH_JWKS_HTTP_TIMEOUT", value: "0s"},
		{name: "timeout over limit", key: "AUTH_JWKS_HTTP_TIMEOUT", value: "11s"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			setValidProductionEnvironment(t)
			t.Setenv("AUTH_JWKS_CACHE_TTL", "10m")
			t.Setenv("AUTH_JWKS_REFRESH_COOLDOWN", "30s")
			t.Setenv("AUTH_CLOCK_SKEW", "30s")
			t.Setenv("AUTH_JWKS_HTTP_TIMEOUT", "2s")
			t.Setenv(test.key, test.value)
			if _, err := Load(); err == nil {
				t.Fatal("Load() accepted unsafe auth configuration")
			}
		})
	}
}

func TestProductionConfigurationFailsClosed(t *testing.T) {
	required := []string{
		"HTTP_HOST", "HTTP_PORT", "DATABASE_URL_FILE", "FRONTEND_ORIGINS",
		"PRODUCTION_SECURITY_PROFILE", "PRODUCTION_SECURITY_ACK", "API_REPLICA_COUNT",
	}
	for _, key := range required {
		t.Run("missing "+key, func(t *testing.T) {
			setValidProductionEnvironment(t)
			t.Setenv(key, "")
			if _, err := Load(); err == nil {
				t.Fatalf("production accepted missing %s", key)
			}
		})
	}

	tests := []struct {
		name  string
		key   string
		value string
	}{
		{name: "public bind", key: "HTTP_HOST", value: "0.0.0.0"},
		{name: "multiple replicas", key: "API_REPLICA_COUNT", value: "2"},
		{name: "unknown profile", key: "PRODUCTION_SECURITY_PROFILE", value: "unknown"},
		{name: "wrong ack", key: "PRODUCTION_SECURITY_ACK", value: "yes"},
		{name: "HTTP origin", key: "FRONTEND_ORIGINS", value: "http://app.finance.example"},
		{name: "origin credentials", key: "FRONTEND_ORIGINS", value: "https://user:password@app.finance.example"},
		{name: "origin placeholder", key: "FRONTEND_ORIGINS", value: "https://app.example.test"},
		{name: "padded auth audience", key: "AUTH_AUDIENCE", value: " authenticated "},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			setValidProductionEnvironment(t)
			t.Setenv(test.key, test.value)
			if _, err := Load(); err == nil {
				t.Fatal("unsafe production configuration accepted")
			}
		})
	}

	for _, test := range []struct {
		name  string
		value string
	}{
		{name: "external DB TLS disabled", value: "postgres://finance:secret@db.finance.internal/finance?sslmode=disable"},
		{name: "external DB TLS implicit", value: "postgres://finance:secret@db.finance.internal/finance"},
		{name: "database placeholder", value: "postgres://finance:finance_dev_only@127.0.0.1/finance?sslmode=disable"},
		{name: "padded database URL", value: " postgres://finance:secret@127.0.0.1/finance?sslmode=disable "},
	} {
		t.Run(test.name, func(t *testing.T) {
			setValidProductionEnvironment(t)
			setProductionDatabaseURL(t, test.value)
			if _, err := Load(); err == nil {
				t.Fatal("unsafe production database configuration accepted")
			}
		})
	}
}

func TestProductionConfigurationAcceptsLoopbackDatabaseAndExactHTTPSOrigins(t *testing.T) {
	setValidProductionEnvironment(t)
	setProductionDatabaseURL(t, "postgres://finance:secret@127.0.0.1:5432/finance?sslmode=disable")
	t.Setenv("FRONTEND_ORIGINS", "https://app.finance.example,https://admin.finance.example")
	config, err := Load()
	if err != nil {
		t.Fatalf("valid production configuration rejected: %v", err)
	}
	if config.Address() != "127.0.0.1:8080" || len(config.FrontendOrigins) != 2 {
		t.Fatalf("unexpected production configuration: %#v", config)
	}
}

func TestProductionDatabaseURLMustComeFromCredentialFile(t *testing.T) {
	setValidProductionEnvironment(t)
	t.Setenv("DATABASE_URL_FILE", "")
	t.Setenv("DATABASE_URL", "postgresql://finance:secret@db.finance.internal/finance?sslmode=require")
	if _, err := Load(); err == nil {
		t.Fatal("production accepted raw DATABASE_URL")
	}

	setValidProductionEnvironment(t)
	t.Setenv("DATABASE_URL", "postgresql://finance:secret@db.finance.internal/finance?sslmode=require")
	if _, err := Load(); err == nil {
		t.Fatal("production accepted both database sources")
	}
}

func TestLocalDatabaseCredentialFileCompatibility(t *testing.T) {
	setValidAuthEnvironment(t)
	t.Setenv("APP_ENV", "test")
	t.Setenv("DATABASE_URL", "")
	expected := "postgres://finance:secret@127.0.0.1:5432/finance?sslmode=disable"
	path := writeDatabaseURLFile(t, expected, 0o600)
	t.Setenv("DATABASE_URL_FILE", path)
	loaded, err := Load()
	if err != nil {
		t.Fatalf("local credential file rejected: %v", err)
	}
	if loaded.DatabaseURL != expected {
		t.Fatal("local credential file value changed")
	}

	t.Setenv("DATABASE_URL", expected)
	if _, err := Load(); err == nil {
		t.Fatal("local configuration accepted both database sources")
	}
}

func TestProductionDatabaseCredentialErrorsAreRedacted(t *testing.T) {
	setValidProductionEnvironment(t)
	secret := "database-super-secret"
	path := writeDatabaseURLFile(t, "postgresql://finance:"+secret+"@db.internal/finance?sslmode=require\nextra", os.FileMode(0o400))
	t.Setenv("DATABASE_URL_FILE", path)
	_, err := Load()
	if err == nil {
		t.Fatal("invalid database credential accepted")
	}
	if rendered := err.Error(); rendered == "" || containsAny(rendered, path, secret) {
		t.Fatalf("database credential error leaked sensitive context: %q", rendered)
	}
}

func containsAny(value string, needles ...string) bool {
	for _, needle := range needles {
		if needle != "" && strings.Contains(value, needle) {
			return true
		}
	}
	return false
}

func TestLocalEnvironmentAllowsHTTPAuthURLs(t *testing.T) {
	setValidAuthEnvironment(t)
	t.Setenv("APP_ENV", "test")
	t.Setenv("AUTH_ISSUER", "http://127.0.0.1:54321/auth/v1")
	t.Setenv("AUTH_JWKS_URL", "http://127.0.0.1:54321/jwks")
	if _, err := Load(); err != nil {
		t.Fatalf("local HTTP auth URLs were rejected: %v", err)
	}
}
