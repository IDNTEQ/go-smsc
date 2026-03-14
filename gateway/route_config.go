package gateway

import (
	"encoding/json"
	"fmt"
)

// RouteConfigStore provides CRUD for MT routes, MO routes, and pool configs in Pebble.
type RouteConfigStore struct {
	store *MessageStore
}

// NewRouteConfigStore creates a RouteConfigStore backed by the given MessageStore.
func NewRouteConfigStore(store *MessageStore) *RouteConfigStore {
	return &RouteConfigStore{store: store}
}

// --- MT Routes ---

// SaveMTRoute persists an MT route keyed by its prefix.
func (rc *RouteConfigStore) SaveMTRoute(route *MTRoute) error {
	key := fmt.Sprintf("route:mt:%s", route.Prefix)
	return rc.store.SetJSON(key, route)
}

// DeleteMTRoute removes the MT route for the given prefix.
func (rc *RouteConfigStore) DeleteMTRoute(prefix string) error {
	key := fmt.Sprintf("route:mt:%s", prefix)
	return rc.store.DeleteKey(key)
}

// LoadAllMTRoutes returns all persisted MT routes.
func (rc *RouteConfigStore) LoadAllMTRoutes() ([]*MTRoute, error) {
	var routes []*MTRoute
	err := rc.store.ScanPrefix("route:mt:", func(key string, data []byte) error {
		var r MTRoute
		if err := json.Unmarshal(data, &r); err != nil {
			return err
		}
		routes = append(routes, &r)
		return nil
	})
	return routes, err
}

// --- MO Routes ---

// SaveMORoute persists an MO route keyed by dest pattern and source prefix.
func (rc *RouteConfigStore) SaveMORoute(route *MORoute) error {
	key := fmt.Sprintf("route:mo:%s:%s", route.DestPattern, route.SourcePrefix)
	return rc.store.SetJSON(key, route)
}

// DeleteMORoute removes the MO route for the given dest pattern and source prefix.
func (rc *RouteConfigStore) DeleteMORoute(destPattern, sourcePrefix string) error {
	key := fmt.Sprintf("route:mo:%s:%s", destPattern, sourcePrefix)
	return rc.store.DeleteKey(key)
}

// LoadAllMORoutes returns all persisted MO routes.
func (rc *RouteConfigStore) LoadAllMORoutes() ([]*MORoute, error) {
	var routes []*MORoute
	err := rc.store.ScanPrefix("route:mo:", func(key string, data []byte) error {
		var r MORoute
		if err := json.Unmarshal(data, &r); err != nil {
			return err
		}
		routes = append(routes, &r)
		return nil
	})
	return routes, err
}

// --- Pool Configs ---

// SavePoolConfig persists a southbound pool configuration.
func (rc *RouteConfigStore) SavePoolConfig(cfg *SouthboundPoolConfig) error {
	key := fmt.Sprintf("pool:%s", cfg.Name)
	return rc.store.SetJSON(key, cfg)
}

// DeletePoolConfig removes the pool configuration with the given name.
func (rc *RouteConfigStore) DeletePoolConfig(name string) error {
	key := fmt.Sprintf("pool:%s", name)
	return rc.store.DeleteKey(key)
}

// LoadAllPoolConfigs returns all persisted pool configurations.
func (rc *RouteConfigStore) LoadAllPoolConfigs() ([]*SouthboundPoolConfig, error) {
	var configs []*SouthboundPoolConfig
	err := rc.store.ScanPrefix("pool:", func(key string, data []byte) error {
		var c SouthboundPoolConfig
		if err := json.Unmarshal(data, &c); err != nil {
			return err
		}
		configs = append(configs, &c)
		return nil
	})
	return configs, err
}
