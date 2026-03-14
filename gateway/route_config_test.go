package gateway

import (
	"testing"

	"github.com/cockroachdb/pebble"
	"go.uber.org/zap/zaptest"
)

// openTestStore opens a temporary Pebble-backed MessageStore for testing.
func openTestStore(t *testing.T) *MessageStore {
	t.Helper()
	dir := t.TempDir()
	logger := zaptest.NewLogger(t)
	store, err := NewMessageStore(dir, logger)
	if err != nil {
		t.Fatalf("NewMessageStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

// ---------------------------------------------------------------------------
// MT Route tests
// ---------------------------------------------------------------------------

func TestRouteConfig_MTRoute_SaveAndLoad(t *testing.T) {
	store := openTestStore(t)
	rc := NewRouteConfigStore(store)

	route1 := &MTRoute{
		Prefix:   "+234",
		Strategy: "failover",
		Pools:    []RoutePool{{Name: "ng-pool", Cost: 0.05}},
	}
	route2 := &MTRoute{
		Prefix:   "+1",
		Strategy: "round_robin",
		Pools:    []RoutePool{{Name: "us-pool-a"}, {Name: "us-pool-b"}},
	}

	if err := rc.SaveMTRoute(route1); err != nil {
		t.Fatalf("SaveMTRoute route1: %v", err)
	}
	if err := rc.SaveMTRoute(route2); err != nil {
		t.Fatalf("SaveMTRoute route2: %v", err)
	}

	routes, err := rc.LoadAllMTRoutes()
	if err != nil {
		t.Fatalf("LoadAllMTRoutes: %v", err)
	}
	if len(routes) != 2 {
		t.Fatalf("expected 2 MT routes, got %d", len(routes))
	}

	// Build a map for easy lookup (order is by key sort, not insertion order).
	byPrefix := map[string]*MTRoute{}
	for _, r := range routes {
		byPrefix[r.Prefix] = r
	}

	r1, ok := byPrefix["+234"]
	if !ok {
		t.Fatal("expected +234 route")
	}
	if r1.Strategy != "failover" {
		t.Errorf("expected strategy failover, got %q", r1.Strategy)
	}
	if len(r1.Pools) != 1 || r1.Pools[0].Name != "ng-pool" {
		t.Errorf("unexpected pools for +234: %+v", r1.Pools)
	}

	r2, ok := byPrefix["+1"]
	if !ok {
		t.Fatal("expected +1 route")
	}
	if r2.Strategy != "round_robin" {
		t.Errorf("expected strategy round_robin, got %q", r2.Strategy)
	}
	if len(r2.Pools) != 2 {
		t.Errorf("expected 2 pools for +1, got %d", len(r2.Pools))
	}
}

func TestRouteConfig_MTRoute_Overwrite(t *testing.T) {
	store := openTestStore(t)
	rc := NewRouteConfigStore(store)

	original := &MTRoute{
		Prefix:   "+234",
		Strategy: "failover",
		Pools:    []RoutePool{{Name: "old-pool"}},
	}
	if err := rc.SaveMTRoute(original); err != nil {
		t.Fatalf("SaveMTRoute: %v", err)
	}

	updated := &MTRoute{
		Prefix:   "+234",
		Strategy: "round_robin",
		Pools:    []RoutePool{{Name: "new-pool-a"}, {Name: "new-pool-b"}},
	}
	if err := rc.SaveMTRoute(updated); err != nil {
		t.Fatalf("SaveMTRoute overwrite: %v", err)
	}

	routes, err := rc.LoadAllMTRoutes()
	if err != nil {
		t.Fatalf("LoadAllMTRoutes: %v", err)
	}
	if len(routes) != 1 {
		t.Fatalf("expected 1 MT route after overwrite, got %d", len(routes))
	}
	if routes[0].Strategy != "round_robin" {
		t.Errorf("expected updated strategy round_robin, got %q", routes[0].Strategy)
	}
	if len(routes[0].Pools) != 2 {
		t.Errorf("expected 2 pools after overwrite, got %d", len(routes[0].Pools))
	}
}

func TestRouteConfig_MTRoute_Delete(t *testing.T) {
	store := openTestStore(t)
	rc := NewRouteConfigStore(store)

	if err := rc.SaveMTRoute(&MTRoute{
		Prefix:   "+234",
		Strategy: "failover",
		Pools:    []RoutePool{{Name: "ng-pool"}},
	}); err != nil {
		t.Fatalf("SaveMTRoute: %v", err)
	}
	if err := rc.SaveMTRoute(&MTRoute{
		Prefix:   "+1",
		Strategy: "failover",
		Pools:    []RoutePool{{Name: "us-pool"}},
	}); err != nil {
		t.Fatalf("SaveMTRoute: %v", err)
	}

	// Delete one.
	if err := rc.DeleteMTRoute("+234"); err != nil {
		t.Fatalf("DeleteMTRoute: %v", err)
	}

	routes, err := rc.LoadAllMTRoutes()
	if err != nil {
		t.Fatalf("LoadAllMTRoutes: %v", err)
	}
	if len(routes) != 1 {
		t.Fatalf("expected 1 MT route after delete, got %d", len(routes))
	}
	if routes[0].Prefix != "+1" {
		t.Errorf("expected remaining route +1, got %q", routes[0].Prefix)
	}
}

func TestRouteConfig_MTRoute_DeleteNonExistent(t *testing.T) {
	store := openTestStore(t)
	rc := NewRouteConfigStore(store)

	// Deleting a non-existent key should not error (Pebble Delete is idempotent).
	if err := rc.DeleteMTRoute("+999"); err != nil {
		t.Fatalf("DeleteMTRoute non-existent: %v", err)
	}
}

func TestRouteConfig_MTRoute_LoadEmpty(t *testing.T) {
	store := openTestStore(t)
	rc := NewRouteConfigStore(store)

	routes, err := rc.LoadAllMTRoutes()
	if err != nil {
		t.Fatalf("LoadAllMTRoutes: %v", err)
	}
	if len(routes) != 0 {
		t.Errorf("expected 0 MT routes from empty store, got %d", len(routes))
	}
}

// ---------------------------------------------------------------------------
// MO Route tests
// ---------------------------------------------------------------------------

func TestRouteConfig_MORoute_SaveAndLoad(t *testing.T) {
	store := openTestStore(t)
	rc := NewRouteConfigStore(store)

	route1 := &MORoute{
		DestPattern:  "12345",
		SourcePrefix: "+2783",
		Target: MOTarget{
			Type:        "http",
			CallbackURL: "https://example.com/mo",
		},
		Priority: 20,
	}
	route2 := &MORoute{
		DestPattern: "67890",
		Target: MOTarget{
			Type:   "smpp",
			ConnID: "north-1",
		},
		Priority: 10,
	}

	if err := rc.SaveMORoute(route1); err != nil {
		t.Fatalf("SaveMORoute route1: %v", err)
	}
	if err := rc.SaveMORoute(route2); err != nil {
		t.Fatalf("SaveMORoute route2: %v", err)
	}

	routes, err := rc.LoadAllMORoutes()
	if err != nil {
		t.Fatalf("LoadAllMORoutes: %v", err)
	}
	if len(routes) != 2 {
		t.Fatalf("expected 2 MO routes, got %d", len(routes))
	}

	// Build a lookup by dest pattern.
	byDest := map[string]*MORoute{}
	for _, r := range routes {
		byDest[r.DestPattern] = r
	}

	r1, ok := byDest["12345"]
	if !ok {
		t.Fatal("expected 12345 route")
	}
	if r1.SourcePrefix != "+2783" {
		t.Errorf("expected source prefix +2783, got %q", r1.SourcePrefix)
	}
	if r1.Target.Type != "http" {
		t.Errorf("expected target type http, got %q", r1.Target.Type)
	}
	if r1.Target.CallbackURL != "https://example.com/mo" {
		t.Errorf("expected callback URL, got %q", r1.Target.CallbackURL)
	}
	if r1.Priority != 20 {
		t.Errorf("expected priority 20, got %d", r1.Priority)
	}

	r2, ok := byDest["67890"]
	if !ok {
		t.Fatal("expected 67890 route")
	}
	if r2.Target.Type != "smpp" {
		t.Errorf("expected target type smpp, got %q", r2.Target.Type)
	}
	if r2.Target.ConnID != "north-1" {
		t.Errorf("expected connID north-1, got %q", r2.Target.ConnID)
	}
}

func TestRouteConfig_MORoute_Delete(t *testing.T) {
	store := openTestStore(t)
	rc := NewRouteConfigStore(store)

	if err := rc.SaveMORoute(&MORoute{
		DestPattern:  "12345",
		SourcePrefix: "+2783",
		Target:       MOTarget{Type: "http", CallbackURL: "https://example.com/a"},
		Priority:     10,
	}); err != nil {
		t.Fatalf("SaveMORoute: %v", err)
	}
	if err := rc.SaveMORoute(&MORoute{
		DestPattern: "67890",
		Target:      MOTarget{Type: "smpp", ConnID: "north-1"},
		Priority:    5,
	}); err != nil {
		t.Fatalf("SaveMORoute: %v", err)
	}

	// Delete the first route.
	if err := rc.DeleteMORoute("12345", "+2783"); err != nil {
		t.Fatalf("DeleteMORoute: %v", err)
	}

	routes, err := rc.LoadAllMORoutes()
	if err != nil {
		t.Fatalf("LoadAllMORoutes: %v", err)
	}
	if len(routes) != 1 {
		t.Fatalf("expected 1 MO route after delete, got %d", len(routes))
	}
	if routes[0].DestPattern != "67890" {
		t.Errorf("expected remaining route 67890, got %q", routes[0].DestPattern)
	}
}

func TestRouteConfig_MORoute_LoadEmpty(t *testing.T) {
	store := openTestStore(t)
	rc := NewRouteConfigStore(store)

	routes, err := rc.LoadAllMORoutes()
	if err != nil {
		t.Fatalf("LoadAllMORoutes: %v", err)
	}
	if len(routes) != 0 {
		t.Errorf("expected 0 MO routes from empty store, got %d", len(routes))
	}
}

// ---------------------------------------------------------------------------
// Pool Config tests
// ---------------------------------------------------------------------------

func TestRouteConfig_PoolConfig_SaveAndLoad(t *testing.T) {
	store := openTestStore(t)
	rc := NewRouteConfigStore(store)

	cfg1 := &SouthboundPoolConfig{
		Name:        "ng-smsc",
		Host:        "smsc.ng.example.com",
		Port:        2775,
		SystemID:    "ngclient",
		Password:    "secret1",
		Connections: 4,
		WindowSize:  20,
	}
	cfg2 := &SouthboundPoolConfig{
		Name:        "za-smsc",
		Host:        "smsc.za.example.com",
		Port:        2776,
		SystemID:    "zaclient",
		Password:    "secret2",
		Connections: 2,
		WindowSize:  10,
		TLSEnabled:  true,
	}

	if err := rc.SavePoolConfig(cfg1); err != nil {
		t.Fatalf("SavePoolConfig cfg1: %v", err)
	}
	if err := rc.SavePoolConfig(cfg2); err != nil {
		t.Fatalf("SavePoolConfig cfg2: %v", err)
	}

	configs, err := rc.LoadAllPoolConfigs()
	if err != nil {
		t.Fatalf("LoadAllPoolConfigs: %v", err)
	}
	if len(configs) != 2 {
		t.Fatalf("expected 2 pool configs, got %d", len(configs))
	}

	byName := map[string]*SouthboundPoolConfig{}
	for _, c := range configs {
		byName[c.Name] = c
	}

	c1, ok := byName["ng-smsc"]
	if !ok {
		t.Fatal("expected ng-smsc config")
	}
	if c1.Host != "smsc.ng.example.com" {
		t.Errorf("expected host smsc.ng.example.com, got %q", c1.Host)
	}
	if c1.Port != 2775 {
		t.Errorf("expected port 2775, got %d", c1.Port)
	}
	if c1.Connections != 4 {
		t.Errorf("expected 4 connections, got %d", c1.Connections)
	}

	c2, ok := byName["za-smsc"]
	if !ok {
		t.Fatal("expected za-smsc config")
	}
	if !c2.TLSEnabled {
		t.Error("expected TLSEnabled=true for za-smsc")
	}
	if c2.SystemID != "zaclient" {
		t.Errorf("expected systemID zaclient, got %q", c2.SystemID)
	}
}

func TestRouteConfig_PoolConfig_Delete(t *testing.T) {
	store := openTestStore(t)
	rc := NewRouteConfigStore(store)

	if err := rc.SavePoolConfig(&SouthboundPoolConfig{
		Name: "pool-a", Host: "a.example.com", Port: 2775,
	}); err != nil {
		t.Fatalf("SavePoolConfig: %v", err)
	}
	if err := rc.SavePoolConfig(&SouthboundPoolConfig{
		Name: "pool-b", Host: "b.example.com", Port: 2776,
	}); err != nil {
		t.Fatalf("SavePoolConfig: %v", err)
	}

	// Delete pool-a.
	if err := rc.DeletePoolConfig("pool-a"); err != nil {
		t.Fatalf("DeletePoolConfig: %v", err)
	}

	configs, err := rc.LoadAllPoolConfigs()
	if err != nil {
		t.Fatalf("LoadAllPoolConfigs: %v", err)
	}
	if len(configs) != 1 {
		t.Fatalf("expected 1 pool config after delete, got %d", len(configs))
	}
	if configs[0].Name != "pool-b" {
		t.Errorf("expected remaining pool-b, got %q", configs[0].Name)
	}
}

func TestRouteConfig_PoolConfig_LoadEmpty(t *testing.T) {
	store := openTestStore(t)
	rc := NewRouteConfigStore(store)

	configs, err := rc.LoadAllPoolConfigs()
	if err != nil {
		t.Fatalf("LoadAllPoolConfigs: %v", err)
	}
	if len(configs) != 0 {
		t.Errorf("expected 0 pool configs from empty store, got %d", len(configs))
	}
}

// ---------------------------------------------------------------------------
// Generic helpers tests
// ---------------------------------------------------------------------------

func TestMessageStore_SetGetDeleteJSON(t *testing.T) {
	store := openTestStore(t)

	type testData struct {
		Name  string `json:"name"`
		Value int    `json:"value"`
	}

	// Set
	if err := store.SetJSON("test:key1", &testData{Name: "hello", Value: 42}); err != nil {
		t.Fatalf("SetJSON: %v", err)
	}

	// Get
	var result testData
	if err := store.GetJSON("test:key1", &result); err != nil {
		t.Fatalf("GetJSON: %v", err)
	}
	if result.Name != "hello" || result.Value != 42 {
		t.Errorf("unexpected result: %+v", result)
	}

	// Delete
	if err := store.DeleteKey("test:key1"); err != nil {
		t.Fatalf("DeleteKey: %v", err)
	}

	// Get after delete should return ErrNotFound.
	err := store.GetJSON("test:key1", &result)
	if err == nil {
		t.Fatal("expected error after delete")
	}
	if err != pebble.ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestMessageStore_ScanPrefix(t *testing.T) {
	store := openTestStore(t)

	// Write keys in two namespaces.
	_ = store.SetJSON("ns1:a", "alpha")
	_ = store.SetJSON("ns1:b", "bravo")
	_ = store.SetJSON("ns2:c", "charlie")

	var keys []string
	err := store.ScanPrefix("ns1:", func(key string, data []byte) error {
		keys = append(keys, key)
		return nil
	})
	if err != nil {
		t.Fatalf("ScanPrefix: %v", err)
	}
	if len(keys) != 2 {
		t.Fatalf("expected 2 keys with prefix ns1:, got %d: %v", len(keys), keys)
	}

	// Verify ns2: is not included.
	for _, k := range keys {
		if k == "ns2:c" {
			t.Error("ns2:c should not appear in ns1: prefix scan")
		}
	}
}

// ---------------------------------------------------------------------------
// Cross-namespace isolation
// ---------------------------------------------------------------------------

func TestRouteConfig_NamespaceIsolation(t *testing.T) {
	store := openTestStore(t)
	rc := NewRouteConfigStore(store)

	// Save one of each type.
	_ = rc.SaveMTRoute(&MTRoute{Prefix: "+234", Strategy: "failover", Pools: []RoutePool{{Name: "p1"}}})
	_ = rc.SaveMORoute(&MORoute{DestPattern: "12345", Target: MOTarget{Type: "http", CallbackURL: "https://example.com"}, Priority: 10})
	_ = rc.SavePoolConfig(&SouthboundPoolConfig{Name: "pool-x", Host: "x.example.com", Port: 2775})

	// Each Load should only return its own type.
	mtRoutes, _ := rc.LoadAllMTRoutes()
	if len(mtRoutes) != 1 {
		t.Errorf("expected 1 MT route, got %d", len(mtRoutes))
	}

	moRoutes, _ := rc.LoadAllMORoutes()
	if len(moRoutes) != 1 {
		t.Errorf("expected 1 MO route, got %d", len(moRoutes))
	}

	pools, _ := rc.LoadAllPoolConfigs()
	if len(pools) != 1 {
		t.Errorf("expected 1 pool config, got %d", len(pools))
	}
}
