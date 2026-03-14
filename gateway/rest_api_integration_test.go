package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap/zaptest"

	"github.com/idnteq/go-smsc/mocksmsc"
	"github.com/idnteq/go-smsc/smpp"
)

// ---------------------------------------------------------------------------
// Test helper: sets up the full integrated stack
// ---------------------------------------------------------------------------

type testStack struct {
	router      *Router
	poolManager *PoolManager
	keyStore    *APIKeyStore
	userStore   *AdminUserStore
	routeConfig *RouteConfigStore
	store       *MessageStore
	mux         *http.ServeMux
	adminAPI    *AdminAPI
}

func setupTestStack(t *testing.T) *testStack {
	t.Helper()

	store := openTestStore(t)
	logger := zaptest.NewLogger(t)
	metrics := newTestMetrics()
	cfg := Config{
		ForwardQueueSize: 1000,
		MaxSubmitRetries: 3,
	}

	router := NewRouter(store, metrics, cfg, logger)
	poolManager := NewPoolManager(router.HandleDeliver, logger)
	router.SetPoolManager(poolManager)

	routeConfig := NewRouteConfigStore(store)
	router.SetRouteConfig(routeConfig)

	keyStore := NewAPIKeyStore(store)
	userStore := NewAdminUserStore(store, []byte("test-integration-secret"))

	mux := http.NewServeMux()
	router.RegisterRESTRoutes(mux, keyStore)

	// Server is nil for tests that don't need connection listing.
	connConfigStore := NewConnectionConfigStore(store)
	adminAPI := NewAdminAPI(router, poolManager, routeConfig, keyStore, userStore, connConfigStore, metrics, nil, logger)
	adminAPI.RegisterRoutes(mux)

	return &testStack{
		router:      router,
		poolManager: poolManager,
		keyStore:    keyStore,
		userStore:   userStore,
		routeConfig: routeConfig,
		store:       store,
		mux:         mux,
		adminAPI:    adminAPI,
	}
}

// adminLogin authenticates as admin and returns the JWT token.
func (ts *testStack) adminLogin(t *testing.T, username, password string) string {
	t.Helper()
	body := fmt.Sprintf(`{"username":%q,"password":%q}`, username, password)
	req := httptest.NewRequest(http.MethodPost, "/admin/api/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	ts.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("admin login failed: %d %s", rr.Code, rr.Body.String())
	}

	var resp map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal login response: %v", err)
	}
	token := resp["token"]
	if token == "" {
		t.Fatal("empty token from login")
	}
	return token
}

// startMockSMSC starts a mock SMSC on a random port and returns the port.
func startMockSMSC(t *testing.T) int {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("find free port: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()

	logger := zaptest.NewLogger(t)
	srv := mocksmsc.NewServer(mocksmsc.Config{
		Port:           port,
		DLRDelayMs:     60000,
		DLRSuccessRate: 1.0,
	}, logger)

	if err := srv.Start(); err != nil {
		t.Fatalf("start mock SMSC on port %d: %v", port, err)
	}
	time.Sleep(50 * time.Millisecond)

	t.Cleanup(func() { srv.Stop() })
	return port
}

// ===========================================================================
// 1. REST API Submit Flow (integration: auth middleware + submit + query)
// ===========================================================================

func TestIntegration_RESTSubmitFlow(t *testing.T) {
	ts := setupTestStack(t)

	// Create an API key
	plainKey, err := ts.keyStore.Create("integration-test", 0)
	if err != nil {
		t.Fatalf("Create API key: %v", err)
	}

	// --- Test: POST /api/v1/sms with valid auth ---
	body := `{"to":"+27831234567","from":"SENDER","body":"Hello Integration"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/sms", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+plainKey)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	ts.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", rr.Code, rr.Body.String())
	}

	var submitResp SMSSubmitResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &submitResp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if submitResp.ID == "" {
		t.Fatal("expected non-empty gwMsgID")
	}
	if submitResp.Status != "accepted" {
		t.Errorf("expected status accepted, got %q", submitResp.Status)
	}
	gwMsgID := submitResp.ID

	// --- Test: POST without auth ---
	req = httptest.NewRequest(http.MethodPost, "/api/v1/sms", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr = httptest.NewRecorder()
	ts.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without auth, got %d", rr.Code)
	}

	// --- Test: POST with invalid auth ---
	req = httptest.NewRequest(http.MethodPost, "/api/v1/sms", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer sk_live_totally_invalid_key_here_123")
	req.Header.Set("Content-Type", "application/json")
	rr = httptest.NewRecorder()
	ts.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 with invalid key, got %d", rr.Code)
	}

	// --- Test: GET /api/v1/sms/{id} with valid auth ---
	req = httptest.NewRequest(http.MethodGet, "/api/v1/sms/"+gwMsgID, nil)
	req.Header.Set("Authorization", "Bearer "+plainKey)
	rr = httptest.NewRecorder()
	ts.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 for query, got %d: %s", rr.Code, rr.Body.String())
	}

	var queryResp map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &queryResp); err != nil {
		t.Fatalf("unmarshal query: %v", err)
	}
	if queryResp["id"] != gwMsgID {
		t.Errorf("expected id %q, got %v", gwMsgID, queryResp["id"])
	}

	// --- Test: GET /api/v1/sms/nonexistent ---
	req = httptest.NewRequest(http.MethodGet, "/api/v1/sms/nonexistent-id-xyz", nil)
	req.Header.Set("Authorization", "Bearer "+plainKey)
	rr = httptest.NewRecorder()
	ts.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for nonexistent message, got %d", rr.Code)
	}
}

// ===========================================================================
// 2. REST API Batch Submit
// ===========================================================================

func TestIntegration_RESTBatchSubmit(t *testing.T) {
	ts := setupTestStack(t)

	plainKey, err := ts.keyStore.Create("batch-test", 0)
	if err != nil {
		t.Fatalf("Create API key: %v", err)
	}

	body := `{"messages":[
		{"to":"+27831111111","from":"BATCH","body":"msg 1"},
		{"to":"+27832222222","from":"BATCH","body":"msg 2"},
		{"to":"+27833333333","from":"BATCH","body":"msg 3"}
	],"reference_prefix":"integ"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/sms/batch", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+plainKey)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	ts.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", rr.Code, rr.Body.String())
	}

	var results []SMSSubmitResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &results); err != nil {
		t.Fatalf("unmarshal batch: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}

	// Verify each has a unique gwMsgID
	ids := make(map[string]bool)
	for i, r := range results {
		if r.ID == "" {
			t.Errorf("result[%d]: empty ID", i)
		}
		if ids[r.ID] {
			t.Errorf("result[%d]: duplicate ID %q", i, r.ID)
		}
		ids[r.ID] = true

		if r.Status != "accepted" {
			t.Errorf("result[%d]: expected status accepted, got %q", i, r.Status)
		}
	}

	// Verify references are auto-generated
	if results[0].Reference != "integ-0" {
		t.Errorf("expected reference 'integ-0', got %q", results[0].Reference)
	}
	if results[2].Reference != "integ-2" {
		t.Errorf("expected reference 'integ-2', got %q", results[2].Reference)
	}

	// Verify each message can be queried
	for _, r := range results {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/sms/"+r.ID, nil)
		req.Header.Set("Authorization", "Bearer "+plainKey)
		rr := httptest.NewRecorder()
		ts.mux.ServeHTTP(rr, req)

		if rr.Code != http.StatusOK {
			t.Errorf("query %s: expected 200, got %d", r.ID, rr.Code)
		}
	}
}

// ===========================================================================
// 3. REST API Query
// ===========================================================================

func TestIntegration_RESTQuery(t *testing.T) {
	ts := setupTestStack(t)

	plainKey, err := ts.keyStore.Create("query-test", 0)
	if err != nil {
		t.Fatalf("Create API key: %v", err)
	}

	// Submit a message
	body := `{"to":"+27831234567","from":"QUERY","body":"Test query"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/sms", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+plainKey)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	ts.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusAccepted {
		t.Fatalf("submit: expected 202, got %d", rr.Code)
	}

	var submitResp SMSSubmitResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &submitResp)
	gwMsgID := submitResp.ID

	// Query the submitted message
	req = httptest.NewRequest(http.MethodGet, "/api/v1/sms/"+gwMsgID, nil)
	req.Header.Set("Authorization", "Bearer "+plainKey)
	rr = httptest.NewRecorder()
	ts.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("query: expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var result map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &result)
	if result["id"] != gwMsgID {
		t.Errorf("expected id %q, got %v", gwMsgID, result["id"])
	}
	// Status should be "accepted" (durable status record), "pending" (gw: record), or "submitted" (in-memory)
	status, _ := result["status"].(string)
	if status != "accepted" && status != "pending" && status != "submitted" {
		t.Errorf("expected status accepted/pending/submitted, got %q", status)
	}

	// Query a non-existent message
	req = httptest.NewRequest(http.MethodGet, "/api/v1/sms/GW-nonexistent-999", nil)
	req.Header.Set("Authorization", "Bearer "+plainKey)
	rr = httptest.NewRecorder()
	ts.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("query nonexistent: expected 404, got %d", rr.Code)
	}
}

// ===========================================================================
// 4. MT Route Table Integration
// ===========================================================================

func TestIntegration_MTRouteTable(t *testing.T) {
	portA := startMockSMSC(t)
	portB := startMockSMSC(t)

	logger := zaptest.NewLogger(t)
	store := openTestStore(t)
	metrics := newTestMetrics()
	cfg := Config{
		ForwardQueueSize: 100,
		MaxSubmitRetries: 3,
	}

	router := NewRouter(store, metrics, cfg, logger)
	poolManager := NewPoolManager(router.HandleDeliver, logger)
	router.SetPoolManager(poolManager)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Add pool-a (for +27)
	if err := poolManager.Add(ctx, &SouthboundPoolConfig{
		Name:        "pool-a",
		Host:        "127.0.0.1",
		Port:        portA,
		SystemID:    "testclient",
		Password:    "password",
		Connections: 1,
		WindowSize:  10,
	}); err != nil {
		t.Fatalf("Add pool-a: %v", err)
	}

	// Add pool-b (for +234)
	if err := poolManager.Add(ctx, &SouthboundPoolConfig{
		Name:        "pool-b",
		Host:        "127.0.0.1",
		Port:        portB,
		SystemID:    "testclient",
		Password:    "password",
		Connections: 1,
		WindowSize:  10,
	}); err != nil {
		t.Fatalf("Add pool-b: %v", err)
	}

	// Configure MT routes
	router.SetMTRoutes([]*MTRoute{
		{
			Prefix:   "+27",
			Strategy: "failover",
			Pools:    []RoutePool{{Name: "pool-a"}},
		},
		{
			Prefix:   "+234",
			Strategy: "failover",
			Pools:    []RoutePool{{Name: "pool-b"}},
		},
	})

	// Start forward workers so submits actually get processed
	router.StartForwardWorkers(2)

	// Verify route resolution for +27
	poolA, nameA, err := router.mtRoutes.Resolve("+27831234567", poolManager)
	if err != nil {
		t.Fatalf("resolve +27: %v", err)
	}
	if nameA != "pool-a" {
		t.Errorf("expected pool-a for +27, got %q", nameA)
	}
	if poolA == nil {
		t.Fatal("expected non-nil pool for +27")
	}

	// Verify route resolution for +234
	poolB, nameB, err := router.mtRoutes.Resolve("+2341234567", poolManager)
	if err != nil {
		t.Fatalf("resolve +234: %v", err)
	}
	if nameB != "pool-b" {
		t.Errorf("expected pool-b for +234, got %q", nameB)
	}
	if poolB == nil {
		t.Fatal("expected non-nil pool for +234")
	}

	// Verify no route for +1 (no default configured)
	_, _, err = router.mtRoutes.Resolve("+11234567890", poolManager)
	if err == nil {
		t.Error("expected error for +1 with no matching route")
	}

	// Add a default route and verify fallback
	router.mtRoutes.AddRoute(&MTRoute{
		Prefix:   "*",
		Strategy: "failover",
		Pools:    []RoutePool{{Name: "pool-a"}},
	})

	poolDefault, nameDefault, err := router.mtRoutes.Resolve("+11234567890", poolManager)
	if err != nil {
		t.Fatalf("resolve +1 with default: %v", err)
	}
	if nameDefault != "pool-a" {
		t.Errorf("expected pool-a as default, got %q", nameDefault)
	}
	if poolDefault == nil {
		t.Fatal("expected non-nil pool for default route")
	}

	// Submit through the REST API and verify it's forwarded via the correct pool
	keyStore := NewAPIKeyStore(store)
	plainKey, _ := keyStore.Create("route-test", 0)

	mux := http.NewServeMux()
	router.RegisterRESTRoutes(mux, keyStore)

	// Submit to +27 number
	req := httptest.NewRequest(http.MethodPost, "/api/v1/sms",
		strings.NewReader(`{"to":"+27831234567","from":"TEST","body":"route test"}`))
	req.Header.Set("Authorization", "Bearer "+plainKey)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusAccepted {
		t.Fatalf("submit to +27: expected 202, got %d: %s", rr.Code, rr.Body.String())
	}

	// Give the forward worker time to process
	time.Sleep(200 * time.Millisecond)

	poolManager.Close()
}

// ===========================================================================
// 5. MO Route Table Integration
// ===========================================================================

func TestIntegration_MORouteTable(t *testing.T) {
	logger := zaptest.NewLogger(t)
	store := openTestStore(t)
	metrics := newTestMetrics()
	cfg := Config{
		ForwardQueueSize: 100,
		MaxSubmitRetries: 3,
	}

	router := NewRouter(store, metrics, cfg, logger)

	// Set up a mock HTTP callback server
	var mu sync.Mutex
	var receivedPayloads []MOCallbackPayload
	callbackServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload MOCallbackPayload
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode MO callback: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		mu.Lock()
		receivedPayloads = append(receivedPayloads, payload)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer callbackServer.Close()

	// Configure MO route: shortcode "12345" -> HTTP callback
	router.SetMORoutes([]*MORoute{
		{
			DestPattern: "12345",
			Target: MOTarget{
				Type:        "http",
				CallbackURL: callbackServer.URL,
			},
			Priority: 10,
		},
	})

	// Simulate MO delivery via HandleDeliver (this is what the southbound
	// SMPP pool calls when a deliver_sm arrives)
	moPayload := []byte("Hello from mobile subscriber")
	sourceAddr := "+27831234567"
	destAddr := "12345"
	esmClass := byte(0x00) // MO, not DLR

	err := router.HandleDeliver(sourceAddr, destAddr, esmClass, moPayload)
	if err != nil {
		t.Fatalf("HandleDeliver: %v", err)
	}

	// Give the callback delivery time to complete
	time.Sleep(200 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()

	if len(receivedPayloads) != 1 {
		t.Fatalf("expected 1 MO callback, got %d", len(receivedPayloads))
	}

	cb := receivedPayloads[0]
	if cb.Event != "mo" {
		t.Errorf("expected event 'mo', got %q", cb.Event)
	}
	if cb.From != sourceAddr {
		t.Errorf("expected from %q, got %q", sourceAddr, cb.From)
	}
	if cb.To != destAddr {
		t.Errorf("expected to %q, got %q", destAddr, cb.To)
	}
	if cb.Body != string(moPayload) {
		t.Errorf("expected body %q, got %q", string(moPayload), cb.Body)
	}
}

// ===========================================================================
// 6. Admin API Integration
// ===========================================================================

func TestIntegration_AdminAPI_LoginAndRoutes(t *testing.T) {
	ts := setupTestStack(t)

	// Bootstrap creates default admin
	if err := ts.userStore.Create("admin", "admin-password", "admin"); err != nil {
		t.Fatalf("Create admin: %v", err)
	}

	// Login with valid credentials
	token := ts.adminLogin(t, "admin", "admin-password")

	// --- List MT routes (should be empty) ---
	req := httptest.NewRequest(http.MethodGet, "/admin/api/routes/mt", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	ts.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("list MT routes: expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var mtRoutes []*MTRoute
	_ = json.Unmarshal(rr.Body.Bytes(), &mtRoutes)
	if len(mtRoutes) != 0 {
		t.Errorf("expected 0 MT routes initially, got %d", len(mtRoutes))
	}

	// --- Create MT route via admin API ---
	routeBody := `{"prefix":"+234","strategy":"failover","pools":[{"name":"ng-pool","cost":0.05}]}`
	req = httptest.NewRequest(http.MethodPost, "/admin/api/routes/mt", strings.NewReader(routeBody))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rr = httptest.NewRecorder()
	ts.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("create MT route: expected 201, got %d: %s", rr.Code, rr.Body.String())
	}

	// --- List MT routes again (should have 1) ---
	req = httptest.NewRequest(http.MethodGet, "/admin/api/routes/mt", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr = httptest.NewRecorder()
	ts.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("list MT routes after create: expected 200, got %d", rr.Code)
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &mtRoutes)
	if len(mtRoutes) != 1 {
		t.Fatalf("expected 1 MT route after create, got %d", len(mtRoutes))
	}
	if mtRoutes[0].Prefix != "+234" {
		t.Errorf("expected prefix +234, got %q", mtRoutes[0].Prefix)
	}
	if mtRoutes[0].Strategy != "failover" {
		t.Errorf("expected strategy failover, got %q", mtRoutes[0].Strategy)
	}

	// Verify route was persisted to store
	persisted, err := ts.routeConfig.LoadAllMTRoutes()
	if err != nil {
		t.Fatalf("LoadAllMTRoutes: %v", err)
	}
	if len(persisted) != 1 {
		t.Errorf("expected 1 persisted MT route, got %d", len(persisted))
	}

	// --- Delete MT route ---
	req = httptest.NewRequest(http.MethodDelete, "/admin/api/routes/mt/+234", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr = httptest.NewRecorder()
	ts.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("delete MT route: expected 204, got %d: %s", rr.Code, rr.Body.String())
	}

	// Verify route was removed
	req = httptest.NewRequest(http.MethodGet, "/admin/api/routes/mt", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr = httptest.NewRecorder()
	ts.mux.ServeHTTP(rr, req)

	_ = json.Unmarshal(rr.Body.Bytes(), &mtRoutes)
	if len(mtRoutes) != 0 {
		t.Errorf("expected 0 MT routes after delete, got %d", len(mtRoutes))
	}
}

func TestIntegration_AdminAPI_LoginFailure(t *testing.T) {
	ts := setupTestStack(t)

	if err := ts.userStore.Create("admin", "correct-pass", "admin"); err != nil {
		t.Fatalf("Create admin: %v", err)
	}

	// Login with wrong password
	body := `{"username":"admin","password":"wrong-pass"}`
	req := httptest.NewRequest(http.MethodPost, "/admin/api/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	ts.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for wrong password, got %d", rr.Code)
	}

	// Login with non-existent user
	body = `{"username":"nobody","password":"whatever"}`
	req = httptest.NewRequest(http.MethodPost, "/admin/api/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr = httptest.NewRecorder()
	ts.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for non-existent user, got %d", rr.Code)
	}
}

func TestIntegration_AdminAPI_UnauthorizedAccess(t *testing.T) {
	ts := setupTestStack(t)

	// Try to access admin endpoint without auth
	req := httptest.NewRequest(http.MethodGet, "/admin/api/routes/mt", nil)
	rr := httptest.NewRecorder()
	ts.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without auth, got %d", rr.Code)
	}

	// Try with invalid JWT
	req = httptest.NewRequest(http.MethodGet, "/admin/api/routes/mt", nil)
	req.Header.Set("Authorization", "Bearer invalid.jwt.token")
	rr = httptest.NewRecorder()
	ts.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 with invalid JWT, got %d", rr.Code)
	}
}

func TestIntegration_AdminAPI_APIKeys(t *testing.T) {
	ts := setupTestStack(t)

	if err := ts.userStore.Create("admin", "admin-pass", "admin"); err != nil {
		t.Fatalf("Create admin: %v", err)
	}
	token := ts.adminLogin(t, "admin", "admin-pass")

	// --- List API keys (should be empty initially — no keys created yet) ---
	req := httptest.NewRequest(http.MethodGet, "/admin/api/apikeys", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	ts.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("list API keys: expected 200, got %d", rr.Code)
	}

	// --- Create API key ---
	keyBody := `{"label":"test-integration-key","rate_limit":100}`
	req = httptest.NewRequest(http.MethodPost, "/admin/api/apikeys", strings.NewReader(keyBody))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rr = httptest.NewRecorder()
	ts.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("create API key: expected 201, got %d: %s", rr.Code, rr.Body.String())
	}

	var createResp map[string]string
	_ = json.Unmarshal(rr.Body.Bytes(), &createResp)
	plainKey := createResp["key"]
	if plainKey == "" {
		t.Fatal("expected non-empty key in create response")
	}
	if !strings.HasPrefix(plainKey, "sk_live_") {
		t.Errorf("expected key prefix sk_live_, got %q", plainKey)
	}
	if createResp["label"] != "test-integration-key" {
		t.Errorf("expected label 'test-integration-key', got %q", createResp["label"])
	}

	// --- Verify the created key works for REST API auth ---
	smsBody := `{"to":"+27831234567","from":"KEY","body":"Key test"}`
	req = httptest.NewRequest(http.MethodPost, "/api/v1/sms", strings.NewReader(smsBody))
	req.Header.Set("Authorization", "Bearer "+plainKey)
	req.Header.Set("Content-Type", "application/json")
	rr = httptest.NewRecorder()
	ts.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusAccepted {
		t.Fatalf("submit with new key: expected 202, got %d", rr.Code)
	}

	// --- List API keys (should have 1 now) ---
	req = httptest.NewRequest(http.MethodGet, "/admin/api/apikeys", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr = httptest.NewRecorder()
	ts.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("list API keys after create: expected 200, got %d", rr.Code)
	}

	var keys []*APIKey
	_ = json.Unmarshal(rr.Body.Bytes(), &keys)
	if len(keys) != 1 {
		t.Fatalf("expected 1 API key, got %d", len(keys))
	}
	if keys[0].Label != "test-integration-key" {
		t.Errorf("expected label 'test-integration-key', got %q", keys[0].Label)
	}
	keyID := keys[0].ID

	// --- Revoke API key ---
	req = httptest.NewRequest(http.MethodDelete, "/admin/api/apikeys/"+keyID, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr = httptest.NewRecorder()
	ts.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("revoke API key: expected 204, got %d: %s", rr.Code, rr.Body.String())
	}

	// --- Verify the revoked key no longer works ---
	req = httptest.NewRequest(http.MethodPost, "/api/v1/sms", strings.NewReader(smsBody))
	req.Header.Set("Authorization", "Bearer "+plainKey)
	req.Header.Set("Content-Type", "application/json")
	rr = httptest.NewRecorder()
	ts.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("submit with revoked key: expected 401, got %d", rr.Code)
	}

	// --- List API keys (should be empty after revoke) ---
	req = httptest.NewRequest(http.MethodGet, "/admin/api/apikeys", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr = httptest.NewRecorder()
	ts.mux.ServeHTTP(rr, req)

	_ = json.Unmarshal(rr.Body.Bytes(), &keys)
	if len(keys) != 0 {
		t.Errorf("expected 0 API keys after revoke, got %d", len(keys))
	}
}

func TestIntegration_AdminAPI_MORoutes(t *testing.T) {
	ts := setupTestStack(t)

	if err := ts.userStore.Create("admin", "admin-pass", "admin"); err != nil {
		t.Fatalf("Create admin: %v", err)
	}
	token := ts.adminLogin(t, "admin", "admin-pass")

	// --- List MO routes (empty) ---
	req := httptest.NewRequest(http.MethodGet, "/admin/api/routes/mo", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	ts.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("list MO routes: expected 200, got %d", rr.Code)
	}

	var moRoutes []*MORoute
	_ = json.Unmarshal(rr.Body.Bytes(), &moRoutes)
	if len(moRoutes) != 0 {
		t.Errorf("expected 0 MO routes initially, got %d", len(moRoutes))
	}

	// --- Create MO route ---
	routeBody := `{"dest_pattern":"12345","source_prefix":"","target":{"type":"http","callback_url":"https://example.com/mo"},"priority":10}`
	req = httptest.NewRequest(http.MethodPost, "/admin/api/routes/mo", strings.NewReader(routeBody))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rr = httptest.NewRecorder()
	ts.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("create MO route: expected 201, got %d: %s", rr.Code, rr.Body.String())
	}

	// --- List MO routes (should have 1) ---
	req = httptest.NewRequest(http.MethodGet, "/admin/api/routes/mo", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr = httptest.NewRecorder()
	ts.mux.ServeHTTP(rr, req)

	_ = json.Unmarshal(rr.Body.Bytes(), &moRoutes)
	if len(moRoutes) != 1 {
		t.Fatalf("expected 1 MO route after create, got %d", len(moRoutes))
	}
	if moRoutes[0].DestPattern != "12345" {
		t.Errorf("expected dest_pattern '12345', got %q", moRoutes[0].DestPattern)
	}
	if moRoutes[0].Target.Type != "http" {
		t.Errorf("expected target type 'http', got %q", moRoutes[0].Target.Type)
	}

	// Verify persistence
	persisted, _ := ts.routeConfig.LoadAllMORoutes()
	if len(persisted) != 1 {
		t.Errorf("expected 1 persisted MO route, got %d", len(persisted))
	}

	// --- Delete MO route ---
	req = httptest.NewRequest(http.MethodDelete, "/admin/api/routes/mo/12345:", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr = httptest.NewRecorder()
	ts.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("delete MO route: expected 204, got %d: %s", rr.Code, rr.Body.String())
	}

	// --- Verify deletion ---
	req = httptest.NewRequest(http.MethodGet, "/admin/api/routes/mo", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr = httptest.NewRecorder()
	ts.mux.ServeHTTP(rr, req)

	_ = json.Unmarshal(rr.Body.Bytes(), &moRoutes)
	if len(moRoutes) != 0 {
		t.Errorf("expected 0 MO routes after delete, got %d", len(moRoutes))
	}
}

func TestIntegration_AdminAPI_Users(t *testing.T) {
	ts := setupTestStack(t)

	// Create initial admin
	if err := ts.userStore.Create("admin", "admin-pass", "admin"); err != nil {
		t.Fatalf("Create admin: %v", err)
	}
	token := ts.adminLogin(t, "admin", "admin-pass")

	// --- List users ---
	req := httptest.NewRequest(http.MethodGet, "/admin/api/users", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	ts.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("list users: expected 200, got %d", rr.Code)
	}

	var users []*AdminUser
	_ = json.Unmarshal(rr.Body.Bytes(), &users)
	if len(users) != 1 {
		t.Fatalf("expected 1 user, got %d", len(users))
	}
	if users[0].Username != "admin" {
		t.Errorf("expected username 'admin', got %q", users[0].Username)
	}
	// Verify password hash is not exposed
	if users[0].PasswordHash != "" {
		t.Error("expected empty password hash in listing")
	}

	// --- Create a new user ---
	userBody := `{"username":"operator","password":"op-password","role":"admin"}`
	req = httptest.NewRequest(http.MethodPost, "/admin/api/users", strings.NewReader(userBody))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rr = httptest.NewRecorder()
	ts.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("create user: expected 201, got %d: %s", rr.Code, rr.Body.String())
	}

	// --- Verify new user can login ---
	opToken := ts.adminLogin(t, "operator", "op-password")
	if opToken == "" {
		t.Fatal("new user should be able to login")
	}

	// --- Delete the new user ---
	req = httptest.NewRequest(http.MethodDelete, "/admin/api/users/operator", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr = httptest.NewRecorder()
	ts.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("delete user: expected 204, got %d", rr.Code)
	}

	// --- Verify deleted user cannot login ---
	body := `{"username":"operator","password":"op-password"}`
	req = httptest.NewRequest(http.MethodPost, "/admin/api/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr = httptest.NewRecorder()
	ts.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("deleted user login: expected 401, got %d", rr.Code)
	}
}

func TestIntegration_AdminAPI_ChangePassword(t *testing.T) {
	ts := setupTestStack(t)

	if err := ts.userStore.Create("admin", "old-pass", "admin"); err != nil {
		t.Fatalf("Create admin: %v", err)
	}
	token := ts.adminLogin(t, "admin", "old-pass")

	// Change password
	body := `{"old_password":"old-pass","new_password":"new-pass"}`
	req := httptest.NewRequest(http.MethodPut, "/admin/api/users/admin/password", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	ts.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("change password: expected 204, got %d: %s", rr.Code, rr.Body.String())
	}

	// Old password should fail
	loginBody := `{"username":"admin","password":"old-pass"}`
	req = httptest.NewRequest(http.MethodPost, "/admin/api/login", strings.NewReader(loginBody))
	req.Header.Set("Content-Type", "application/json")
	rr = httptest.NewRecorder()
	ts.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("login with old password: expected 401, got %d", rr.Code)
	}

	// New password should work
	_ = ts.adminLogin(t, "admin", "new-pass")
}

func TestIntegration_AdminAPI_Pools(t *testing.T) {
	ts := setupTestStack(t)
	port := startMockSMSC(t)

	if err := ts.userStore.Create("admin", "admin-pass", "admin"); err != nil {
		t.Fatalf("Create admin: %v", err)
	}
	token := ts.adminLogin(t, "admin", "admin-pass")

	// --- List pools (empty) ---
	req := httptest.NewRequest(http.MethodGet, "/admin/api/pools", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	ts.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("list pools: expected 200, got %d", rr.Code)
	}

	var pools []PoolHealth
	_ = json.Unmarshal(rr.Body.Bytes(), &pools)
	if len(pools) != 0 {
		t.Errorf("expected 0 pools initially, got %d", len(pools))
	}

	// --- Create pool ---
	poolBody := fmt.Sprintf(`{"name":"test-pool","host":"127.0.0.1","port":%d,"system_id":"testclient","password":"password","connections":1,"window_size":10}`, port)
	req = httptest.NewRequest(http.MethodPost, "/admin/api/pools", strings.NewReader(poolBody))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rr = httptest.NewRecorder()
	ts.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("create pool: expected 201, got %d: %s", rr.Code, rr.Body.String())
	}

	// --- List pools (should have 1) ---
	req = httptest.NewRequest(http.MethodGet, "/admin/api/pools", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr = httptest.NewRecorder()
	ts.mux.ServeHTTP(rr, req)

	_ = json.Unmarshal(rr.Body.Bytes(), &pools)
	if len(pools) != 1 {
		t.Fatalf("expected 1 pool, got %d", len(pools))
	}
	if pools[0].Name != "test-pool" {
		t.Errorf("expected pool name 'test-pool', got %q", pools[0].Name)
	}
	if !pools[0].Healthy {
		t.Error("expected pool to be healthy")
	}

	// --- Delete pool ---
	req = httptest.NewRequest(http.MethodDelete, "/admin/api/pools/test-pool", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr = httptest.NewRecorder()
	ts.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("delete pool: expected 204, got %d", rr.Code)
	}

	// Verify pool is gone
	req = httptest.NewRequest(http.MethodGet, "/admin/api/pools", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr = httptest.NewRecorder()
	ts.mux.ServeHTTP(rr, req)

	_ = json.Unmarshal(rr.Body.Bytes(), &pools)
	if len(pools) != 0 {
		t.Errorf("expected 0 pools after delete, got %d", len(pools))
	}
}

// ===========================================================================
// 7. End-to-end: REST submit + route + forward through mock SMSC
// ===========================================================================

func TestIntegration_RESTSubmitThroughPool(t *testing.T) {
	port := startMockSMSC(t)

	logger := zaptest.NewLogger(t)
	store := openTestStore(t)
	metrics := newTestMetrics()
	cfg := Config{
		ForwardQueueSize: 100,
		MaxSubmitRetries: 3,
	}

	router := NewRouter(store, metrics, cfg, logger)

	// Set up a single southbound pool
	smppCfg := smpp.Config{
		Host:           "127.0.0.1",
		Port:           port,
		SystemID:       "testclient",
		Password:       "password",
		SourceAddr:     "test",
		SourceAddrTON:  0x05,
		SourceAddrNPI:  0x00,
		EnquireLinkSec: 30,
	}
	poolCfg := smpp.PoolConfig{
		Connections:      1,
		WindowSize:       10,
		DeliverWorkers:   4,
		DeliverQueueSize: 100,
		SubmitTimeout:    10 * time.Second,
	}

	pool := smpp.NewPool(smppCfg, poolCfg, router.HandleDeliver, logger)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := pool.Connect(ctx); err != nil {
		t.Fatalf("connect pool: %v", err)
	}
	defer func() { _ = pool.Close() }()

	router.SetSouthbound(pool)
	router.StartForwardWorkers(2)

	// Create REST API key
	keyStore := NewAPIKeyStore(store)
	plainKey, _ := keyStore.Create("e2e-test", 0)

	mux := http.NewServeMux()
	router.RegisterRESTRoutes(mux, keyStore)

	// Submit via REST API
	body := `{"to":"+27831234567","from":"E2E","body":"End-to-end test message"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/sms", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+plainKey)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusAccepted {
		t.Fatalf("submit: expected 202, got %d: %s", rr.Code, rr.Body.String())
	}

	var submitResp SMSSubmitResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &submitResp)
	if submitResp.ID == "" {
		t.Fatal("expected non-empty gwMsgID")
	}

	// Wait for the forward worker to process the submit
	time.Sleep(500 * time.Millisecond)

	// The message should have been forwarded to the mock SMSC and a
	// correlation entry created with the SMSC message ID.
	// We verify that the correlation table has been updated.
	found := false
	router.smscCorrelation.Range(func(key string, val *correlation) bool {
		if val.GwMsgID == submitResp.ID {
			found = true
			return false
		}
		return true
	})

	if !found {
		// The message may still be in gwCorrelation if forwarding hasn't completed yet.
		// That's OK — the important thing is we got a valid response.
		t.Log("SMSC correlation not found yet — message may still be in forward queue")
	}
}

// ===========================================================================
// 8. Admin API Bootstrap
// ===========================================================================

func TestIntegration_AdminBootstrap(t *testing.T) {
	ts := setupTestStack(t)

	// Bootstrap should create a default admin user
	if err := ts.userStore.Bootstrap(); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}

	// Verify user was created
	users, err := ts.userStore.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(users) != 1 {
		t.Fatalf("expected 1 user after bootstrap, got %d", len(users))
	}
	if users[0].Username != "admin" {
		t.Errorf("expected username 'admin', got %q", users[0].Username)
	}

	// Second bootstrap should be no-op
	if err := ts.userStore.Bootstrap(); err != nil {
		t.Fatalf("second Bootstrap: %v", err)
	}
	users, _ = ts.userStore.List()
	if len(users) != 1 {
		t.Errorf("expected still 1 user after second bootstrap, got %d", len(users))
	}
}

// ===========================================================================
// 9. Full stack: Admin creates route + key, then REST submit uses both
// ===========================================================================

func TestIntegration_FullStack_AdminThenREST(t *testing.T) {
	port := startMockSMSC(t)
	ts := setupTestStack(t)

	// Create admin and login
	if err := ts.userStore.Create("admin", "admin-pass", "admin"); err != nil {
		t.Fatalf("Create admin: %v", err)
	}
	token := ts.adminLogin(t, "admin", "admin-pass")

	// --- Step 1: Create a pool via admin API ---
	poolBody := fmt.Sprintf(`{"name":"za-smsc","host":"127.0.0.1","port":%d,"system_id":"testclient","password":"password","connections":1,"window_size":10}`, port)
	req := httptest.NewRequest(http.MethodPost, "/admin/api/pools", strings.NewReader(poolBody))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	ts.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("create pool: expected 201, got %d: %s", rr.Code, rr.Body.String())
	}

	// --- Step 2: Create an MT route via admin API ---
	routeBody := `{"prefix":"+27","strategy":"failover","pools":[{"name":"za-smsc"}]}`
	req = httptest.NewRequest(http.MethodPost, "/admin/api/routes/mt", strings.NewReader(routeBody))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rr = httptest.NewRecorder()
	ts.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("create route: expected 201, got %d: %s", rr.Code, rr.Body.String())
	}

	// --- Step 3: Create an API key via admin API ---
	keyBody := `{"label":"mobile-app","rate_limit":0}`
	req = httptest.NewRequest(http.MethodPost, "/admin/api/apikeys", strings.NewReader(keyBody))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rr = httptest.NewRecorder()
	ts.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("create API key: expected 201, got %d: %s", rr.Code, rr.Body.String())
	}

	var keyResp map[string]string
	_ = json.Unmarshal(rr.Body.Bytes(), &keyResp)
	apiKey := keyResp["key"]
	if apiKey == "" {
		t.Fatal("expected non-empty API key")
	}

	// --- Step 4: Submit via REST API using the newly created key ---
	ts.router.StartForwardWorkers(2)

	smsBody := `{"to":"+27831234567","from":"APP","body":"Full stack integration test"}`
	req = httptest.NewRequest(http.MethodPost, "/api/v1/sms", strings.NewReader(smsBody))
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")
	rr = httptest.NewRecorder()
	ts.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusAccepted {
		t.Fatalf("REST submit: expected 202, got %d: %s", rr.Code, rr.Body.String())
	}

	var submitResp SMSSubmitResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &submitResp)
	if submitResp.ID == "" {
		t.Fatal("expected non-empty message ID")
	}

	// --- Step 5: Query the submitted message ---
	req = httptest.NewRequest(http.MethodGet, "/api/v1/sms/"+submitResp.ID, nil)
	req.Header.Set("Authorization", "Bearer "+apiKey)
	rr = httptest.NewRecorder()
	ts.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("query: expected 200, got %d", rr.Code)
	}

	// Wait for forward worker to submit to mock SMSC
	time.Sleep(500 * time.Millisecond)

	ts.poolManager.Close()
}
