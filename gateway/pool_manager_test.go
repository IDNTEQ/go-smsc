package gateway

import (
	"context"
	"net"
	"testing"
	"time"

	"go.uber.org/zap/zaptest"

	"github.com/idnteq/go-smsc/mocksmsc"
	"github.com/idnteq/go-smsc/smpp"
)

// newTestHandler returns a no-op DeliverHandler suitable for tests.
func newTestHandler() smpp.DeliverHandler {
	return func(sourceAddr, destAddr string, esmClass byte, payload []byte) error {
		return nil
	}
}

// startTestSMSC starts a mock SMSC on a random port and returns the port and a
// cleanup function. The mock SMSC speaks enough SMPP to handle bind, submit_sm,
// enquire_link, and unbind.
func startTestSMSC(t *testing.T) (int, func()) {
	t.Helper()

	// Find a free port by briefly binding to :0.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("find free port: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()

	logger := zaptest.NewLogger(t)
	srv := mocksmsc.NewServer(mocksmsc.Config{
		Port:           port,
		DLRDelayMs:     60000, // effectively disable DLRs during tests
		DLRSuccessRate: 1.0,
	}, logger)

	if err := srv.Start(); err != nil {
		t.Fatalf("start mock SMSC on port %d: %v", port, err)
	}

	// Give the listener a moment to be ready.
	time.Sleep(50 * time.Millisecond)

	return port, func() { srv.Stop() }
}

func TestNewPoolManager(t *testing.T) {
	logger := zaptest.NewLogger(t)
	pm := NewPoolManager(newTestHandler(), logger)
	if pm == nil {
		t.Fatal("NewPoolManager returned nil")
	}
	if len(pm.pools) != 0 {
		t.Errorf("expected 0 pools, got %d", len(pm.pools))
	}
}

func TestGet_NonExistent(t *testing.T) {
	logger := zaptest.NewLogger(t)
	pm := NewPoolManager(newTestHandler(), logger)

	p, ok := pm.Get("does-not-exist")
	if ok {
		t.Error("expected ok=false for non-existent pool")
	}
	if p != nil {
		t.Error("expected nil pool for non-existent name")
	}
}

func TestRemove_NonExistent(t *testing.T) {
	logger := zaptest.NewLogger(t)
	pm := NewPoolManager(newTestHandler(), logger)

	err := pm.Remove("does-not-exist")
	if err == nil {
		t.Error("expected error when removing non-existent pool")
	}
}

func TestHealth_NonExistent(t *testing.T) {
	logger := zaptest.NewLogger(t)
	pm := NewPoolManager(newTestHandler(), logger)

	h := pm.Health("does-not-exist")
	if h.Healthy {
		t.Error("expected unhealthy for non-existent pool")
	}
	if h.Name != "does-not-exist" {
		t.Errorf("expected name 'does-not-exist', got %q", h.Name)
	}
	if h.ActiveConnections != 0 {
		t.Errorf("expected 0 active connections, got %d", h.ActiveConnections)
	}
}

func TestAllHealth_Empty(t *testing.T) {
	logger := zaptest.NewLogger(t)
	pm := NewPoolManager(newTestHandler(), logger)

	result := pm.AllHealth()
	if len(result) != 0 {
		t.Errorf("expected empty AllHealth, got %d entries", len(result))
	}
}

func TestNames_Empty(t *testing.T) {
	logger := zaptest.NewLogger(t)
	pm := NewPoolManager(newTestHandler(), logger)

	names := pm.Names()
	if len(names) != 0 {
		t.Errorf("expected empty Names, got %v", names)
	}
}

func TestClose_Empty(t *testing.T) {
	logger := zaptest.NewLogger(t)
	pm := NewPoolManager(newTestHandler(), logger)

	// Should not panic.
	pm.Close()

	if len(pm.pools) != 0 {
		t.Errorf("expected 0 pools after Close, got %d", len(pm.pools))
	}
}

func TestAdd_AndGet(t *testing.T) {
	port, cleanup := startTestSMSC(t)
	defer cleanup()

	logger := zaptest.NewLogger(t)
	pm := NewPoolManager(newTestHandler(), logger)
	defer pm.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cfg := &SouthboundPoolConfig{
		Name:        "test-pool",
		Host:        "127.0.0.1",
		Port:        port,
		SystemID:    "testclient",
		Password:    "password",
		SourceAddr:  "test",
		Connections: 1,
		WindowSize:  10,
	}

	if err := pm.Add(ctx, cfg); err != nil {
		t.Fatalf("Add: %v", err)
	}

	// Verify Get returns the pool.
	p, ok := pm.Get("test-pool")
	if !ok {
		t.Fatal("expected Get to find 'test-pool'")
	}
	if p == nil {
		t.Fatal("expected non-nil pool from Get")
	}
}

func TestAdd_Duplicate(t *testing.T) {
	port, cleanup := startTestSMSC(t)
	defer cleanup()

	logger := zaptest.NewLogger(t)
	pm := NewPoolManager(newTestHandler(), logger)
	defer pm.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cfg := &SouthboundPoolConfig{
		Name:        "dup-pool",
		Host:        "127.0.0.1",
		Port:        port,
		SystemID:    "testclient",
		Password:    "password",
		Connections: 1,
		WindowSize:  10,
	}

	if err := pm.Add(ctx, cfg); err != nil {
		t.Fatalf("first Add: %v", err)
	}

	// Second add with same name should fail.
	err := pm.Add(ctx, cfg)
	if err == nil {
		t.Fatal("expected error on duplicate Add")
	}
	if err.Error() != `pool "dup-pool" already exists` {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestAdd_ConnectionFailure(t *testing.T) {
	logger := zaptest.NewLogger(t)
	pm := NewPoolManager(newTestHandler(), logger)
	defer pm.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Point at a port with nothing listening — all connections should fail.
	cfg := &SouthboundPoolConfig{
		Name:        "bad-pool",
		Host:        "127.0.0.1",
		Port:        1, // almost certainly not listening
		SystemID:    "testclient",
		Password:    "password",
		Connections: 1,
		WindowSize:  10,
	}

	err := pm.Add(ctx, cfg)
	if err == nil {
		t.Fatal("expected error when connecting to non-existent SMSC")
	}

	// Pool should NOT have been added.
	_, ok := pm.Get("bad-pool")
	if ok {
		t.Error("pool should not exist after failed Add")
	}
}

func TestHealth_Connected(t *testing.T) {
	port, cleanup := startTestSMSC(t)
	defer cleanup()

	logger := zaptest.NewLogger(t)
	pm := NewPoolManager(newTestHandler(), logger)
	defer pm.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cfg := &SouthboundPoolConfig{
		Name:        "healthy-pool",
		Host:        "127.0.0.1",
		Port:        port,
		SystemID:    "testclient",
		Password:    "password",
		Connections: 1,
		WindowSize:  10,
	}

	if err := pm.Add(ctx, cfg); err != nil {
		t.Fatalf("Add: %v", err)
	}

	h := pm.Health("healthy-pool")
	if !h.Healthy {
		t.Error("expected pool to be healthy after successful Add")
	}
	if h.ActiveConnections < 1 {
		t.Errorf("expected at least 1 active connection, got %d", h.ActiveConnections)
	}
}

func TestAllHealth_WithPools(t *testing.T) {
	port, cleanup := startTestSMSC(t)
	defer cleanup()

	logger := zaptest.NewLogger(t)
	pm := NewPoolManager(newTestHandler(), logger)
	defer pm.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cfg := &SouthboundPoolConfig{
		Name:        "pool-a",
		Host:        "127.0.0.1",
		Port:        port,
		SystemID:    "testclient",
		Password:    "password",
		Connections: 1,
		WindowSize:  10,
	}

	if err := pm.Add(ctx, cfg); err != nil {
		t.Fatalf("Add pool-a: %v", err)
	}

	allHealth := pm.AllHealth()
	if len(allHealth) != 1 {
		t.Fatalf("expected 1 health entry, got %d", len(allHealth))
	}
	if allHealth[0].Name != "pool-a" {
		t.Errorf("expected name 'pool-a', got %q", allHealth[0].Name)
	}
	if !allHealth[0].Healthy {
		t.Error("expected pool-a to be healthy")
	}
}

func TestNames_WithPools(t *testing.T) {
	port, cleanup := startTestSMSC(t)
	defer cleanup()

	logger := zaptest.NewLogger(t)
	pm := NewPoolManager(newTestHandler(), logger)
	defer pm.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	for _, name := range []string{"alpha", "beta"} {
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
			t.Fatalf("Add %s: %v", name, err)
		}
	}

	names := pm.Names()
	if len(names) != 2 {
		t.Fatalf("expected 2 names, got %d: %v", len(names), names)
	}

	nameSet := make(map[string]bool)
	for _, n := range names {
		nameSet[n] = true
	}
	if !nameSet["alpha"] || !nameSet["beta"] {
		t.Errorf("expected names {alpha, beta}, got %v", names)
	}
}

func TestRemove_Existing(t *testing.T) {
	port, cleanup := startTestSMSC(t)
	defer cleanup()

	logger := zaptest.NewLogger(t)
	pm := NewPoolManager(newTestHandler(), logger)
	defer pm.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cfg := &SouthboundPoolConfig{
		Name:        "removable",
		Host:        "127.0.0.1",
		Port:        port,
		SystemID:    "testclient",
		Password:    "password",
		Connections: 1,
		WindowSize:  10,
	}

	if err := pm.Add(ctx, cfg); err != nil {
		t.Fatalf("Add: %v", err)
	}

	// Verify it exists.
	if _, ok := pm.Get("removable"); !ok {
		t.Fatal("expected pool to exist before Remove")
	}

	// Remove it.
	if err := pm.Remove("removable"); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	// Verify it's gone.
	if _, ok := pm.Get("removable"); ok {
		t.Error("expected pool to be gone after Remove")
	}

	// Health should report unhealthy for removed pool.
	h := pm.Health("removable")
	if h.Healthy {
		t.Error("expected unhealthy after Remove")
	}
}

func TestClose_WithPools(t *testing.T) {
	port, cleanup := startTestSMSC(t)
	defer cleanup()

	logger := zaptest.NewLogger(t)
	pm := NewPoolManager(newTestHandler(), logger)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cfg := &SouthboundPoolConfig{
		Name:        "closeable",
		Host:        "127.0.0.1",
		Port:        port,
		SystemID:    "testclient",
		Password:    "password",
		Connections: 1,
		WindowSize:  10,
	}

	if err := pm.Add(ctx, cfg); err != nil {
		t.Fatalf("Add: %v", err)
	}

	// Close all pools.
	pm.Close()

	if len(pm.Names()) != 0 {
		t.Error("expected 0 pools after Close")
	}
}
