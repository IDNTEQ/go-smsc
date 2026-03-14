package gateway

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// APIKey represents a REST API authentication key.
type APIKey struct {
	ID        string    `json:"id"`
	KeyHash   string    `json:"key_hash"`   // sha256 hash of the plain key
	Label     string    `json:"label"`
	RateLimit int       `json:"rate_limit"` // TPS, 0=unlimited
	CreatedAt time.Time `json:"created_at"`
	LastUsed  time.Time `json:"last_used"`
}

// APIKeyStore manages API keys in Pebble.
type APIKeyStore struct {
	store *MessageStore
}

// NewAPIKeyStore creates a new APIKeyStore backed by the given MessageStore.
func NewAPIKeyStore(store *MessageStore) *APIKeyStore {
	return &APIKeyStore{store: store}
}

// Create generates a new API key and stores it. Returns the plain key (shown once).
func (ks *APIKeyStore) Create(label string, rateLimit int) (string, error) {
	// Generate random key: sk_live_{32 random hex chars}
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	plainKey := "sk_live_" + hex.EncodeToString(b)

	// Hash for storage (SHA-256 — fast lookup, safe for high-entropy keys)
	hash := sha256.Sum256([]byte(plainKey))
	keyHash := hex.EncodeToString(hash[:])

	// Generate short ID
	idBytes := make([]byte, 4)
	if _, err := rand.Read(idBytes); err != nil {
		return "", err
	}
	id := "key_" + hex.EncodeToString(idBytes)

	apiKey := &APIKey{
		ID:        id,
		KeyHash:   keyHash,
		Label:     label,
		RateLimit: rateLimit,
		CreatedAt: time.Now(),
	}

	// Store as apikey:{hash}
	pebbleKey := "apikey:" + keyHash
	if err := ks.store.SetJSON(pebbleKey, apiKey); err != nil {
		return "", err
	}

	return plainKey, nil
}

// Validate checks a bearer token and returns the API key if valid.
func (ks *APIKeyStore) Validate(bearerToken string) (*APIKey, error) {
	hash := sha256.Sum256([]byte(bearerToken))
	keyHash := hex.EncodeToString(hash[:])

	pebbleKey := "apikey:" + keyHash
	var apiKey APIKey
	if err := ks.store.GetJSON(pebbleKey, &apiKey); err != nil {
		return nil, fmt.Errorf("invalid API key")
	}

	// Update last used (best effort)
	apiKey.LastUsed = time.Now()
	_ = ks.store.SetJSON(pebbleKey, &apiKey)

	return &apiKey, nil
}

// Revoke removes an API key by ID.
func (ks *APIKeyStore) Revoke(id string) error {
	// Scan all apikey: entries to find the one with matching ID
	var targetKey string
	err := ks.store.ScanPrefix("apikey:", func(key string, data []byte) error {
		var ak APIKey
		if err := json.Unmarshal(data, &ak); err != nil {
			return nil // skip malformed
		}
		if ak.ID == id {
			targetKey = key
		}
		return nil
	})
	if err != nil {
		return err
	}
	if targetKey == "" {
		return fmt.Errorf("API key %q not found", id)
	}
	return ks.store.DeleteKey(targetKey)
}

// List returns all API keys (without exposing the hash).
func (ks *APIKeyStore) List() ([]*APIKey, error) {
	var keys []*APIKey
	err := ks.store.ScanPrefix("apikey:", func(key string, data []byte) error {
		var ak APIKey
		if err := json.Unmarshal(data, &ak); err != nil {
			return nil
		}
		ak.KeyHash = "" // Don't expose hash in listings
		keys = append(keys, &ak)
		return nil
	})
	return keys, err
}

type contextKey string

const apiKeyContextKey contextKey = "apiKey"

// APIKeyAuthMiddleware validates the Authorization: Bearer <key> header.
func APIKeyAuthMiddleware(ks *APIKeyStore) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			auth := r.Header.Get("Authorization")
			if !strings.HasPrefix(auth, "Bearer ") {
				http.Error(w, `{"error":"missing or invalid Authorization header"}`, http.StatusUnauthorized)
				return
			}
			token := strings.TrimPrefix(auth, "Bearer ")

			apiKey, err := ks.Validate(token)
			if err != nil {
				http.Error(w, `{"error":"invalid API key"}`, http.StatusUnauthorized)
				return
			}

			// Store API key in request context for downstream handlers
			ctx := context.WithValue(r.Context(), apiKeyContextKey, apiKey)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// GetAPIKey retrieves the authenticated API key from the request context.
func GetAPIKey(r *http.Request) *APIKey {
	ak, _ := r.Context().Value(apiKeyContextKey).(*APIKey)
	return ak
}
