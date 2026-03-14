package gateway

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// AdminUser represents an admin UI user.
type AdminUser struct {
	Username     string    `json:"username"`
	PasswordHash string    `json:"password_hash"` // bcrypt
	Role         string    `json:"role"`           // "admin"
	CreatedAt    time.Time `json:"created_at"`
	MustChange   bool      `json:"must_change"` // force password change on first login
}

// AdminUserStore manages admin users and JWT authentication in Pebble.
type AdminUserStore struct {
	store     *MessageStore
	jwtSecret []byte
}

// NewAdminUserStore creates a new AdminUserStore backed by the given MessageStore.
// If no JWT secret is provided, a random one is generated.
func NewAdminUserStore(store *MessageStore, jwtSecret []byte) *AdminUserStore {
	if len(jwtSecret) == 0 {
		jwtSecret = make([]byte, 32)
		rand.Read(jwtSecret)
	}
	return &AdminUserStore{store: store, jwtSecret: jwtSecret}
}

// Create stores a new admin user with a bcrypt-hashed password.
func (us *AdminUserStore) Create(username, password, role string) error {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), 10)
	if err != nil {
		return fmt.Errorf("bcrypt hash: %w", err)
	}
	user := &AdminUser{
		Username:     username,
		PasswordHash: string(hash),
		Role:         role,
		CreatedAt:    time.Now(),
		MustChange:   false,
	}
	return us.store.SetJSON("user:"+username, user)
}

// Authenticate checks the username/password and returns a JWT token on success.
func (us *AdminUserStore) Authenticate(username, password string) (string, error) {
	var user AdminUser
	if err := us.store.GetJSON("user:"+username, &user); err != nil {
		return "", fmt.Errorf("invalid credentials")
	}
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)); err != nil {
		return "", fmt.Errorf("invalid credentials")
	}
	return us.generateJWT(user.Username, user.Role)
}

// jwtHeader is the JOSE header for HS256 JWT.
type jwtHeader struct {
	Alg string `json:"alg"`
	Typ string `json:"typ"`
}

// jwtClaims are the payload claims for the JWT.
type jwtClaims struct {
	Sub  string `json:"sub"`
	Role string `json:"role"`
	Exp  int64  `json:"exp"`
	Iat  int64  `json:"iat"`
}

// base64URLEncode encodes data using base64url (no padding).
func base64URLEncode(data []byte) string {
	return base64.RawURLEncoding.EncodeToString(data)
}

// base64URLDecode decodes a base64url string (no padding).
func base64URLDecode(s string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(s)
}

// generateJWT creates an HS256 JWT token with the given claims.
func (us *AdminUserStore) generateJWT(username, role string) (string, error) {
	return us.generateJWTWithTime(username, role, time.Now())
}

// generateJWTWithTime creates an HS256 JWT token using a specific timestamp (for testing).
func (us *AdminUserStore) generateJWTWithTime(username, role string, now time.Time) (string, error) {
	header := jwtHeader{Alg: "HS256", Typ: "JWT"}
	claims := jwtClaims{
		Sub:  username,
		Role: role,
		Iat:  now.Unix(),
		Exp:  now.Add(24 * time.Hour).Unix(),
	}

	headerJSON, err := json.Marshal(header)
	if err != nil {
		return "", fmt.Errorf("marshal header: %w", err)
	}
	claimsJSON, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("marshal claims: %w", err)
	}

	headerB64 := base64URLEncode(headerJSON)
	claimsB64 := base64URLEncode(claimsJSON)

	signingInput := headerB64 + "." + claimsB64

	mac := hmac.New(sha256.New, us.jwtSecret)
	mac.Write([]byte(signingInput))
	signature := mac.Sum(nil)

	return signingInput + "." + base64URLEncode(signature), nil
}

// ValidateJWT verifies a JWT token and returns the associated admin user.
func (us *AdminUserStore) ValidateJWT(token string) (*AdminUser, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("invalid token format")
	}

	// Verify signature
	signingInput := parts[0] + "." + parts[1]
	mac := hmac.New(sha256.New, us.jwtSecret)
	mac.Write([]byte(signingInput))
	expectedSig := mac.Sum(nil)

	actualSig, err := base64URLDecode(parts[2])
	if err != nil {
		return nil, fmt.Errorf("decode signature: %w", err)
	}
	if !hmac.Equal(expectedSig, actualSig) {
		return nil, fmt.Errorf("invalid signature")
	}

	// Decode claims
	claimsJSON, err := base64URLDecode(parts[1])
	if err != nil {
		return nil, fmt.Errorf("decode claims: %w", err)
	}
	var claims jwtClaims
	if err := json.Unmarshal(claimsJSON, &claims); err != nil {
		return nil, fmt.Errorf("unmarshal claims: %w", err)
	}

	// Check expiration
	if time.Now().Unix() > claims.Exp {
		return nil, fmt.Errorf("token expired")
	}

	// Load user from store to confirm still exists
	var user AdminUser
	if err := us.store.GetJSON("user:"+claims.Sub, &user); err != nil {
		return nil, fmt.Errorf("user no longer exists")
	}

	return &user, nil
}

// ChangePassword changes the password for an admin user after verifying the old one.
func (us *AdminUserStore) ChangePassword(username, oldPass, newPass string) error {
	var user AdminUser
	if err := us.store.GetJSON("user:"+username, &user); err != nil {
		return fmt.Errorf("user not found")
	}
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(oldPass)); err != nil {
		return fmt.Errorf("invalid old password")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(newPass), 10)
	if err != nil {
		return fmt.Errorf("bcrypt hash: %w", err)
	}
	user.PasswordHash = string(hash)
	user.MustChange = false
	return us.store.SetJSON("user:"+username, &user)
}

// Delete removes an admin user.
func (us *AdminUserStore) Delete(username string) error {
	return us.store.DeleteKey("user:" + username)
}

// List returns all admin users with password hashes cleared.
func (us *AdminUserStore) List() ([]*AdminUser, error) {
	var users []*AdminUser
	err := us.store.ScanPrefix("user:", func(key string, data []byte) error {
		var u AdminUser
		if err := json.Unmarshal(data, &u); err != nil {
			return nil // skip malformed
		}
		u.PasswordHash = "" // Don't expose hash in listings
		users = append(users, &u)
		return nil
	})
	return users, err
}

// Bootstrap creates a default admin user if no users exist.
func (us *AdminUserStore) Bootstrap() error {
	var exists bool
	err := us.store.ScanPrefix("user:", func(key string, data []byte) error {
		exists = true
		return nil
	})
	if err != nil {
		return err
	}
	if exists {
		return nil
	}

	// Generate a random default password
	b := make([]byte, 12)
	rand.Read(b)
	defaultPassword := hex.EncodeToString(b)

	hash, hashErr := bcrypt.GenerateFromPassword([]byte(defaultPassword), 10)
	if hashErr != nil {
		return fmt.Errorf("bcrypt hash: %w", hashErr)
	}
	user := &AdminUser{
		Username:     "admin",
		PasswordHash: string(hash),
		Role:         "admin",
		CreatedAt:    time.Now(),
		MustChange:   true,
	}
	if err := us.store.SetJSON("user:admin", user); err != nil {
		return err
	}
	log.Printf("WARNING: Default admin user created — password: %s (change immediately)", defaultPassword)
	return nil
}

const adminUserContextKey contextKey = "adminUser"

// AdminAuthMiddleware validates the Authorization: Bearer <jwt> header.
func AdminAuthMiddleware(us *AdminUserStore) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			auth := r.Header.Get("Authorization")
			if !strings.HasPrefix(auth, "Bearer ") {
				http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
				return
			}
			token := strings.TrimPrefix(auth, "Bearer ")

			user, err := us.ValidateJWT(token)
			if err != nil {
				http.Error(w, `{"error":"invalid or expired token"}`, http.StatusUnauthorized)
				return
			}

			ctx := context.WithValue(r.Context(), adminUserContextKey, user)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// GetAdminUser retrieves the authenticated admin user from the request context.
func GetAdminUser(r *http.Request) *AdminUser {
	u, _ := r.Context().Value(adminUserContextKey).(*AdminUser)
	return u
}
