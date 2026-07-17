package config

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	defaultAppEnv          = "development"
	defaultHTTPHost        = "127.0.0.1"
	defaultHTTPPort        = 8080
	defaultDatabaseURL     = "postgres://finance:finance_dev_only@127.0.0.1:5432/finance_dev?sslmode=disable"
	defaultFrontendOrigins = "http://127.0.0.1:5173"
	productionProfileV1    = "single-proxy-single-replica-v1"
	productionSecurityAck  = "ack-single-proxy-single-replica-v1"
	productionImportAck    = "ack-backup-v5-single-replica-v1"
)

type Config struct {
	AppEnv                  string
	HTTPHost                string
	HTTPPort                int
	DatabaseURL             string
	FrontendOrigins         []string
	AuthIssuer              string
	AuthAudience            string
	AuthJWKSURL             string
	AuthJWKSCacheTTL        time.Duration
	AuthJWKSRefreshCooldown time.Duration
	AuthClockSkew           time.Duration
	AuthHTTPTimeout         time.Duration
	SecurityProfile         string
	ProductionSecurityAck   string
	APIReplicaCount         int
	ImportBackupV5Enabled   bool
	ImportHMACActiveKeyID   string
	ImportHMACKeyringFile   string
	ImportProductionAck     string
}

func Load() (Config, error) {
	appEnvironment := valueOrDefault("APP_ENV", defaultAppEnv)
	if err := validateEnvironment(appEnvironment); err != nil {
		return Config{}, err
	}
	production := appEnvironment == "production"
	authIssuer, err := requiredEnvironmentValue("AUTH_ISSUER", production)
	if err != nil {
		return Config{}, err
	}
	authAudience, err := requiredEnvironmentValue("AUTH_AUDIENCE", production)
	if err != nil {
		return Config{}, err
	}
	authJWKSURL, err := requiredEnvironmentValue("AUTH_JWKS_URL", production)
	if err != nil {
		return Config{}, err
	}
	httpHost, err := environmentValue("HTTP_HOST", defaultHTTPHost, production)
	if err != nil {
		return Config{}, err
	}
	httpPort, err := environmentValue("HTTP_PORT", strconv.Itoa(defaultHTTPPort), production)
	if err != nil {
		return Config{}, err
	}
	port, err := parsePort(httpPort)
	if err != nil {
		return Config{}, err
	}
	cacheTTL, err := parseDuration("AUTH_JWKS_CACHE_TTL", "10m")
	if err != nil {
		return Config{}, err
	}
	refreshCooldown, err := parseDuration("AUTH_JWKS_REFRESH_COOLDOWN", "30s")
	if err != nil {
		return Config{}, err
	}
	clockSkew, err := parseDuration("AUTH_CLOCK_SKEW", "30s")
	if err != nil {
		return Config{}, err
	}
	httpTimeout, err := parseDuration("AUTH_JWKS_HTTP_TIMEOUT", "2s")
	if err != nil {
		return Config{}, err
	}
	originsValue, err := environmentValue("FRONTEND_ORIGINS", defaultFrontendOrigins, production)
	if err != nil {
		return Config{}, err
	}
	origins, err := parseOrigins(originsValue)
	if err != nil {
		return Config{}, err
	}
	databaseURL, err := loadDatabaseURL(production)
	if err != nil {
		return Config{}, err
	}
	securityProfile, securityAck, replicaCount, err := parseSecurityConfiguration(appEnvironment)
	if err != nil {
		return Config{}, err
	}

	config := Config{
		AppEnv:                  appEnvironment,
		HTTPHost:                httpHost,
		HTTPPort:                port,
		DatabaseURL:             databaseURL,
		FrontendOrigins:         origins,
		AuthIssuer:              authIssuer,
		AuthAudience:            authAudience,
		AuthJWKSURL:             authJWKSURL,
		AuthJWKSCacheTTL:        cacheTTL,
		AuthJWKSRefreshCooldown: refreshCooldown,
		AuthClockSkew:           clockSkew,
		AuthHTTPTimeout:         httpTimeout,
		SecurityProfile:         securityProfile,
		ProductionSecurityAck:   securityAck,
		APIReplicaCount:         replicaCount,
	}
	if err := validateHTTPBind(config); err != nil {
		return Config{}, err
	}
	if err := validateOrigins(config); err != nil {
		return Config{}, err
	}
	if err := validateDatabaseURL(config.DatabaseURL, config.AppEnv); err != nil {
		return Config{}, err
	}
	if err := validateAuth(config); err != nil {
		return Config{}, err
	}
	importEnabled, importActiveKeyID, importKeyringFile, importAck, err := parseImportConfiguration(config)
	if err != nil {
		return Config{}, err
	}
	config.ImportBackupV5Enabled = importEnabled
	config.ImportHMACActiveKeyID = importActiveKeyID
	config.ImportHMACKeyringFile = importKeyringFile
	config.ImportProductionAck = importAck
	return config, nil
}

func loadDatabaseURL(production bool) (string, error) {
	rawURL := os.Getenv("DATABASE_URL")
	rawFile := os.Getenv("DATABASE_URL_FILE")
	if rawURL != "" && rawFile != "" {
		return "", fmt.Errorf("DATABASE_URL and DATABASE_URL_FILE are mutually exclusive")
	}
	if production {
		if rawURL != "" {
			return "", fmt.Errorf("production requires DATABASE_URL_FILE and forbids DATABASE_URL")
		}
		filePath, err := requiredExactValue("DATABASE_URL_FILE")
		if err != nil {
			return "", err
		}
		return loadDatabaseURLFile(filePath)
	}
	if rawFile != "" {
		if strings.TrimSpace(rawFile) != rawFile {
			return "", fmt.Errorf("DATABASE_URL_FILE must not contain outer whitespace")
		}
		return loadDatabaseURLFile(rawFile)
	}
	return valueOrDefault("DATABASE_URL", defaultDatabaseURL), nil
}

func parseSecurityConfiguration(appEnvironment string) (string, string, int, error) {
	rawReplicaCount := valueOrDefault("API_REPLICA_COUNT", "1")
	if appEnvironment == "production" {
		var err error
		rawReplicaCount, err = requiredExactValue("API_REPLICA_COUNT")
		if err != nil {
			return "", "", 0, err
		}
	}
	replicaCount, err := strconv.Atoi(rawReplicaCount)
	if err != nil || replicaCount < 1 {
		return "", "", 0, fmt.Errorf("API_REPLICA_COUNT must be a positive integer")
	}
	profile := os.Getenv("PRODUCTION_SECURITY_PROFILE")
	ack := os.Getenv("PRODUCTION_SECURITY_ACK")
	if appEnvironment != "production" {
		if profile != "" && profile != productionProfileV1 {
			return "", "", 0, fmt.Errorf("PRODUCTION_SECURITY_PROFILE is not supported")
		}
		if ack != "" && ack != productionSecurityAck {
			return "", "", 0, fmt.Errorf("PRODUCTION_SECURITY_ACK is not supported")
		}
		return profile, ack, replicaCount, nil
	}
	if profile != productionProfileV1 {
		return "", "", 0, fmt.Errorf("PRODUCTION_SECURITY_PROFILE must acknowledge the supported topology")
	}
	if ack != productionSecurityAck {
		return "", "", 0, fmt.Errorf("PRODUCTION_SECURITY_ACK is required")
	}
	if replicaCount != 1 {
		return "", "", 0, fmt.Errorf("production supports exactly one API replica")
	}
	return profile, ack, replicaCount, nil
}

func parseImportConfiguration(config Config) (bool, string, string, string, error) {
	rawEnabled := os.Getenv("IMPORT_BACKUP_V5_ENABLED")
	if rawEnabled == "" || rawEnabled == "false" {
		return false, "", "", "", nil
	}
	if rawEnabled != "true" {
		return false, "", "", "", fmt.Errorf("IMPORT_BACKUP_V5_ENABLED must be true or false")
	}
	activeKeyID, err := requiredExactValue("IMPORT_HMAC_ACTIVE_KEY_ID")
	if err != nil {
		return false, "", "", "", err
	}
	keyringFile, err := requiredExactValue("IMPORT_HMAC_KEYRING_FILE")
	if err != nil {
		return false, "", "", "", err
	}
	if !filepath.IsAbs(keyringFile) {
		return false, "", "", "", fmt.Errorf("IMPORT_HMAC_KEYRING_FILE must be an absolute path")
	}
	importAck := ""
	if config.AppEnv == "production" {
		importAck = os.Getenv("IMPORT_PRODUCTION_ACK")
		if config.SecurityProfile != productionProfileV1 || config.ProductionSecurityAck != productionSecurityAck ||
			config.APIReplicaCount != 1 || importAck != productionImportAck {
			return false, "", "", "", fmt.Errorf("production backup import prerequisites are not acknowledged")
		}
	}
	return true, activeKeyID, keyringFile, importAck, nil
}

func (config Config) Address() string {
	return net.JoinHostPort(config.HTTPHost, strconv.Itoa(config.HTTPPort))
}

func (config Config) ProductionSecurityReady() bool {
	return config.AppEnv == "production" &&
		config.SecurityProfile == productionProfileV1 &&
		config.ProductionSecurityAck == productionSecurityAck &&
		config.APIReplicaCount == 1
}

func (config Config) ProductionImportReady() bool {
	return config.ProductionSecurityReady() &&
		config.ImportBackupV5Enabled &&
		config.ImportProductionAck == productionImportAck
}

func valueOrDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func environmentValue(key, fallback string, production bool) (string, error) {
	if !production {
		return valueOrDefault(key, fallback), nil
	}
	return requiredExactValue(key)
}

func requiredEnvironmentValue(key string, production bool) (string, error) {
	if production {
		return requiredExactValue(key)
	}
	return requiredValue(key)
}

func requiredValue(key string) (string, error) {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return "", fmt.Errorf("%s is required", key)
	}
	return value, nil
}

func requiredExactValue(key string) (string, error) {
	value := os.Getenv(key)
	if value == "" || strings.TrimSpace(value) != value {
		return "", fmt.Errorf("%s is required and must not contain outer whitespace", key)
	}
	return value, nil
}

func parsePort(raw string) (int, error) {
	port, err := strconv.Atoi(raw)
	if err != nil || port < 1 || port > 65535 {
		return 0, fmt.Errorf("HTTP_PORT must be an integer between 1 and 65535")
	}
	return port, nil
}

func parseDuration(key, fallback string) (time.Duration, error) {
	value := valueOrDefault(key, fallback)
	duration, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("%s must be a valid duration", key)
	}
	return duration, nil
}

func parseOrigins(raw string) ([]string, error) {
	parts := strings.Split(raw, ",")
	origins := make([]string, 0, len(parts))
	seen := make(map[string]struct{})
	for _, part := range parts {
		origin := strings.TrimSpace(part)
		if err := validateOrigin(origin); err != nil {
			return nil, err
		}
		if _, duplicate := seen[origin]; duplicate {
			return nil, fmt.Errorf("FRONTEND_ORIGINS must not contain duplicates")
		}
		seen[origin] = struct{}{}
		origins = append(origins, origin)
	}
	if len(origins) == 0 {
		return nil, fmt.Errorf("FRONTEND_ORIGINS must contain at least one origin")
	}
	return origins, nil
}

func validateOrigin(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") ||
		parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" ||
		strings.Contains(raw, "*") {
		return fmt.Errorf("FRONTEND_ORIGINS must contain exact HTTP origins without paths or wildcards")
	}
	return nil
}

func validateDatabaseURL(raw, environment string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || parsed.Fragment != "" ||
		(parsed.Scheme != "postgres" && parsed.Scheme != "postgresql") {
		return fmt.Errorf("DATABASE_URL must be a valid PostgreSQL connection URL")
	}
	if environment == "production" {
		if containsPlaceholder(raw) {
			return fmt.Errorf("placeholder DATABASE_URL is forbidden in production")
		}
		if !isLoopbackHost(parsed.Hostname()) {
			sslModes := parsed.Query()["sslmode"]
			if len(sslModes) != 1 {
				return fmt.Errorf("external production DATABASE_URL requires an explicit TLS sslmode")
			}
			switch strings.ToLower(sslModes[0]) {
			case "require", "verify-ca", "verify-full":
			default:
				return fmt.Errorf("external production DATABASE_URL must not disable TLS")
			}
		}
	}
	return nil
}

func validateHTTPBind(config Config) error {
	if config.HTTPHost == "" || strings.TrimSpace(config.HTTPHost) != config.HTTPHost ||
		strings.ContainsAny(config.HTTPHost, "/?#@") {
		return fmt.Errorf("HTTP_HOST must be an exact host")
	}
	if config.AppEnv == "production" && config.HTTPHost != "127.0.0.1" && config.HTTPHost != "::1" {
		return fmt.Errorf("production HTTP_HOST must bind to loopback")
	}
	return nil
}

func validateOrigins(config Config) error {
	for _, origin := range config.FrontendOrigins {
		parsed, _ := url.Parse(origin)
		if config.AppEnv == "production" && (parsed.Scheme != "https" || containsPlaceholder(origin)) {
			return fmt.Errorf("production FRONTEND_ORIGINS must contain only exact non-placeholder HTTPS origins")
		}
	}
	return nil
}

func validateEnvironment(value string) error {
	switch value {
	case "local", "development", "test", "production":
		return nil
	default:
		return fmt.Errorf("APP_ENV must be local, development, test or production")
	}
}

func validateAuth(config Config) error {
	if strings.TrimSpace(config.AuthAudience) == "" {
		return fmt.Errorf("AUTH_AUDIENCE is required")
	}
	issuer, err := url.Parse(config.AuthIssuer)
	if err != nil || issuer.Host == "" || issuer.User != nil || issuer.RawQuery != "" || issuer.Fragment != "" {
		return fmt.Errorf("AUTH_ISSUER must be a valid URL")
	}
	jwksURL, err := url.Parse(config.AuthJWKSURL)
	if err != nil || jwksURL.Host == "" || jwksURL.User != nil || jwksURL.RawQuery != "" || jwksURL.Fragment != "" {
		return fmt.Errorf("AUTH_JWKS_URL must be a valid URL")
	}
	localEnvironment := config.AppEnv == "local" || config.AppEnv == "development" || config.AppEnv == "test"
	if !localEnvironment && (issuer.Scheme != "https" || jwksURL.Scheme != "https") {
		return fmt.Errorf("AUTH_ISSUER and AUTH_JWKS_URL must use HTTPS outside local/test")
	}
	if localEnvironment {
		if issuer.Scheme != "http" && issuer.Scheme != "https" {
			return fmt.Errorf("AUTH_ISSUER must use HTTP or HTTPS")
		}
		if jwksURL.Scheme != "http" && jwksURL.Scheme != "https" {
			return fmt.Errorf("AUTH_JWKS_URL must use HTTP or HTTPS")
		}
	}
	if config.AppEnv == "production" && (strings.Contains(config.AuthIssuer, "your-project-ref") || strings.Contains(config.AuthJWKSURL, "your-project-ref")) {
		return fmt.Errorf("placeholder auth configuration is forbidden in production")
	}
	if config.AppEnv == "production" &&
		(containsPlaceholder(config.AuthIssuer) || containsPlaceholder(config.AuthJWKSURL) || containsPlaceholder(config.AuthAudience)) {
		return fmt.Errorf("placeholder auth configuration is forbidden in production")
	}
	if config.AuthJWKSCacheTTL <= 0 || config.AuthJWKSCacheTTL > 10*time.Minute {
		return fmt.Errorf("AUTH_JWKS_CACHE_TTL must be greater than zero and at most 10m")
	}
	if config.AuthJWKSRefreshCooldown < time.Second || config.AuthJWKSRefreshCooldown > time.Minute ||
		config.AuthJWKSRefreshCooldown > config.AuthJWKSCacheTTL {
		return fmt.Errorf("AUTH_JWKS_REFRESH_COOLDOWN must be between 1s and 1m and not exceed cache TTL")
	}
	if config.AuthClockSkew < 0 || config.AuthClockSkew > 2*time.Minute {
		return fmt.Errorf("AUTH_CLOCK_SKEW must be between 0 and 2m")
	}
	if config.AuthHTTPTimeout <= 0 || config.AuthHTTPTimeout > 10*time.Second {
		return fmt.Errorf("AUTH_JWKS_HTTP_TIMEOUT must be greater than zero and at most 10s")
	}
	return nil
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func containsPlaceholder(value string) bool {
	lower := strings.ToLower(value)
	for _, marker := range []string{
		"your-project-ref", "replace-with", "changeme", "placeholder", "finance_dev_only", "example.test",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}
