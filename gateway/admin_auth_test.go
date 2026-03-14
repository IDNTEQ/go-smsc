package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

var testJWTSecret = []byte("test-secret-key-for-jwt-signing!")

// ---------------------------------------------------------------------------
// AdminUserStore tests
// ---------------------------------------------------------------------------

func TestAdminUser_CreateAndAuthenticate(t *testing.T) {
	store := openTestStore(t)
	us := NewAdminUserStore(store, testJWTSecret)

	if err := us.Create("alice", "s3cret", "admin"); err != nil {
		t.Fatalf("Create: %v", err)
	}

	token, err := us.Authenticate("alice", "s3cret")
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if token == "" {
		t.Fatal("expected non-empty JWT token")
	}
	// JWT has 3 dot-separated parts
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Errorf("expected 3 JWT parts, got %d", len(parts))
	}
}

func TestAdminUser_ValidateJWT(t *testing.T) {
	store := openTestStore(t)
	us := NewAdminUserStore(store, testJWTSecret)

	if err := us.Create("bob", "password123", "admin"); err != nil {
		t.Fatalf("Create: %v", err)
	}

	token, err := us.Authenticate("bob", "password123")
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}

	user, err := us.ValidateJWT(token)
	if err != nil {
		t.Fatalf("ValidateJWT: %v", err)
	}
	if user.Username != "bob" {
		t.Errorf("expected username 'bob', got %q", user.Username)
	}
	if user.Role != "admin" {
		t.Errorf("expected role 'admin', got %q", user.Role)
	}
}

func TestAdminUser_AuthenticateWrongPassword(t *testing.T) {
	store := openTestStore(t)
	us := NewAdminUserStore(store, testJWTSecret)

	if err := us.Create("carol", "correct-pass", "admin"); err != nil {
		t.Fatalf("Create: %v", err)
	}

	_, err := us.Authenticate("carol", "wrong-pass")
	if err == nil {
		t.Fatal("expected error for wrong password")
	}
}

func TestAdminUser_AuthenticateNonExistentUser(t *testing.T) {
	store := openTestStore(t)
	us := NewAdminUserStore(store, testJWTSecret)

	_, err := us.Authenticate("nobody", "password")
	if err == nil {
		t.Fatal("expected error for non-existent user")
	}
}

func TestAdminUser_ValidateJWT_Expired(t *testing.T) {
	store := openTestStore(t)
	us := NewAdminUserStore(store, testJWTSecret)

	if err := us.Create("dave", "mypass", "admin"); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Generate a token that expired 1 hour ago
	token, err := us.generateJWTWithTime("dave", "admin", time.Now().Add(-25*time.Hour))
	if err != nil {
		t.Fatalf("generateJWTWithTime: %v", err)
	}

	_, err = us.ValidateJWT(token)
	if err == nil {
		t.Fatal("expected error for expired token")
	}
	if !strings.Contains(err.Error(), "expired") {
		t.Errorf("expected 'expired' in error, got %q", err.Error())
	}
}

func TestAdminUser_ValidateJWT_Tampered(t *testing.T) {
	store := openTestStore(t)
	us := NewAdminUserStore(store, testJWTSecret)

	if err := us.Create("eve", "mypass", "admin"); err != nil {
		t.Fatalf("Create: %v", err)
	}

	token, err := us.Authenticate("eve", "mypass")
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}

	// Tamper with the payload by flipping a character in the claims part
	parts := strings.Split(token, ".")
	tampered := parts[0] + "." + parts[1] + "x" + "." + parts[2]

	_, err = us.ValidateJWT(tampered)
	if err == nil {
		t.Fatal("expected error for tampered token")
	}
}

func TestAdminUser_ValidateJWT_WrongSecret(t *testing.T) {
	store := openTestStore(t)
	us1 := NewAdminUserStore(store, []byte("secret-one"))
	us2 := NewAdminUserStore(store, []byte("secret-two"))

	if err := us1.Create("frank", "mypass", "admin"); err != nil {
		t.Fatalf("Create: %v", err)
	}

	token, err := us1.Authenticate("frank", "mypass")
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}

	// Validate with different secret should fail
	_, err = us2.ValidateJWT(token)
	if err == nil {
		t.Fatal("expected error for token signed with different secret")
	}
}

func TestAdminUser_ValidateJWT_DeletedUser(t *testing.T) {
	store := openTestStore(t)
	us := NewAdminUserStore(store, testJWTSecret)

	if err := us.Create("grace", "mypass", "admin"); err != nil {
		t.Fatalf("Create: %v", err)
	}

	token, err := us.Authenticate("grace", "mypass")
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}

	// Delete the user
	if err := us.Delete("grace"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	// Token should now fail validation
	_, err = us.ValidateJWT(token)
	if err == nil {
		t.Fatal("expected error for deleted user's token")
	}
}

func TestAdminUser_Bootstrap_CreatesDefault(t *testing.T) {
	store := openTestStore(t)
	us := NewAdminUserStore(store, testJWTSecret)

	if err := us.Bootstrap(); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}

	users, err := us.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(users) != 1 {
		t.Fatalf("expected 1 user after bootstrap, got %d", len(users))
	}
	if users[0].Username != "admin" {
		t.Errorf("expected username 'admin', got %q", users[0].Username)
	}
	if users[0].Role != "admin" {
		t.Errorf("expected role 'admin', got %q", users[0].Role)
	}
}

func TestAdminUser_Bootstrap_MustChange(t *testing.T) {
	store := openTestStore(t)
	us := NewAdminUserStore(store, testJWTSecret)

	if err := us.Bootstrap(); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}

	// Read the full user record (not from List which clears password hash)
	var user AdminUser
	if err := store.GetJSON("user:admin", &user); err != nil {
		t.Fatalf("GetJSON: %v", err)
	}
	if !user.MustChange {
		t.Error("expected MustChange=true for bootstrapped admin")
	}
}

func TestAdminUser_Bootstrap_NoopWhenUsersExist(t *testing.T) {
	store := openTestStore(t)
	us := NewAdminUserStore(store, testJWTSecret)

	// Create a user first
	if err := us.Create("existing", "pass", "admin"); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Bootstrap should be a no-op
	if err := us.Bootstrap(); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}

	users, err := us.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(users) != 1 {
		t.Fatalf("expected 1 user (no default created), got %d", len(users))
	}
	if users[0].Username != "existing" {
		t.Errorf("expected username 'existing', got %q", users[0].Username)
	}
}

func TestAdminUser_ChangePassword(t *testing.T) {
	store := openTestStore(t)
	us := NewAdminUserStore(store, testJWTSecret)

	if err := us.Create("henry", "oldpass", "admin"); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := us.ChangePassword("henry", "oldpass", "newpass"); err != nil {
		t.Fatalf("ChangePassword: %v", err)
	}

	// Old password should no longer work
	_, err := us.Authenticate("henry", "oldpass")
	if err == nil {
		t.Fatal("expected error with old password after change")
	}

	// New password should work
	token, err := us.Authenticate("henry", "newpass")
	if err != nil {
		t.Fatalf("Authenticate with new password: %v", err)
	}
	if token == "" {
		t.Fatal("expected non-empty token")
	}
}

func TestAdminUser_ChangePassword_WrongOldPassword(t *testing.T) {
	store := openTestStore(t)
	us := NewAdminUserStore(store, testJWTSecret)

	if err := us.Create("irene", "correct", "admin"); err != nil {
		t.Fatalf("Create: %v", err)
	}

	err := us.ChangePassword("irene", "wrong", "newpass")
	if err == nil {
		t.Fatal("expected error for wrong old password")
	}
}

func TestAdminUser_ChangePassword_ClearsMustChange(t *testing.T) {
	store := openTestStore(t)
	us := NewAdminUserStore(store, testJWTSecret)

	// Bootstrap creates user with MustChange=true
	if err := us.Bootstrap(); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}

	// Read the bootstrapped password from the record so we can change it
	var user AdminUser
	if err := store.GetJSON("user:admin", &user); err != nil {
		t.Fatalf("GetJSON: %v", err)
	}
	if !user.MustChange {
		t.Fatal("expected MustChange=true before password change")
	}

	// We can't test ChangePassword directly here since we don't know the generated password.
	// Instead, manually create a user with MustChange=true and test.
	user2 := &AdminUser{
		Username:     "mustchange",
		PasswordHash: user.PasswordHash, // reuse bootstrapped hash
		Role:         "admin",
		CreatedAt:    time.Now(),
		MustChange:   true,
	}
	if err := store.SetJSON("user:mustchange", user2); err != nil {
		t.Fatalf("SetJSON: %v", err)
	}

	// We need to know the password. Let's create fresh.
	if err := us.Create("testmc", "temppass", "admin"); err != nil {
		t.Fatalf("Create: %v", err)
	}
	// Manually set MustChange=true
	var u AdminUser
	if err := store.GetJSON("user:testmc", &u); err != nil {
		t.Fatalf("GetJSON: %v", err)
	}
	u.MustChange = true
	if err := store.SetJSON("user:testmc", &u); err != nil {
		t.Fatalf("SetJSON: %v", err)
	}

	// Change password
	if err := us.ChangePassword("testmc", "temppass", "newpass"); err != nil {
		t.Fatalf("ChangePassword: %v", err)
	}

	// Verify MustChange is now false
	var updated AdminUser
	if err := store.GetJSON("user:testmc", &updated); err != nil {
		t.Fatalf("GetJSON after change: %v", err)
	}
	if updated.MustChange {
		t.Error("expected MustChange=false after password change")
	}
}

func TestAdminUser_Delete(t *testing.T) {
	store := openTestStore(t)
	us := NewAdminUserStore(store, testJWTSecret)

	if err := us.Create("todelete", "pass", "admin"); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Verify user exists
	_, err := us.Authenticate("todelete", "pass")
	if err != nil {
		t.Fatalf("Authenticate before delete: %v", err)
	}

	if err := us.Delete("todelete"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	// Authentication should fail
	_, err = us.Authenticate("todelete", "pass")
	if err == nil {
		t.Fatal("expected error after deleting user")
	}
}

func TestAdminUser_List(t *testing.T) {
	store := openTestStore(t)
	us := NewAdminUserStore(store, testJWTSecret)

	if err := us.Create("alpha", "pass1", "admin"); err != nil {
		t.Fatalf("Create alpha: %v", err)
	}
	if err := us.Create("bravo", "pass2", "admin"); err != nil {
		t.Fatalf("Create bravo: %v", err)
	}
	if err := us.Create("charlie", "pass3", "admin"); err != nil {
		t.Fatalf("Create charlie: %v", err)
	}

	users, err := us.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(users) != 3 {
		t.Fatalf("expected 3 users, got %d", len(users))
	}

	// Verify password hashes are not exposed
	for _, u := range users {
		if u.PasswordHash != "" {
			t.Errorf("expected empty PasswordHash in listing for %q", u.Username)
		}
	}

	// Verify all usernames are present
	names := map[string]bool{}
	for _, u := range users {
		names[u.Username] = true
	}
	for _, expected := range []string{"alpha", "bravo", "charlie"} {
		if !names[expected] {
			t.Errorf("expected username %q in listing", expected)
		}
	}
}

func TestAdminUser_ListEmpty(t *testing.T) {
	store := openTestStore(t)
	us := NewAdminUserStore(store, testJWTSecret)

	users, err := us.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(users) != 0 {
		t.Errorf("expected 0 users from empty store, got %d", len(users))
	}
}

func TestAdminUser_NewAdminUserStore_GeneratesSecret(t *testing.T) {
	store := openTestStore(t)
	us := NewAdminUserStore(store, nil)

	if err := us.Create("test", "pass", "admin"); err != nil {
		t.Fatalf("Create: %v", err)
	}

	token, err := us.Authenticate("test", "pass")
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}

	// Should be able to validate with the same store instance
	user, err := us.ValidateJWT(token)
	if err != nil {
		t.Fatalf("ValidateJWT: %v", err)
	}
	if user.Username != "test" {
		t.Errorf("expected username 'test', got %q", user.Username)
	}
}

// ---------------------------------------------------------------------------
// Middleware tests
// ---------------------------------------------------------------------------

func TestAdminAuthMiddleware_ValidJWT(t *testing.T) {
	store := openTestStore(t)
	us := NewAdminUserStore(store, testJWTSecret)

	if err := us.Create("mw-user", "pass", "admin"); err != nil {
		t.Fatalf("Create: %v", err)
	}

	token, err := us.Authenticate("mw-user", "pass")
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}

	handler := AdminAuthMiddleware(us)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user := GetAdminUser(r)
		if user == nil {
			t.Error("expected admin user in context")
			http.Error(w, "no user", http.StatusInternalServerError)
			return
		}
		if user.Username != "mw-user" {
			t.Errorf("expected username 'mw-user', got %q", user.Username)
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}))

	req := httptest.NewRequest(http.MethodGet, "/admin/test", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestAdminAuthMiddleware_NoAuth(t *testing.T) {
	store := openTestStore(t)
	us := NewAdminUserStore(store, testJWTSecret)

	handler := AdminAuthMiddleware(us)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler should not be called without auth")
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/admin/test", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rr.Code)
	}
}

func TestAdminAuthMiddleware_InvalidJWT(t *testing.T) {
	store := openTestStore(t)
	us := NewAdminUserStore(store, testJWTSecret)

	handler := AdminAuthMiddleware(us)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler should not be called with invalid JWT")
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/admin/test", nil)
	req.Header.Set("Authorization", "Bearer invalid.jwt.token")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rr.Code)
	}
}

func TestAdminAuthMiddleware_MalformedAuth(t *testing.T) {
	store := openTestStore(t)
	us := NewAdminUserStore(store, testJWTSecret)

	handler := AdminAuthMiddleware(us)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler should not be called with malformed auth")
		w.WriteHeader(http.StatusOK)
	}))

	// Test with Basic auth instead of Bearer
	req := httptest.NewRequest(http.MethodGet, "/admin/test", nil)
	req.Header.Set("Authorization", "Basic dXNlcjpwYXNz")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rr.Code)
	}
}

func TestGetAdminUser_NoContext(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	user := GetAdminUser(req)
	if user != nil {
		t.Errorf("expected nil admin user from request without context, got %+v", user)
	}
}
