package gateway

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/cockroachdb/pebble"
	"golang.org/x/crypto/bcrypt"
)

// SourceAddrMode controls how the gateway handles source addresses for a client.
type SourceAddrMode string

const (
	// SourceAddrModePassthrough forwards whatever source address the client sends (default).
	SourceAddrModePassthrough SourceAddrMode = "passthrough"
	// SourceAddrModeDefault uses DefaultSourceAddr when the client sends an empty source.
	SourceAddrModeDefault SourceAddrMode = "default"
	// SourceAddrModeOverride always replaces the source address with ForceSourceAddr.
	SourceAddrModeOverride SourceAddrMode = "override"
	// SourceAddrModeWhitelist allows only source addresses in AllowedSourceAddrs.
	// If the client sends an unlisted address, the submit is rejected.
	SourceAddrModeWhitelist SourceAddrMode = "whitelist"
)

// ConnectionConfig holds per-connection SMPP client configuration.
type ConnectionConfig struct {
	SystemID          string         `json:"system_id"`
	Password          string         `json:"password"`             // bcrypt hash
	Description       string         `json:"description"`
	Enabled           bool           `json:"enabled"`
	AllowedIPs        []string       `json:"allowed_ips"`          // empty = allow all
	MaxTPS            int            `json:"max_tps"`              // 0 = unlimited
	CostPerSMS        float64        `json:"cost_per_sms"`
	AllowedPrefixes   []string       `json:"allowed_prefixes"`     // empty = allow all destinations
	DefaultSourceAddr string         `json:"default_source_addr"`
	DefaultSourceTON  byte           `json:"default_source_ton"`
	DefaultSourceNPI  byte           `json:"default_source_npi"`
	SourceAddrMode    SourceAddrMode `json:"source_addr_mode"`     // passthrough, default, override, whitelist
	ForceSourceAddr   string         `json:"force_source_addr"`    // used when mode=override
	ForceSourceTON    byte           `json:"force_source_ton"`
	ForceSourceNPI    byte           `json:"force_source_npi"`
	AllowedSourceAddrs []string      `json:"allowed_source_addrs"` // used when mode=whitelist
	MaxBinds          int            `json:"max_binds"`            // 0 = unlimited
	AllowedBindModes  []string       `json:"allowed_bind_modes"`   // empty = all
	CreatedAt         time.Time      `json:"created_at"`
	UpdatedAt         time.Time      `json:"updated_at"`
}

// ConnectionConfigStore manages per-connection SMPP configurations in Pebble.
type ConnectionConfigStore struct {
	store *MessageStore
}

// NewConnectionConfigStore creates a new ConnectionConfigStore backed by the given MessageStore.
func NewConnectionConfigStore(store *MessageStore) *ConnectionConfigStore {
	return &ConnectionConfigStore{store: store}
}

// connConfigKey returns the Pebble key for a connection config.
func connConfigKey(systemID string) string {
	return "connconfig:" + systemID
}

// Create validates and stores a new connection config. The Password field
// must contain the plaintext password; it will be bcrypt-hashed before storage.
func (cs *ConnectionConfigStore) Create(cfg *ConnectionConfig) error {
	if cfg.SystemID == "" {
		return fmt.Errorf("system_id is required")
	}

	// Check uniqueness.
	existing := cs.store.GetJSON(connConfigKey(cfg.SystemID), &ConnectionConfig{})
	if existing == nil {
		return fmt.Errorf("connection config for system_id %q already exists", cfg.SystemID)
	}

	// Hash the plaintext password.
	if cfg.Password == "" {
		return fmt.Errorf("password is required")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(cfg.Password), 10)
	if err != nil {
		return fmt.Errorf("bcrypt hash: %w", err)
	}
	cfg.Password = string(hash)

	now := time.Now()
	cfg.CreatedAt = now
	cfg.UpdatedAt = now

	return cs.store.SetJSON(connConfigKey(cfg.SystemID), cfg)
}

// Get retrieves a connection config by system_id. Returns nil, nil if not found.
func (cs *ConnectionConfigStore) Get(systemID string) (*ConnectionConfig, error) {
	var cfg ConnectionConfig
	if err := cs.store.GetJSON(connConfigKey(systemID), &cfg); err != nil {
		if err == pebble.ErrNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &cfg, nil
}

// Update merges changes into an existing connection config. If cfg.Password
// is empty, the existing hash is preserved; otherwise the new plaintext
// password is bcrypt-hashed.
func (cs *ConnectionConfigStore) Update(cfg *ConnectionConfig) error {
	if cfg.SystemID == "" {
		return fmt.Errorf("system_id is required")
	}

	existing, err := cs.Get(cfg.SystemID)
	if err != nil {
		return err
	}
	if existing == nil {
		return fmt.Errorf("connection config for system_id %q not found", cfg.SystemID)
	}

	// If password is provided, re-hash; otherwise keep existing.
	if cfg.Password != "" {
		hash, hashErr := bcrypt.GenerateFromPassword([]byte(cfg.Password), 10)
		if hashErr != nil {
			return fmt.Errorf("bcrypt hash: %w", hashErr)
		}
		existing.Password = string(hash)
	}

	// Merge all other fields.
	existing.Description = cfg.Description
	existing.Enabled = cfg.Enabled
	existing.AllowedIPs = cfg.AllowedIPs
	existing.MaxTPS = cfg.MaxTPS
	existing.CostPerSMS = cfg.CostPerSMS
	existing.AllowedPrefixes = cfg.AllowedPrefixes
	existing.DefaultSourceAddr = cfg.DefaultSourceAddr
	existing.DefaultSourceTON = cfg.DefaultSourceTON
	existing.DefaultSourceNPI = cfg.DefaultSourceNPI
	existing.SourceAddrMode = cfg.SourceAddrMode
	existing.ForceSourceAddr = cfg.ForceSourceAddr
	existing.ForceSourceTON = cfg.ForceSourceTON
	existing.ForceSourceNPI = cfg.ForceSourceNPI
	existing.AllowedSourceAddrs = cfg.AllowedSourceAddrs
	existing.MaxBinds = cfg.MaxBinds
	existing.AllowedBindModes = cfg.AllowedBindModes
	existing.UpdatedAt = time.Now()

	return cs.store.SetJSON(connConfigKey(cfg.SystemID), existing)
}

// Delete removes a connection config by system_id.
func (cs *ConnectionConfigStore) Delete(systemID string) error {
	return cs.store.DeleteKey(connConfigKey(systemID))
}

// List returns all connection configs with Password fields cleared.
func (cs *ConnectionConfigStore) List() ([]*ConnectionConfig, error) {
	var configs []*ConnectionConfig
	err := cs.store.ScanPrefix("connconfig:", func(key string, data []byte) error {
		var cfg ConnectionConfig
		if err := json.Unmarshal(data, &cfg); err != nil {
			return nil // skip malformed
		}
		cfg.Password = "" // Don't expose hash in listings
		configs = append(configs, &cfg)
		return nil
	})
	return configs, err
}

// Authenticate loads the config for systemID and verifies the plaintext
// password. Returns (config, nil) on success, (nil, nil) if not found,
// and (nil, error) if the password is wrong or the account is disabled.
func (cs *ConnectionConfigStore) Authenticate(systemID, plainPassword string) (*ConnectionConfig, error) {
	cfg, err := cs.Get(systemID)
	if err != nil {
		return nil, err
	}
	if cfg == nil {
		return nil, nil
	}

	if !cfg.Enabled {
		return nil, fmt.Errorf("account %q is disabled", systemID)
	}

	if err := bcrypt.CompareHashAndPassword([]byte(cfg.Password), []byte(plainPassword)); err != nil {
		return nil, fmt.Errorf("invalid password for %q", systemID)
	}

	return cfg, nil
}
