package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"go.uber.org/zap/zaptest"
)

// newTestRouter creates a minimal Router for REST API tests.
func newTestRouter(t *testing.T) (*Router, *MessageStore) {
	t.Helper()
	store := openTestStore(t)
	logger := zaptest.NewLogger(t)
	cfg := Config{
		ForwardQueueSize: 1000,
		MaxSubmitRetries: 3,
	}
	metrics := newTestMetrics()
	r := NewRouter(store, metrics, cfg, logger)
	return r, store
}

// newTestMetrics creates unregistered Prometheus metrics for testing.
// These are NOT registered with the default registry, so multiple tests
// can run without panicking on duplicate registration.
func newTestMetrics() *Metrics {
	return &Metrics{
		NorthboundConnections: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "test_smscgw_northbound_connections",
		}),
		SubmitTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "test_smscgw_submit_total",
		}, []string{"status"}),
		DLRTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "test_smscgw_dlr_total",
		}, []string{"status", "routed"}),
		MOTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "test_smscgw_mo_total",
		}, []string{"routed"}),
		AffinityTableSize: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "test_smscgw_affinity_table_size",
		}),
		CorrelationTableSize: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "test_smscgw_correlation_table_size",
		}),
		StoreMessages: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "test_smscgw_store_messages",
		}),
		RetryQueueSize: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "test_smscgw_retry_queue_size",
		}),
		SubmitLatency: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name: "test_smscgw_submit_latency_seconds",
		}),
		DeliverLatency: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name: "test_smscgw_deliver_latency_seconds",
		}),
		ThrottledTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "test_smscgw_throttled_total",
		}),
		BlacklistedTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "test_smscgw_blacklisted_total",
		}),
		SyntheticDLRTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "test_smscgw_synthetic_dlr_total",
		}),
		SubmitRetryTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "test_smscgw_submit_retry_total",
		}),
		CallbackTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "test_smscgw_callback_total",
		}, []string{"status"}),
		RouteResolutions: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "test_smscgw_route_resolutions_total",
		}, []string{"type", "result"}),
		PoolHealthGauge: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "test_smscgw_pool_healthy",
		}, []string{"pool_name"}),
		AdminLoginTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "test_smscgw_admin_login_total",
		}, []string{"status"}),
	}
}

// ---------------------------------------------------------------------------
// REST API submit tests
// ---------------------------------------------------------------------------

func TestRESTSubmit_Success(t *testing.T) {
	r, _ := newTestRouter(t)

	body := `{"to":"+254700000001","from":"SENDER","body":"Hello World"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/sms", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	r.HandleHTTPSubmit(rr, req)

	if rr.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp SMSSubmitResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp.ID == "" {
		t.Error("expected non-empty ID")
	}
	if resp.Status != "accepted" {
		t.Errorf("expected status 'accepted', got %q", resp.Status)
	}
	if resp.To != "+254700000001" {
		t.Errorf("expected to '+254700000001', got %q", resp.To)
	}

	// Verify correlation was stored
	if _, ok := r.gwCorrelation.Get(resp.ID); !ok {
		t.Error("expected correlation entry for submitted message")
	}
}

func TestRESTSubmit_WithReference(t *testing.T) {
	r, _ := newTestRouter(t)

	body := `{"to":"+254700000001","from":"SENDER","body":"Hello","reference":"ref-123"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/sms", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	r.HandleHTTPSubmit(rr, req)

	if rr.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", rr.Code)
	}

	var resp SMSSubmitResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Reference != "ref-123" {
		t.Errorf("expected reference 'ref-123', got %q", resp.Reference)
	}
}

func TestRESTSubmit_MissingTo(t *testing.T) {
	r, _ := newTestRouter(t)

	body := `{"from":"SENDER","body":"Hello"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/sms", strings.NewReader(body))
	rr := httptest.NewRecorder()

	r.HandleHTTPSubmit(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

func TestRESTSubmit_MissingBody(t *testing.T) {
	r, _ := newTestRouter(t)

	body := `{"to":"+254700000001","from":"SENDER"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/sms", strings.NewReader(body))
	rr := httptest.NewRecorder()

	r.HandleHTTPSubmit(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

func TestRESTSubmit_InvalidJSON(t *testing.T) {
	r, _ := newTestRouter(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/sms", strings.NewReader("{invalid"))
	rr := httptest.NewRecorder()

	r.HandleHTTPSubmit(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

// ---------------------------------------------------------------------------
// Batch submit tests
// ---------------------------------------------------------------------------

func TestRESTBatchSubmit_Success(t *testing.T) {
	r, _ := newTestRouter(t)

	body := `{"messages":[
		{"to":"+254700000001","from":"SENDER","body":"Hello 1"},
		{"to":"+254700000002","from":"SENDER","body":"Hello 2"},
		{"to":"+254700000003","from":"SENDER","body":"Hello 3"}
	],"reference_prefix":"batch"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/sms/batch", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	r.HandleHTTPBatchSubmit(rr, req)

	if rr.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", rr.Code, rr.Body.String())
	}

	var results []SMSSubmitResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &results); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}

	// Check references are auto-generated from prefix
	for i, res := range results {
		if res.Status != "accepted" {
			t.Errorf("result[%d]: expected status 'accepted', got %q", i, res.Status)
		}
		if res.ID == "" {
			t.Errorf("result[%d]: expected non-empty ID", i)
		}
	}
	if results[0].Reference != "batch-0" {
		t.Errorf("expected reference 'batch-0', got %q", results[0].Reference)
	}
	if results[2].Reference != "batch-2" {
		t.Errorf("expected reference 'batch-2', got %q", results[2].Reference)
	}
}

func TestRESTBatchSubmit_EmptyMessages(t *testing.T) {
	r, _ := newTestRouter(t)

	body := `{"messages":[]}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/sms/batch", strings.NewReader(body))
	rr := httptest.NewRecorder()

	r.HandleHTTPBatchSubmit(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

func TestRESTBatchSubmit_TooManyMessages(t *testing.T) {
	r, _ := newTestRouter(t)

	// Build batch with 1001 messages
	msgs := make([]SMSSubmitRequest, 1001)
	for i := range msgs {
		msgs[i] = SMSSubmitRequest{To: "+254700000001", Body: "x"}
	}
	batchReq := SMSBatchRequest{Messages: msgs}
	data, _ := json.Marshal(batchReq)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/sms/batch", strings.NewReader(string(data)))
	rr := httptest.NewRecorder()

	r.HandleHTTPBatchSubmit(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestRESTBatchSubmit_InvalidJSON(t *testing.T) {
	r, _ := newTestRouter(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/sms/batch", strings.NewReader("not json"))
	rr := httptest.NewRecorder()

	r.HandleHTTPBatchSubmit(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

// ---------------------------------------------------------------------------
// Query tests
// ---------------------------------------------------------------------------

func TestRESTQuery_Found(t *testing.T) {
	r, _ := newTestRouter(t)

	// Submit a message first to create correlation
	gwMsgID := r.nextMsgID()
	r.gwCorrelation.Set(gwMsgID, &correlation{
		GwMsgID:     gwMsgID,
		NorthConnID: "rest-api",
		MSISDN:      "+254700000001",
		SubmittedAt: time.Now(),
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/sms/"+gwMsgID, nil)
	req.SetPathValue("id", gwMsgID)
	rr := httptest.NewRecorder()

	r.HandleHTTPQuery(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var result map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if result["id"] != gwMsgID {
		t.Errorf("expected id %q, got %v", gwMsgID, result["id"])
	}
	if result["status"] != "submitted" {
		t.Errorf("expected status 'submitted', got %v", result["status"])
	}
}

func TestRESTQuery_NotFound(t *testing.T) {
	r, _ := newTestRouter(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/sms/nonexistent", nil)
	req.SetPathValue("id", "nonexistent")
	rr := httptest.NewRecorder()

	r.HandleHTTPQuery(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rr.Code)
	}
}

func TestRESTQuery_EmptyID(t *testing.T) {
	r, _ := newTestRouter(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/sms/", nil)
	// Do not set path value — simulates empty ID
	rr := httptest.NewRecorder()

	r.HandleHTTPQuery(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

// ---------------------------------------------------------------------------
// Auth middleware integration tests
// ---------------------------------------------------------------------------

func TestRESTAuth_NoKey_Returns401(t *testing.T) {
	r, store := newTestRouter(t)
	ks := NewAPIKeyStore(store)

	mux := http.NewServeMux()
	r.RegisterRESTRoutes(mux, ks)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/sms", strings.NewReader(`{"to":"+254700000001","body":"test"}`))
	rr := httptest.NewRecorder()

	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestRESTAuth_ValidKey_Returns202(t *testing.T) {
	r, store := newTestRouter(t)
	ks := NewAPIKeyStore(store)

	plainKey, err := ks.Create("test-api", 0)
	if err != nil {
		t.Fatalf("Create API key: %v", err)
	}

	mux := http.NewServeMux()
	r.RegisterRESTRoutes(mux, ks)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/sms",
		strings.NewReader(`{"to":"+254700000001","from":"SENDER","body":"test"}`))
	req.Header.Set("Authorization", "Bearer "+plainKey)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestRESTAuth_InvalidKey_Returns401(t *testing.T) {
	r, store := newTestRouter(t)
	ks := NewAPIKeyStore(store)

	mux := http.NewServeMux()
	r.RegisterRESTRoutes(mux, ks)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/sms",
		strings.NewReader(`{"to":"+254700000001","body":"test"}`))
	req.Header.Set("Authorization", "Bearer sk_live_invalid_key_that_does_not_exist")
	rr := httptest.NewRecorder()

	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
}

// ---------------------------------------------------------------------------
// Callback delivery tests
// ---------------------------------------------------------------------------

func TestCallbackDelivery_DLR(t *testing.T) {
	r, store := newTestRouter(t)

	// Set up a mock HTTP server to receive callbacks
	var mu sync.Mutex
	var received []DLRCallbackPayload

	callbackServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var payload DLRCallbackPayload
		if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
			t.Errorf("decode callback payload: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		mu.Lock()
		received = append(received, payload)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer callbackServer.Close()

	gwMsgID := "GW-test-callback-1"

	// Register callback
	_ = store.SetJSON("callback:"+gwMsgID, &CallbackRecord{
		GwMsgID:     gwMsgID,
		CallbackURL: callbackServer.URL,
		Reference:   "my-ref-123",
	})

	// Deliver DLR callback
	r.deliverDLRCallback(gwMsgID, "DELIVRD", "+254700000001", "SENDER")

	// Verify callback was received
	mu.Lock()
	defer mu.Unlock()

	if len(received) != 1 {
		t.Fatalf("expected 1 callback, got %d", len(received))
	}
	cb := received[0]
	if cb.Event != "dlr" {
		t.Errorf("expected event 'dlr', got %q", cb.Event)
	}
	if cb.ID != gwMsgID {
		t.Errorf("expected id %q, got %q", gwMsgID, cb.ID)
	}
	if cb.Status != "DELIVRD" {
		t.Errorf("expected status 'DELIVRD', got %q", cb.Status)
	}
	if cb.To != "+254700000001" {
		t.Errorf("expected to '+254700000001', got %q", cb.To)
	}
	if cb.Reference != "my-ref-123" {
		t.Errorf("expected reference 'my-ref-123', got %q", cb.Reference)
	}

	// Verify callback record was cleaned up
	var check CallbackRecord
	if err := store.GetJSON("callback:"+gwMsgID, &check); err == nil {
		t.Error("expected callback record to be deleted after successful delivery")
	}
}

func TestCallbackDelivery_NoCallback(t *testing.T) {
	r, _ := newTestRouter(t)

	// Should not panic or error when no callback is registered
	r.deliverDLRCallback("GW-no-callback", "DELIVRD", "+254700000001", "SENDER")
}

func TestCallbackDelivery_MO(t *testing.T) {
	r, _ := newTestRouter(t)

	var mu sync.Mutex
	var received []MOCallbackPayload

	callbackServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var payload MOCallbackPayload
		if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
			t.Errorf("decode MO callback payload: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		mu.Lock()
		received = append(received, payload)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer callbackServer.Close()

	r.deliverMOCallback(callbackServer.URL, "+254700000001", "12345", []byte("Hello from mobile"))

	mu.Lock()
	defer mu.Unlock()

	if len(received) != 1 {
		t.Fatalf("expected 1 MO callback, got %d", len(received))
	}
	mo := received[0]
	if mo.Event != "mo" {
		t.Errorf("expected event 'mo', got %q", mo.Event)
	}
	if mo.From != "+254700000001" {
		t.Errorf("expected from '+254700000001', got %q", mo.From)
	}
	if mo.To != "12345" {
		t.Errorf("expected to '12345', got %q", mo.To)
	}
	if mo.Body != "Hello from mobile" {
		t.Errorf("expected body 'Hello from mobile', got %q", mo.Body)
	}
}

func TestCallbackDelivery_FailedCallback_EnqueuesRetry(t *testing.T) {
	r, store := newTestRouter(t)

	// Use a server that always returns 500
	failServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer failServer.Close()

	gwMsgID := "GW-retry-test"

	// Register callback
	_ = store.SetJSON("callback:"+gwMsgID, &CallbackRecord{
		GwMsgID:     gwMsgID,
		CallbackURL: failServer.URL,
		Reference:   "retry-ref",
		Retries:     0,
	})

	// Attempt delivery (should fail and enqueue retry)
	r.deliverDLRCallback(gwMsgID, "DELIVRD", "+254700000001", "SENDER")

	// Verify callback record was updated with retry count
	var cb CallbackRecord
	if err := store.GetJSON("callback:"+gwMsgID, &cb); err != nil {
		t.Fatalf("get callback record: %v", err)
	}
	if cb.Retries != 1 {
		t.Errorf("expected retries=1, got %d", cb.Retries)
	}
	if cb.NextAttempt.IsZero() {
		t.Error("expected non-zero next attempt time")
	}
}

func TestCallbackDelivery_RetriesExhausted(t *testing.T) {
	r, store := newTestRouter(t)

	failServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer failServer.Close()

	gwMsgID := "GW-exhaust-test"

	// Register callback with retries already at max
	_ = store.SetJSON("callback:"+gwMsgID, &CallbackRecord{
		GwMsgID:     gwMsgID,
		CallbackURL: failServer.URL,
		Reference:   "exhaust-ref",
		Retries:     3,
	})

	// This should give up and delete the callback
	r.deliverDLRCallback(gwMsgID, "DELIVRD", "+254700000001", "SENDER")

	// Verify callback record was removed after exhaustion
	var cb CallbackRecord
	if err := store.GetJSON("callback:"+gwMsgID, &cb); err == nil {
		t.Error("expected callback record to be deleted after retries exhausted")
	}
}

// ---------------------------------------------------------------------------
// BuildSubmitSMBody tests
// ---------------------------------------------------------------------------

func TestBuildSubmitSMBody_Roundtrip(t *testing.T) {
	sourceAddr := "SENDER"
	destAddr := "+254700000001"
	message := []byte("Hello World")

	body := BuildSubmitSMBody(sourceAddr, destAddr, message)

	// Parse back using ParseSubmitSMAddresses
	parsedSource, parsedDest := ParseSubmitSMAddresses(body)

	if parsedSource != sourceAddr {
		t.Errorf("expected source %q, got %q", sourceAddr, parsedSource)
	}
	if parsedDest != destAddr {
		t.Errorf("expected dest %q, got %q", destAddr, parsedDest)
	}
}

// ---------------------------------------------------------------------------
// RegisterRESTRoutes tests
// ---------------------------------------------------------------------------

func TestRegisterRESTRoutes_AllEndpoints(t *testing.T) {
	r, store := newTestRouter(t)
	ks := NewAPIKeyStore(store)

	plainKey, err := ks.Create("route-test", 0)
	if err != nil {
		t.Fatalf("Create API key: %v", err)
	}

	mux := http.NewServeMux()
	r.RegisterRESTRoutes(mux, ks)

	// Test POST /api/v1/sms
	req := httptest.NewRequest(http.MethodPost, "/api/v1/sms",
		strings.NewReader(`{"to":"+254700000001","from":"TEST","body":"hello"}`))
	req.Header.Set("Authorization", "Bearer "+plainKey)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Errorf("POST /api/v1/sms: expected 202, got %d", rr.Code)
	}

	// Extract the ID from submit response for query test
	var submitResp SMSSubmitResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &submitResp)

	// Test POST /api/v1/sms/batch
	req = httptest.NewRequest(http.MethodPost, "/api/v1/sms/batch",
		strings.NewReader(`{"messages":[{"to":"+254700000001","body":"batch msg"}]}`))
	req.Header.Set("Authorization", "Bearer "+plainKey)
	req.Header.Set("Content-Type", "application/json")
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Errorf("POST /api/v1/sms/batch: expected 202, got %d", rr.Code)
	}

	// Test GET /api/v1/sms/{id}
	req = httptest.NewRequest(http.MethodGet, "/api/v1/sms/"+submitResp.ID, nil)
	req.Header.Set("Authorization", "Bearer "+plainKey)
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("GET /api/v1/sms/{id}: expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
}
