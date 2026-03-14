package gateway

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"go.uber.org/zap"
	"github.com/idnteq/go-smsc/smpp"
)

// SouthboundPoolConfig defines a named southbound SMSC connection pool.
type SouthboundPoolConfig struct {
	Name                  string `json:"name"`
	Host                  string `json:"host"`
	Port                  int    `json:"port"`
	SystemID              string `json:"system_id"`
	Password              string `json:"password"`
	SourceAddr            string `json:"source_addr"`
	Connections           int    `json:"connections"`
	WindowSize            int    `json:"window_size"`
	TLSEnabled            bool   `json:"tls_enabled"`
	TLSInsecureSkipVerify bool   `json:"tls_insecure_skip_verify"`
	BindMode              string `json:"bind_mode"`         // "transceiver", "transmitter", "receiver"
	InterfaceVersion      string `json:"interface_version"` // "3.4" or "5.0"
}

// PoolHealth reports the health of a named pool.
type PoolHealth struct {
	Name              string `json:"name"`
	ActiveConnections int    `json:"active_connections"`
	Healthy           bool   `json:"healthy"`
}

// PoolManager manages multiple named smpp.Pool instances.
type PoolManager struct {
	pools   map[string]*smpp.Pool
	configs map[string]*SouthboundPoolConfig
	handler smpp.DeliverHandler
	logger  *zap.Logger
	mu      sync.RWMutex
}

// NewPoolManager creates a new PoolManager. The handler is shared across all
// pools and receives DLR/MO deliver_sm PDUs from every southbound connection.
func NewPoolManager(handler smpp.DeliverHandler, logger *zap.Logger) *PoolManager {
	return &PoolManager{
		pools:   make(map[string]*smpp.Pool),
		configs: make(map[string]*SouthboundPoolConfig),
		handler: handler,
		logger:  logger,
	}
}

// Add creates and connects a new named southbound pool. Returns an error if
// the name is already taken or if all connections in the pool fail to bind.
func (pm *PoolManager) Add(ctx context.Context, cfg *SouthboundPoolConfig) error {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	if _, exists := pm.pools[cfg.Name]; exists {
		return fmt.Errorf("pool %q already exists", cfg.Name)
	}

	// Map string bind mode to smpp.BindMode.
	var bindMode smpp.BindMode
	switch cfg.BindMode {
	case "transmitter":
		bindMode = smpp.BindTransmitter
	case "receiver":
		bindMode = smpp.BindReceiver
	default:
		bindMode = smpp.BindTransceiver
	}

	// Map string interface version to byte.
	var ifVersion byte
	switch cfg.InterfaceVersion {
	case "5.0":
		ifVersion = 0x50
	default:
		ifVersion = 0x34
	}

	smppCfg := smpp.Config{
		Host:                  cfg.Host,
		Port:                  cfg.Port,
		SystemID:              cfg.SystemID,
		Password:              cfg.Password,
		SourceAddr:            cfg.SourceAddr,
		SourceAddrTON:         0x05,
		SourceAddrNPI:         0x00,
		EnquireLinkSec:        30,
		TLSEnabled:            cfg.TLSEnabled,
		TLSInsecureSkipVerify: cfg.TLSInsecureSkipVerify,
		BindMode:              bindMode,
		InterfaceVersion:      ifVersion,
	}

	conns := cfg.Connections
	if conns <= 0 {
		conns = 2
	}
	window := cfg.WindowSize
	if window <= 0 {
		window = 10
	}

	poolCfg := smpp.PoolConfig{
		Connections:      conns,
		WindowSize:       window,
		DeliverWorkers:   16,
		DeliverQueueSize: 25000,
		SubmitTimeout:    60 * time.Second,
	}

	pool := smpp.NewPool(smppCfg, poolCfg, pm.handler, pm.logger.Named(cfg.Name))
	if err := pool.Connect(ctx); err != nil {
		return fmt.Errorf("connect pool %q: %w", cfg.Name, err)
	}

	pm.pools[cfg.Name] = pool
	pm.configs[cfg.Name] = cfg
	pm.logger.Info("southbound pool added",
		zap.String("name", cfg.Name),
		zap.String("host", cfg.Host),
		zap.Int("port", cfg.Port),
		zap.Int("connections", conns),
	)
	return nil
}

// Get returns the pool with the given name and true, or nil and false if not found.
func (pm *PoolManager) Get(name string) (*smpp.Pool, bool) {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	p, ok := pm.pools[name]
	return p, ok
}

// Remove closes and removes the named pool. Returns an error if not found.
func (pm *PoolManager) Remove(name string) error {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	p, ok := pm.pools[name]
	if !ok {
		return fmt.Errorf("pool %q not found", name)
	}

	_ = p.Close()
	delete(pm.pools, name)
	delete(pm.configs, name)
	pm.logger.Info("southbound pool removed", zap.String("name", name))
	return nil
}

// Health returns the health status of a named pool. If the pool does not
// exist, it returns an unhealthy PoolHealth.
func (pm *PoolManager) Health(name string) PoolHealth {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	p, ok := pm.pools[name]
	if !ok {
		return PoolHealth{Name: name, Healthy: false}
	}
	active := p.ActiveConnections()
	return PoolHealth{Name: name, ActiveConnections: active, Healthy: active > 0}
}

// AllHealth returns the health status of every managed pool.
func (pm *PoolManager) AllHealth() []PoolHealth {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	result := make([]PoolHealth, 0, len(pm.pools))
	for name, p := range pm.pools {
		active := p.ActiveConnections()
		result = append(result, PoolHealth{Name: name, ActiveConnections: active, Healthy: active > 0})
	}
	return result
}

// Names returns the names of all managed pools (unordered).
func (pm *PoolManager) Names() []string {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	names := make([]string, 0, len(pm.pools))
	for n := range pm.pools {
		names = append(names, n)
	}
	return names
}

// PoolNames returns a sorted list of all managed pool names.
func (pm *PoolManager) PoolNames() []string {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	names := make([]string, 0, len(pm.pools))
	for n := range pm.pools {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Close shuts down all managed pools and clears internal state.
func (pm *PoolManager) Close() {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	for name, p := range pm.pools {
		_ = p.Close()
		pm.logger.Info("southbound pool closed", zap.String("name", name))
	}
	pm.pools = make(map[string]*smpp.Pool)
	pm.configs = make(map[string]*SouthboundPoolConfig)
}
