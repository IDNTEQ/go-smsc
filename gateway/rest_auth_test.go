package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// APIKeyStore tests
// ---------------------------------------------------------------------------

func TestAPIKey_Create(t *testing.T) {
	store := openTestStore(t)
	ks := NewAPIKeyStore(store)

	plainKey, err := ks.Create("test-key", 100)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !strings.HasPrefix(plainKey, "sk_live_") {
		t.Errorf("expected key to start with sk_live_, got %q", plainKey)
	}
	// sk_live_ (8 chars) + 32 hex chars = 40 chars total
	if len(plainKey) != 40 {
		t.Errorf("expected key length 40, got %d", len(plainKey))
	}
}

func TestAPIKey_ValidateSuccess(t *testing.T) {
	store := openTestStore(t)
	ks := NewAPIKeyStore(store)

	plainKey, err := ks.Create("my-service", 50)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	apiKey, err := ks.Validate(plainKey)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if apiKey.Label != "my-service" {
		t.Errorf("expected label 'my-service', got %q", apiKey.Label)
	}
	if apiKey.RateLimit != 50 {
		t.Errorf("expected rate limit 50, got %d", apiKey.RateLimit)
	}
	if apiKey.ID == "" {
		t.Error("expected non-empty ID")
	}
	if !strings.HasPrefix(apiKey.ID, "key_") {
		t.Errorf("expected ID to start with key_, got %q", apiKey.ID)
	}
	if apiKey.LastUsed.IsZero() {
		t.Error("expected LastUsed to be set after validation")
	}
}

func TestAPIKey_ValidateWrongKey(t *testing.T) {
	store := openTestStore(t)
	ks := NewAPIKeyStore(store)

	// Create a valid key but try to validate with a different one
	_, err := ks.Create("my-service", 0)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	_, err = ks.Validate("sk_live_0000000000000000000000000000000f")
	if err == nil {
		t.Fatal("expected error for wrong key")
	}
}

func TestAPIKey_ValidateEmptyStore(t *testing.T) {
	store := openTestStore(t)
	ks := NewAPIKeyStore(store)

	_, err := ks.Validate("sk_live_does_not_exist")
	if err == nil {
		t.Fatal("expected error for non-existent key")
	}
}

func TestAPIKey_Revoke(t *testing.T) {
	store := openTestStore(t)
	ks := NewAPIKeyStore(store)

	plainKey, err := ks.Create("to-revoke", 0)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Validate works before revoke
	apiKey, err := ks.Validate(plainKey)
	if err != nil {
		t.Fatalf("Validate before revoke: %v", err)
	}

	// Revoke by ID
	if err := ks.Revoke(apiKey.ID); err != nil {
		t.Fatalf("Revoke: %v", err)
	}

	// Validate should fail after revoke
	_, err = ks.Validate(plainKey)
	if err == nil {
		t.Fatal("expected error after revoke")
	}
}

func TestAPIKey_RevokeNonExistent(t *testing.T) {
	store := openTestStore(t)
	ks := NewAPIKeyStore(store)

	err := ks.Revoke("key_nonexistent")
	if err == nil {
		t.Fatal("expected error revoking non-existent key")
	}
}

func TestAPIKey_List(t *testing.T) {
	store := openTestStore(t)
	ks := NewAPIKeyStore(store)

	// Create multiple keys
	_, err := ks.Create("key-alpha", 100)
	if err != nil {
		t.Fatalf("Create alpha: %v", err)
	}
	_, err = ks.Create("key-bravo", 200)
	if err != nil {
		t.Fatalf("Create bravo: %v", err)
	}
	_, err = ks.Create("key-charlie", 0)
	if err != nil {
		t.Fatalf("Create charlie: %v", err)
	}

	keys, err := ks.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(keys) != 3 {
		t.Fatalf("expected 3 keys, got %d", len(keys))
	}

	// Verify hashes are not exposed
	for _, k := range keys {
		if k.KeyHash != "" {
			t.Errorf("expected empty KeyHash in listing, got %q", k.KeyHash)
		}
	}

	// Verify all labels are present
	labels := map[string]bool{}
	for _, k := range keys {
		labels[k.Label] = true
	}
	for _, expected := range []string{"key-alpha", "key-bravo", "key-charlie"} {
		if !labels[expected] {
			t.Errorf("expected label %q in listing", expected)
		}
	}
}

func TestAPIKey_ListEmpty(t *testing.T) {
	store := openTestStore(t)
	ks := NewAPIKeyStore(store)

	keys, err := ks.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(keys) != 0 {
		t.Errorf("expected 0 keys from empty store, got %d", len(keys))
	}
}

// ---------------------------------------------------------------------------
// Middleware tests
// ---------------------------------------------------------------------------

func TestAPIKeyAuthMiddleware_ValidKey(t *testing.T) {
	store := openTestStore(t)
	ks := NewAPIKeyStore(store)

	plainKey, err := ks.Create("middleware-test", 0)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Test handler that returns 200 and checks context
	handler := APIKeyAuthMiddleware(ks)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ak := GetAPIKey(r)
		if ak == nil {
			t.Error("expected API key in context")
			http.Error(w, "no key", http.StatusInternalServerError)
			return
		}
		if ak.Label != "middleware-test" {
			t.Errorf("expected label 'middleware-test', got %q", ak.Label)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", "Bearer "+plainKey)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestAPIKeyAuthMiddleware_NoAuth(t *testing.T) {
	store := openTestStore(t)
	ks := NewAPIKeyStore(store)

	handler := APIKeyAuthMiddleware(ks)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler should not be called without auth")
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rr.Code)
	}
}

func TestAPIKeyAuthMiddleware_InvalidKey(t *testing.T) {
	store := openTestStore(t)
	ks := NewAPIKeyStore(store)

	handler := APIKeyAuthMiddleware(ks)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler should not be called with invalid key")
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", "Bearer sk_live_invalid_key_that_does_not_exist")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rr.Code)
	}
}

func TestAPIKeyAuthMiddleware_MalformedAuth(t *testing.T) {
	store := openTestStore(t)
	ks := NewAPIKeyStore(store)

	handler := APIKeyAuthMiddleware(ks)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler should not be called with malformed auth")
		w.WriteHeader(http.StatusOK)
	}))

	// Test with Basic auth instead of Bearer
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", "Basic dXNlcjpwYXNz")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rr.Code)
	}
}

func TestGetAPIKey_NoContext(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	ak := GetAPIKey(req)
	if ak != nil {
		t.Errorf("expected nil API key from request without context, got %+v", ak)
	}
}
