package gateway

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds all SMSC Gateway configuration.
type Config struct {
	// Northbound (server) — accepts engine connections
	ListenAddr     string
	AllowedEngines []string // system_id whitelist (empty = allow all)
	ServerSystemID string   // system_id to present in bind_resp
	ServerPassword string   // password engines must present

	// Southbound (client) — connects to real/mock SMSC
	SMSCHost       string
	SMSCPort       int
	SMSCSystemID   string
	SMSCPassword   string
	SMSCSourceAddr string

	// Pool settings
	PoolConnections int
	PoolWindowSize  int

	// Store
	DataDir         string
	MaxMessages     int
	MessageTTL      time.Duration
	CleanupInterval time.Duration

	// Retry
	RetryInterval      time.Duration
	MaxRetryAge        time.Duration
	ReconnectGraceSec  int // seconds to wait for engine reconnect before fallback

	// Southbound submit retries
	MaxSubmitRetries      int           // max retries before synthetic failure DLR (0 = no retries)
	SubmitRetryInterval   time.Duration // how often to drain retry queue

	// Forwarding worker pool
	ForwardWorkers   int // goroutines for southbound forwarding (default 64)
	ForwardQueueSize int // bounded channel size (default 10000)

	// Drain limits
	RetryDrainLimit       int // max entries per retry drain tick (default 200)
	SubmitRetryDrainLimit int // max entries per submit retry drain tick (default 100)

	// Rate limiting
	RateLimitTPS int // max submits/sec per connection (0 = unlimited)

	// Blacklist
	BlacklistFile string // path to file with blacklisted MSISDNs (one per line)

	// Keepalive / stale detection
	EnquireLinkSec int // seconds between enquire_link to engines (0 = disabled)
	IdleTimeoutSec int // close connections idle longer than this (0 = disabled)

	// TLS
	TLSCertFile string
	TLSKeyFile  string

	// REST API + Admin UI
	HTTPAddr        string // address for REST API + Admin UI server (default ":8080")
	HTTPTLSCertFile string // TLS cert for HTTP server (defaults to TLSCertFile)
	HTTPTLSKeyFile  string // TLS key for HTTP server (defaults to TLSKeyFile)

	// Admin
	JWTSecret string // secret for signing JWT tokens

	// Shutdown
	DrainTimeoutSec int // max seconds to wait for in-flight during shutdown

	// Metrics
	MetricsAddr string
}

// LoadConfig reads configuration from environment variables.
func LoadConfig() Config {
	var allowed []string
	if v := getEnv("GW_ALLOWED_ENGINES", ""); v != "" {
		allowed = strings.Split(v, ",")
	}

	return Config{
		ListenAddr:     getEnv("GW_LISTEN_ADDR", ":2776"),
		AllowedEngines: allowed,
		ServerSystemID: getEnv("GW_SERVER_SYSTEM_ID", "smscgw"),
		ServerPassword: getEnv("GW_SERVER_PASSWORD", "password"),

		SMSCHost:       getEnv("GW_SMSC_HOST", "localhost"),
		SMSCPort:       getEnvInt("GW_SMSC_PORT", 2775),
		SMSCSystemID:   getEnv("GW_SMSC_SYSTEM_ID", "smppclient"),
		SMSCPassword:   getEnv("GW_SMSC_PASSWORD", "password"),
		SMSCSourceAddr: getEnv("GW_SMSC_SOURCE_ADDR", ""),

		PoolConnections: getEnvInt("GW_POOL_CONNECTIONS", 5),
		PoolWindowSize:  getEnvInt("GW_POOL_WINDOW_SIZE", 10),

		DataDir:         getEnv("GW_DATA_DIR", "/tmp/smscgw-data"),
		MaxMessages:     getEnvInt("GW_MAX_MESSAGES", 1000000),
		MessageTTL:      time.Duration(getEnvInt("GW_MESSAGE_TTL_HOURS", 24)) * time.Hour,
		CleanupInterval: time.Duration(getEnvInt("GW_CLEANUP_INTERVAL_MIN", 5)) * time.Minute,

		RetryInterval:     time.Duration(getEnvInt("GW_RETRY_INTERVAL_SEC", 5)) * time.Second,
		MaxRetryAge:       time.Duration(getEnvInt("GW_MAX_RETRY_AGE_MIN", 60)) * time.Minute,
		ReconnectGraceSec: getEnvInt("GW_RECONNECT_GRACE_SEC", 60),

		MaxSubmitRetries:    getEnvInt("GW_MAX_SUBMIT_RETRIES", 3),
		SubmitRetryInterval: time.Duration(getEnvInt("GW_SUBMIT_RETRY_INTERVAL_SEC", 10)) * time.Second,

		ForwardWorkers:        getEnvInt("GW_FORWARD_WORKERS", 64),
		ForwardQueueSize:      getEnvInt("GW_FORWARD_QUEUE_SIZE", 10000),
		RetryDrainLimit:       getEnvInt("GW_RETRY_DRAIN_LIMIT", 200),
		SubmitRetryDrainLimit: getEnvInt("GW_SUBMIT_RETRY_DRAIN_LIMIT", 100),

		RateLimitTPS: getEnvInt("GW_RATE_LIMIT_TPS", 0),

		BlacklistFile: getEnv("GW_BLACKLIST_FILE", ""),

		EnquireLinkSec: getEnvInt("GW_ENQUIRE_LINK_SEC", 30),
		IdleTimeoutSec: getEnvInt("GW_IDLE_TIMEOUT_SEC", 120),

		TLSCertFile: getEnv("GW_TLS_CERT", ""),
		TLSKeyFile:  getEnv("GW_TLS_KEY", ""),

		HTTPAddr:        getEnv("GW_HTTP_ADDR", ":8080"),
		HTTPTLSCertFile: getEnv("GW_HTTP_TLS_CERT", ""),
		HTTPTLSKeyFile:  getEnv("GW_HTTP_TLS_KEY", ""),

		JWTSecret: getEnv("GW_JWT_SECRET", ""),

		DrainTimeoutSec: getEnvInt("GW_DRAIN_TIMEOUT_SEC", 10),

		MetricsAddr: getEnv("GW_METRICS_ADDR", ":9090"),
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}
