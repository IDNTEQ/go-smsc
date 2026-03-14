package gateway

import (
	"context"
	"testing"
	"time"

	"go.uber.org/zap/zaptest"
)

// setupRouteTestPM creates a PoolManager with the given pool names, all pointing
// at the same mock SMSC. Returns the PoolManager and a cleanup function.
func setupRouteTestPM(t *testing.T, poolNames ...string) (*PoolManager, func()) {
	t.Helper()

	port, smscCleanup := startTestSMSC(t)
	logger := zaptest.NewLogger(t)
	pm := NewPoolManager(newTestHandler(), logger)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	for _, name := range poolNames {
		cfg := &SouthboundPoolConfig{
			Name:        name,
			Host:        "127.0.0.1",
			Port:        port,
			SystemID:    "testclient",
			Password:    "password",
			Connections: 1,
			WindowSize:  10,
		}
		if err := pm.Add(ctx, cfg); err != nil {
			t.Fatalf("Add pool %q: %v", name, err)
		}
	}

	return pm, func() {
		pm.Close()
		smscCleanup()
	}
}

func TestMTRouteTable_LongestPrefixMatch(t *testing.T) {
	pm, cleanup := setupRouteTestPM(t, "ng-pool", "ng-mtn-pool")
	defer cleanup()

	rt := NewMTRouteTable()
	rt.AddRoute(&MTRoute{
		Prefix:   "+234",
		Strategy: "failover",
		Pools:    []RoutePool{{Name: "ng-pool"}},
	})
	rt.AddRoute(&MTRoute{
		Prefix:   "+2347",
		Strategy: "failover",
		Pools:    []RoutePool{{Name: "ng-mtn-pool"}},
	})

	// +23471234567 should match the longer prefix "+2347".
	_, name, err := rt.Resolve("+23471234567", pm)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if name != "ng-mtn-pool" {
		t.Errorf("expected ng-mtn-pool, got %q", name)
	}

	// +23480000000 should match "+234" (not "+2347").
	_, name, err = rt.Resolve("+23480000000", pm)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if name != "ng-pool" {
		t.Errorf("expected ng-pool, got %q", name)
	}
}

func TestMTRouteTable_Failover(t *testing.T) {
	// Create two pools: pool-a connected (healthy), pool-b connected (healthy).
	pm, cleanup := setupRouteTestPM(t, "pool-a", "pool-b")
	defer cleanup()

	rt := NewMTRouteTable()
	rt.AddRoute(&MTRoute{
		Prefix:   "*",
		Strategy: "failover",
		Pools:    []RoutePool{{Name: "pool-a"}, {Name: "pool-b"}},
	})

	// Both healthy: should return pool-a (first in order).
	_, name, err := rt.Resolve("+1234", pm)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if name != "pool-a" {
		t.Errorf("expected pool-a, got %q", name)
	}

	// Remove pool-a to simulate unhealthy.
	if err := pm.Remove("pool-a"); err != nil {
		t.Fatalf("Remove pool-a: %v", err)
	}

	// Now should failover to pool-b.
	_, name, err = rt.Resolve("+1234", pm)
	if err != nil {
		t.Fatalf("Resolve after failover: %v", err)
	}
	if name != "pool-b" {
		t.Errorf("expected pool-b after failover, got %q", name)
	}
}

func TestMTRouteTable_Failover_AllUnhealthy(t *testing.T) {
	pm, cleanup := setupRouteTestPM(t, "pool-x")
	defer cleanup()

	rt := NewMTRouteTable()
	rt.AddRoute(&MTRoute{
		Prefix:   "*",
		Strategy: "failover",
		Pools:    []RoutePool{{Name: "pool-x"}},
	})

	// Remove the only pool to make it unhealthy.
	if err := pm.Remove("pool-x"); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	_, _, err := rt.Resolve("+1234", pm)
	if err == nil {
		t.Fatal("expected error when all pools are unhealthy")
	}
}

func TestMTRouteTable_RoundRobin(t *testing.T) {
	pm, cleanup := setupRouteTestPM(t, "rr-a", "rr-b", "rr-c")
	defer cleanup()

	rt := NewMTRouteTable()
	rt.AddRoute(&MTRoute{
		Prefix:   "*",
		Strategy: "round_robin",
		Pools: []RoutePool{
			{Name: "rr-a"},
			{Name: "rr-b"},
			{Name: "rr-c"},
		},
	})

	// Make 6 calls and verify distribution.
	counts := map[string]int{}
	for i := 0; i < 6; i++ {
		_, name, err := rt.Resolve("+1234", pm)
		if err != nil {
			t.Fatalf("Resolve #%d: %v", i, err)
		}
		counts[name]++
	}

	// Each pool should get exactly 2 calls (6 / 3).
	for _, poolName := range []string{"rr-a", "rr-b", "rr-c"} {
		if counts[poolName] != 2 {
			t.Errorf("expected pool %q to get 2 calls, got %d (counts: %v)", poolName, counts[poolName], counts)
		}
	}
}

func TestMTRouteTable_RoundRobin_SkipsUnhealthy(t *testing.T) {
	pm, cleanup := setupRouteTestPM(t, "rr-x", "rr-y")
	defer cleanup()

	rt := NewMTRouteTable()
	rt.AddRoute(&MTRoute{
		Prefix:   "*",
		Strategy: "round_robin",
		Pools: []RoutePool{
			{Name: "rr-x"},
			{Name: "rr-y"},
		},
	})

	// Remove rr-x to simulate unhealthy.
	if err := pm.Remove("rr-x"); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	// All calls should go to rr-y.
	for i := 0; i < 4; i++ {
		_, name, err := rt.Resolve("+1234", pm)
		if err != nil {
			t.Fatalf("Resolve #%d: %v", i, err)
		}
		if name != "rr-y" {
			t.Errorf("Resolve #%d: expected rr-y, got %q", i, name)
		}
	}
}

func TestMTRouteTable_LeastCost(t *testing.T) {
	pm, cleanup := setupRouteTestPM(t, "lc-cheap", "lc-mid", "lc-pricey")
	defer cleanup()

	rt := NewMTRouteTable()
	rt.AddRoute(&MTRoute{
		Prefix:   "*",
		Strategy: "least_cost",
		Pools: []RoutePool{
			{Name: "lc-pricey", Cost: 0.10},
			{Name: "lc-cheap", Cost: 0.01},
			{Name: "lc-mid", Cost: 0.05},
		},
	})

	_, name, err := rt.Resolve("+1234", pm)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if name != "lc-cheap" {
		t.Errorf("expected lc-cheap (lowest cost), got %q", name)
	}
}

func TestMTRouteTable_LeastCost_FallsBackIfCheapestUnhealthy(t *testing.T) {
	pm, cleanup := setupRouteTestPM(t, "lc-a", "lc-b")
	defer cleanup()

	rt := NewMTRouteTable()
	rt.AddRoute(&MTRoute{
		Prefix:   "*",
		Strategy: "least_cost",
		Pools: []RoutePool{
			{Name: "lc-a", Cost: 0.01}, // cheapest
			{Name: "lc-b", Cost: 0.05},
		},
	})

	// Remove cheapest.
	if err := pm.Remove("lc-a"); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	_, name, err := rt.Resolve("+1234", pm)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if name != "lc-b" {
		t.Errorf("expected lc-b (next cheapest healthy), got %q", name)
	}
}

func TestMTRouteTable_DefaultRoute(t *testing.T) {
	pm, cleanup := setupRouteTestPM(t, "default-pool", "ng-pool")
	defer cleanup()

	rt := NewMTRouteTable()
	rt.AddRoute(&MTRoute{
		Prefix:   "+234",
		Strategy: "failover",
		Pools:    []RoutePool{{Name: "ng-pool"}},
	})
	rt.AddRoute(&MTRoute{
		Prefix:   "*",
		Strategy: "failover",
		Pools:    []RoutePool{{Name: "default-pool"}},
	})

	// A US number should fall through to the default route.
	_, name, err := rt.Resolve("+12125551234", pm)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if name != "default-pool" {
		t.Errorf("expected default-pool, got %q", name)
	}

	// An NG number should match the +234 route.
	_, name, err = rt.Resolve("+2341234567", pm)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if name != "ng-pool" {
		t.Errorf("expected ng-pool, got %q", name)
	}
}

func TestMTRouteTable_NoMatch(t *testing.T) {
	pm, cleanup := setupRouteTestPM(t, "some-pool")
	defer cleanup()

	rt := NewMTRouteTable()
	rt.AddRoute(&MTRoute{
		Prefix:   "+234",
		Strategy: "failover",
		Pools:    []RoutePool{{Name: "some-pool"}},
	})

	// No default route and MSISDN doesn't match "+234".
	_, _, err := rt.Resolve("+12125551234", pm)
	if err == nil {
		t.Fatal("expected error for unmatched MSISDN")
	}
}

func TestMTRouteTable_AddRemoveList(t *testing.T) {
	rt := NewMTRouteTable()

	// Start empty.
	if routes := rt.ListRoutes(); len(routes) != 0 {
		t.Fatalf("expected 0 routes, got %d", len(routes))
	}

	// Add routes in arbitrary order.
	rt.AddRoute(&MTRoute{Prefix: "+234", Strategy: "failover", Pools: []RoutePool{{Name: "a"}}})
	rt.AddRoute(&MTRoute{Prefix: "*", Strategy: "failover", Pools: []RoutePool{{Name: "b"}}})
	rt.AddRoute(&MTRoute{Prefix: "+2347", Strategy: "failover", Pools: []RoutePool{{Name: "c"}}})
	rt.AddRoute(&MTRoute{Prefix: "+1", Strategy: "failover", Pools: []RoutePool{{Name: "d"}}})

	routes := rt.ListRoutes()
	if len(routes) != 4 {
		t.Fatalf("expected 4 routes, got %d", len(routes))
	}

	// Verify sort order: longest prefix first, "*" last.
	expectedOrder := []string{"+2347", "+234", "+1", "*"}
	for i, expected := range expectedOrder {
		if routes[i].Prefix != expected {
			t.Errorf("route[%d]: expected prefix %q, got %q", i, expected, routes[i].Prefix)
		}
	}

	// Replace an existing route (same prefix).
	rt.AddRoute(&MTRoute{Prefix: "+234", Strategy: "round_robin", Pools: []RoutePool{{Name: "e"}}})
	routes = rt.ListRoutes()
	if len(routes) != 4 {
		t.Fatalf("expected 4 routes after replace, got %d", len(routes))
	}
	// Find the +234 route and verify it was replaced.
	for _, r := range routes {
		if r.Prefix == "+234" {
			if r.Strategy != "round_robin" {
				t.Errorf("expected replaced route strategy round_robin, got %q", r.Strategy)
			}
			if len(r.Pools) != 1 || r.Pools[0].Name != "e" {
				t.Errorf("expected replaced route pool 'e', got %v", r.Pools)
			}
		}
	}

	// Remove a route.
	ok := rt.RemoveRoute("+2347")
	if !ok {
		t.Error("expected RemoveRoute to return true")
	}
	routes = rt.ListRoutes()
	if len(routes) != 3 {
		t.Fatalf("expected 3 routes after remove, got %d", len(routes))
	}

	// Remove non-existent.
	ok = rt.RemoveRoute("+9999")
	if ok {
		t.Error("expected RemoveRoute to return false for non-existent prefix")
	}

	// Verify remaining order.
	expectedOrder = []string{"+234", "+1", "*"}
	for i, expected := range expectedOrder {
		if routes[i].Prefix != expected {
			t.Errorf("route[%d] after remove: expected prefix %q, got %q", i, expected, routes[i].Prefix)
		}
	}
}

func TestMTRouteTable_UnknownStrategy(t *testing.T) {
	pm, cleanup := setupRouteTestPM(t, "pool-z")
	defer cleanup()

	rt := NewMTRouteTable()
	rt.AddRoute(&MTRoute{
		Prefix:   "*",
		Strategy: "invalid_strategy",
		Pools:    []RoutePool{{Name: "pool-z"}},
	})

	_, _, err := rt.Resolve("+1234", pm)
	if err == nil {
		t.Fatal("expected error for unknown strategy")
	}
}

// ---------------------------------------------------------------------------
// MO Route Table Tests
// ---------------------------------------------------------------------------

func TestMORouteTable_ExactMatch(t *testing.T) {
	rt := NewMORouteTable()
	rt.AddRoute(&MORoute{
		DestPattern: "12345",
		Target: MOTarget{
			Type:        "http",
			CallbackURL: "https://example.com/mo",
		},
		Priority: 10,
	})

	target, err := rt.Resolve("+27831234567", "12345")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if target == nil {
		t.Fatal("expected a target, got nil")
	}
	if target.CallbackURL != "https://example.com/mo" {
		t.Errorf("expected callback URL https://example.com/mo, got %q", target.CallbackURL)
	}

	// Non-matching destination should return nil.
	target, err = rt.Resolve("+27831234567", "99999")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if target != nil {
		t.Errorf("expected nil target for non-matching dest, got %+v", target)
	}
}

func TestMORouteTable_PrefixMatch(t *testing.T) {
	rt := NewMORouteTable()
	rt.AddRoute(&MORoute{
		DestPattern: "+27*",
		Target: MOTarget{
			Type:   "smpp",
			ConnID: "north-1",
		},
		Priority: 10,
	})

	target, err := rt.Resolve("+1234567890", "+27831234567")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if target == nil {
		t.Fatal("expected a target for prefix match, got nil")
	}
	if target.ConnID != "north-1" {
		t.Errorf("expected connID north-1, got %q", target.ConnID)
	}

	// Non-matching prefix.
	target, err = rt.Resolve("+1234567890", "+44123456")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if target != nil {
		t.Errorf("expected nil for non-matching prefix, got %+v", target)
	}
}

func TestMORouteTable_SourcePrefixFilter(t *testing.T) {
	rt := NewMORouteTable()
	rt.AddRoute(&MORoute{
		DestPattern:  "12345",
		SourcePrefix: "+2783",
		Target: MOTarget{
			Type:        "http",
			CallbackURL: "https://example.com/za-mo",
		},
		Priority: 10,
	})

	// Source matches prefix.
	target, err := rt.Resolve("+27831112222", "12345")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if target == nil {
		t.Fatal("expected target when source matches prefix, got nil")
	}
	if target.CallbackURL != "https://example.com/za-mo" {
		t.Errorf("expected za-mo callback, got %q", target.CallbackURL)
	}

	// Source does NOT match prefix — should return nil.
	target, err = rt.Resolve("+44771234567", "12345")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if target != nil {
		t.Errorf("expected nil when source prefix doesn't match, got %+v", target)
	}
}

func TestMORouteTable_PriorityOrdering(t *testing.T) {
	rt := NewMORouteTable()

	// Lower priority — generic catch-all for shortcode.
	rt.AddRoute(&MORoute{
		DestPattern: "12345",
		Target: MOTarget{
			Type:        "http",
			CallbackURL: "https://example.com/generic",
		},
		Priority: 5,
	})

	// Higher priority — specific source prefix override.
	rt.AddRoute(&MORoute{
		DestPattern:  "12345",
		SourcePrefix: "+2783",
		Target: MOTarget{
			Type:        "http",
			CallbackURL: "https://example.com/za-specific",
		},
		Priority: 20,
	})

	// Source matching the high-priority route should get the specific target.
	target, err := rt.Resolve("+27831112222", "12345")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if target == nil {
		t.Fatal("expected target, got nil")
	}
	if target.CallbackURL != "https://example.com/za-specific" {
		t.Errorf("expected za-specific callback (high priority), got %q", target.CallbackURL)
	}

	// Source NOT matching the high-priority source prefix should fall through to generic.
	target, err = rt.Resolve("+44771234567", "12345")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if target == nil {
		t.Fatal("expected generic target, got nil")
	}
	if target.CallbackURL != "https://example.com/generic" {
		t.Errorf("expected generic callback (low priority), got %q", target.CallbackURL)
	}
}

func TestMORouteTable_NoMatch(t *testing.T) {
	rt := NewMORouteTable()
	rt.AddRoute(&MORoute{
		DestPattern: "12345",
		Target: MOTarget{
			Type:        "http",
			CallbackURL: "https://example.com/mo",
		},
		Priority: 10,
	})

	target, err := rt.Resolve("+27831234567", "99999")
	if err != nil {
		t.Fatalf("Resolve: unexpected error %v", err)
	}
	if target != nil {
		t.Errorf("expected nil target for unmatched MO, got %+v", target)
	}
}

func TestMORouteTable_HTTPTarget(t *testing.T) {
	rt := NewMORouteTable()
	rt.AddRoute(&MORoute{
		DestPattern: "54321",
		Target: MOTarget{
			Type:        "http",
			CallbackURL: "https://webhook.example.com/inbound",
		},
		Priority: 10,
	})

	target, err := rt.Resolve("+1234567890", "54321")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if target == nil {
		t.Fatal("expected HTTP target, got nil")
	}
	if target.Type != "http" {
		t.Errorf("expected type http, got %q", target.Type)
	}
	if target.CallbackURL != "https://webhook.example.com/inbound" {
		t.Errorf("expected callback URL, got %q", target.CallbackURL)
	}
}

func TestMORouteTable_SMPPTarget(t *testing.T) {
	rt := NewMORouteTable()
	rt.AddRoute(&MORoute{
		DestPattern: "67890",
		Target: MOTarget{
			Type:   "smpp",
			ConnID: "engine-north-3",
		},
		Priority: 10,
	})

	target, err := rt.Resolve("+1234567890", "67890")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if target == nil {
		t.Fatal("expected SMPP target, got nil")
	}
	if target.Type != "smpp" {
		t.Errorf("expected type smpp, got %q", target.Type)
	}
	if target.ConnID != "engine-north-3" {
		t.Errorf("expected connID engine-north-3, got %q", target.ConnID)
	}
}

func TestMORouteTable_AddRemoveList(t *testing.T) {
	rt := NewMORouteTable()

	// Start empty.
	if routes := rt.ListRoutes(); len(routes) != 0 {
		t.Fatalf("expected 0 routes, got %d", len(routes))
	}

	// Add routes.
	rt.AddRoute(&MORoute{
		DestPattern: "12345",
		Target:      MOTarget{Type: "http", CallbackURL: "https://a.example.com"},
		Priority:    10,
	})
	rt.AddRoute(&MORoute{
		DestPattern:  "12345",
		SourcePrefix: "+2783",
		Target:       MOTarget{Type: "http", CallbackURL: "https://b.example.com"},
		Priority:     20,
	})
	rt.AddRoute(&MORoute{
		DestPattern: "+27*",
		Target:      MOTarget{Type: "smpp", ConnID: "north-1"},
		Priority:    5,
	})

	routes := rt.ListRoutes()
	if len(routes) != 3 {
		t.Fatalf("expected 3 routes, got %d", len(routes))
	}

	// Verify priority ordering: 20, 10, 5.
	expectedPriorities := []int{20, 10, 5}
	for i, expected := range expectedPriorities {
		if routes[i].Priority != expected {
			t.Errorf("route[%d]: expected priority %d, got %d", i, expected, routes[i].Priority)
		}
	}

	// Remove the route with destPattern "12345" and sourcePrefix "+2783".
	ok := rt.RemoveRoute("12345", "+2783")
	if !ok {
		t.Error("expected RemoveRoute to return true")
	}
	routes = rt.ListRoutes()
	if len(routes) != 2 {
		t.Fatalf("expected 2 routes after remove, got %d", len(routes))
	}

	// Remove non-existent route.
	ok = rt.RemoveRoute("99999", "")
	if ok {
		t.Error("expected RemoveRoute to return false for non-existent route")
	}

	// Verify remaining routes.
	if routes[0].DestPattern != "12345" || routes[0].Priority != 10 {
		t.Errorf("route[0] unexpected: %+v", routes[0])
	}
	if routes[1].DestPattern != "+27*" || routes[1].Priority != 5 {
		t.Errorf("route[1] unexpected: %+v", routes[1])
	}
}
