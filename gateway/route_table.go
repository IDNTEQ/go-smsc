package gateway

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/idnteq/go-smsc/smpp"
)

// MTRoute defines a route for outbound (MT) messages.
type MTRoute struct {
	Prefix   string      `json:"prefix"`   // MSISDN prefix to match, "*" = default
	Strategy string      `json:"strategy"` // failover, round_robin, least_cost
	Pools    []RoutePool `json:"pools"`
	rrIndex  atomic.Uint64 // for round-robin
}

// RoutePool references a named pool in the PoolManager.
type RoutePool struct {
	Name string  `json:"name"`
	Cost float64 `json:"cost,omitempty"` // for least_cost strategy
}

// MTRouteTable evaluates destination MSISDN against configured routes.
type MTRouteTable struct {
	routes []*MTRoute // sorted by prefix length descending (longest first)
	mu     sync.RWMutex
}

// NewMTRouteTable creates an empty route table.
func NewMTRouteTable() *MTRouteTable {
	return &MTRouteTable{}
}

// Resolve finds the best pool for the given MSISDN.
// It matches the longest prefix first, then applies the route's strategy
// (failover, round_robin, or least_cost) to select a healthy pool.
func (t *MTRouteTable) Resolve(msisdn string, pm *PoolManager) (*smpp.Pool, string, error) {
	t.mu.RLock()
	defer t.mu.RUnlock()

	for _, route := range t.routes {
		if route.Prefix != "*" && !strings.HasPrefix(msisdn, route.Prefix) {
			continue
		}
		// This route matches.
		pool, name, err := t.applyStrategy(route, pm)
		if err != nil {
			return nil, "", fmt.Errorf("route %q matched but %w", route.Prefix, err)
		}
		return pool, name, nil
	}
	return nil, "", fmt.Errorf("no route for MSISDN %q", msisdn)
}

// applyStrategy selects a healthy pool from the route according to its strategy.
func (t *MTRouteTable) applyStrategy(route *MTRoute, pm *PoolManager) (*smpp.Pool, string, error) {
	switch route.Strategy {
	case "failover":
		return t.resolveFailover(route, pm)
	case "round_robin":
		return t.resolveRoundRobin(route, pm)
	case "least_cost":
		return t.resolveLeastCost(route, pm)
	default:
		return nil, "", fmt.Errorf("unknown strategy %q", route.Strategy)
	}
}

// resolveFailover iterates pools in order and returns the first healthy one.
func (t *MTRouteTable) resolveFailover(route *MTRoute, pm *PoolManager) (*smpp.Pool, string, error) {
	for _, rp := range route.Pools {
		h := pm.Health(rp.Name)
		if h.Healthy {
			pool, ok := pm.Get(rp.Name)
			if ok {
				return pool, rp.Name, nil
			}
		}
	}
	return nil, "", fmt.Errorf("no healthy pool for failover route")
}

// resolveRoundRobin uses an atomic counter to distribute across healthy pools.
func (t *MTRouteTable) resolveRoundRobin(route *MTRoute, pm *PoolManager) (*smpp.Pool, string, error) {
	n := len(route.Pools)
	if n == 0 {
		return nil, "", fmt.Errorf("no pools configured for round_robin route")
	}

	idx := route.rrIndex.Add(1) - 1 // get current, then increment
	for i := 0; i < n; i++ {
		candidate := route.Pools[(int(idx)+i)%n]
		h := pm.Health(candidate.Name)
		if h.Healthy {
			pool, ok := pm.Get(candidate.Name)
			if ok {
				return pool, candidate.Name, nil
			}
		}
	}
	return nil, "", fmt.Errorf("no healthy pool for round_robin route")
}

// resolveLeastCost sorts pools by cost ascending and returns the cheapest healthy one.
func (t *MTRouteTable) resolveLeastCost(route *MTRoute, pm *PoolManager) (*smpp.Pool, string, error) {
	// Copy to avoid mutating the route's pool order.
	sorted := make([]RoutePool, len(route.Pools))
	copy(sorted, route.Pools)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Cost < sorted[j].Cost
	})

	for _, rp := range sorted {
		h := pm.Health(rp.Name)
		if h.Healthy {
			pool, ok := pm.Get(rp.Name)
			if ok {
				return pool, rp.Name, nil
			}
		}
	}
	return nil, "", fmt.Errorf("no healthy pool for least_cost route")
}

// AddRoute adds or replaces a route for the given prefix.
// Routes are sorted so the longest prefix is evaluated first; "*" is always last.
func (t *MTRouteTable) AddRoute(route *MTRoute) {
	t.mu.Lock()
	defer t.mu.Unlock()

	// Remove existing route with same prefix if any.
	t.removeByPrefixLocked(route.Prefix)
	t.routes = append(t.routes, route)

	// Sort: longest prefix first, "*" always last.
	sort.Slice(t.routes, func(i, j int) bool {
		if t.routes[i].Prefix == "*" {
			return false
		}
		if t.routes[j].Prefix == "*" {
			return true
		}
		return len(t.routes[i].Prefix) > len(t.routes[j].Prefix)
	})
}

// RemoveRoute removes the route with the given prefix. Returns true if found.
func (t *MTRouteTable) RemoveRoute(prefix string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.removeByPrefixLocked(prefix)
}

// ListRoutes returns a copy of all configured routes in evaluation order.
func (t *MTRouteTable) ListRoutes() []*MTRoute {
	t.mu.RLock()
	defer t.mu.RUnlock()
	result := make([]*MTRoute, len(t.routes))
	copy(result, t.routes)
	return result
}

// removeByPrefixLocked removes a route by prefix. Caller must hold t.mu.
func (t *MTRouteTable) removeByPrefixLocked(prefix string) bool {
	for i, r := range t.routes {
		if r.Prefix == prefix {
			t.routes = append(t.routes[:i], t.routes[i+1:]...)
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// MO (Mobile Originated) Route Table
// ---------------------------------------------------------------------------

// MORoute defines a route for inbound (MO) messages.
type MORoute struct {
	DestPattern  string   `json:"dest_pattern"`  // exact or prefix match on destination (shortcode/long number)
	SourcePrefix string   `json:"source_prefix"` // optional additional filter on source MSISDN
	Target       MOTarget `json:"target"`
	Priority     int      `json:"priority"` // higher = evaluated first
}

// MOTarget defines where to deliver an MO message.
type MOTarget struct {
	Type        string `json:"type"`         // "smpp" or "http"
	ConnID      string `json:"conn_id"`      // for type=smpp: northbound connection ID
	CallbackURL string `json:"callback_url"` // for type=http: webhook URL
}

// MORouteTable routes inbound MO messages to northbound targets.
type MORouteTable struct {
	routes []*MORoute // sorted by priority descending (highest first)
	mu     sync.RWMutex
}

// NewMORouteTable creates an empty MO route table.
func NewMORouteTable() *MORouteTable {
	return &MORouteTable{}
}

// Resolve finds the target for an inbound MO message.
// Returns nil, nil if no route matches (caller should fall back to MSISDN affinity).
func (t *MORouteTable) Resolve(sourceAddr, destAddr string) (*MOTarget, error) {
	t.mu.RLock()
	defer t.mu.RUnlock()

	for _, route := range t.routes {
		// Check destination pattern: trailing "*" means prefix match, otherwise exact.
		if strings.HasSuffix(route.DestPattern, "*") {
			prefix := strings.TrimSuffix(route.DestPattern, "*")
			if !strings.HasPrefix(destAddr, prefix) {
				continue
			}
		} else {
			if destAddr != route.DestPattern {
				continue
			}
		}

		// Check optional source prefix filter.
		if route.SourcePrefix != "" && !strings.HasPrefix(sourceAddr, route.SourcePrefix) {
			continue
		}

		// Both match — return a copy of the target.
		target := route.Target
		return &target, nil
	}

	// No match — caller falls back to MSISDN affinity.
	return nil, nil
}

// AddRoute adds an MO route and re-sorts by priority descending.
func (t *MORouteTable) AddRoute(route *MORoute) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.routes = append(t.routes, route)
	sort.Slice(t.routes, func(i, j int) bool {
		return t.routes[i].Priority > t.routes[j].Priority
	})
}

// RemoveRoute removes the MO route matching destPattern and sourcePrefix.
// Returns true if a route was removed.
func (t *MORouteTable) RemoveRoute(destPattern, sourcePrefix string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	for i, r := range t.routes {
		if r.DestPattern == destPattern && r.SourcePrefix == sourcePrefix {
			t.routes = append(t.routes[:i], t.routes[i+1:]...)
			return true
		}
	}
	return false
}

// ListRoutes returns a copy of all configured MO routes in evaluation order.
func (t *MORouteTable) ListRoutes() []*MORoute {
	t.mu.RLock()
	defer t.mu.RUnlock()
	result := make([]*MORoute, len(t.routes))
	copy(result, t.routes)
	return result
}
