package gateway

import (
	"testing"
)

func TestConnectionConfig_CreateAndGet(t *testing.T) {
	store := openTestStore(t)
	cs := NewConnectionConfigStore(store)

	cfg := &ConnectionConfig{
		SystemID:    "engine1",
		Password:    "secret123",
		Description: "Test engine",
		Enabled:     true,
		AllowedIPs:  []string{"10.0.0.1", "10.0.0.2"},
		MaxTPS:      100,
		CostPerSMS:  0.05,
	}

	if err := cs.Create(cfg); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := cs.Get("engine1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil {
		t.Fatal("expected non-nil config")
	}
	if got.SystemID != "engine1" {
		t.Errorf("expected system_id 'engine1', got %q", got.SystemID)
	}
	if got.Description != "Test engine" {
		t.Errorf("expected description 'Test engine', got %q", got.Description)
	}
	if !got.Enabled {
		t.Error("expected Enabled=true")
	}
	if len(got.AllowedIPs) != 2 {
		t.Errorf("expected 2 allowed IPs, got %d", len(got.AllowedIPs))
	}
	if got.MaxTPS != 100 {
		t.Errorf("expected MaxTPS=100, got %d", got.MaxTPS)
	}
	if got.CostPerSMS != 0.05 {
		t.Errorf("expected CostPerSMS=0.05, got %f", got.CostPerSMS)
	}
	// Password should be a bcrypt hash, not the plaintext.
	if got.Password == "secret123" {
		t.Error("expected password to be hashed, got plaintext")
	}
	if got.Password == "" {
		t.Error("expected non-empty password hash")
	}
	if got.CreatedAt.IsZero() {
		t.Error("expected non-zero CreatedAt")
	}
	if got.UpdatedAt.IsZero() {
		t.Error("expected non-zero UpdatedAt")
	}
}

func TestConnectionConfig_Update(t *testing.T) {
	store := openTestStore(t)
	cs := NewConnectionConfigStore(store)

	cfg := &ConnectionConfig{
		SystemID:    "engine2",
		Password:    "original",
		Description: "Original desc",
		Enabled:     true,
		MaxTPS:      50,
	}
	if err := cs.Create(cfg); err != nil {
		t.Fatalf("Create: %v", err)
	}

	original, err := cs.Get("engine2")
	if err != nil {
		t.Fatalf("Get original: %v", err)
	}
	originalHash := original.Password

	// Update without changing password (empty password field).
	update := &ConnectionConfig{
		SystemID:    "engine2",
		Password:    "", // keep existing
		Description: "Updated desc",
		Enabled:     true,
		MaxTPS:      200,
	}
	if err := cs.Update(update); err != nil {
		t.Fatalf("Update: %v", err)
	}

	updated, err := cs.Get("engine2")
	if err != nil {
		t.Fatalf("Get updated: %v", err)
	}
	if updated.Description != "Updated desc" {
		t.Errorf("expected 'Updated desc', got %q", updated.Description)
	}
	if updated.MaxTPS != 200 {
		t.Errorf("expected MaxTPS=200, got %d", updated.MaxTPS)
	}
	if updated.Password != originalHash {
		t.Error("expected password hash to be preserved when no new password provided")
	}

	// Update with password change.
	update2 := &ConnectionConfig{
		SystemID:    "engine2",
		Password:    "newpassword",
		Description: "Updated desc 2",
		Enabled:     true,
		MaxTPS:      300,
	}
	if err := cs.Update(update2); err != nil {
		t.Fatalf("Update with password: %v", err)
	}

	updated2, err := cs.Get("engine2")
	if err != nil {
		t.Fatalf("Get updated2: %v", err)
	}
	if updated2.Password == originalHash {
		t.Error("expected password hash to change")
	}
	if updated2.Password == "newpassword" {
		t.Error("expected password to be hashed, got plaintext")
	}
	if updated2.MaxTPS != 300 {
		t.Errorf("expected MaxTPS=300, got %d", updated2.MaxTPS)
	}
}

func TestConnectionConfig_Delete(t *testing.T) {
	store := openTestStore(t)
	cs := NewConnectionConfigStore(store)

	cfg := &ConnectionConfig{
		SystemID: "engine3",
		Password: "pass",
		Enabled:  true,
	}
	if err := cs.Create(cfg); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := cs.Delete("engine3"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	got, err := cs.Get("engine3")
	if err != nil {
		t.Fatalf("Get after delete: %v", err)
	}
	if got != nil {
		t.Error("expected nil after delete")
	}
}

func TestConnectionConfig_List_HidesPasswords(t *testing.T) {
	store := openTestStore(t)
	cs := NewConnectionConfigStore(store)

	for _, id := range []string{"alpha", "bravo", "charlie"} {
		cfg := &ConnectionConfig{
			SystemID: id,
			Password: "pass-" + id,
			Enabled:  true,
		}
		if err := cs.Create(cfg); err != nil {
			t.Fatalf("Create %s: %v", id, err)
		}
	}

	configs, err := cs.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(configs) != 3 {
		t.Fatalf("expected 3 configs, got %d", len(configs))
	}

	ids := map[string]bool{}
	for _, c := range configs {
		if c.Password != "" {
			t.Errorf("expected empty Password in listing for %q, got non-empty", c.SystemID)
		}
		ids[c.SystemID] = true
	}
	for _, expected := range []string{"alpha", "bravo", "charlie"} {
		if !ids[expected] {
			t.Errorf("expected system_id %q in listing", expected)
		}
	}
}

func TestConnectionConfig_Authenticate_Valid(t *testing.T) {
	store := openTestStore(t)
	cs := NewConnectionConfigStore(store)

	cfg := &ConnectionConfig{
		SystemID: "auth1",
		Password: "correctpass",
		Enabled:  true,
	}
	if err := cs.Create(cfg); err != nil {
		t.Fatalf("Create: %v", err)
	}

	result, err := cs.Authenticate("auth1", "correctpass")
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil config")
	}
	if result.SystemID != "auth1" {
		t.Errorf("expected system_id 'auth1', got %q", result.SystemID)
	}
}

func TestConnectionConfig_Authenticate_WrongPassword(t *testing.T) {
	store := openTestStore(t)
	cs := NewConnectionConfigStore(store)

	cfg := &ConnectionConfig{
		SystemID: "auth2",
		Password: "correctpass",
		Enabled:  true,
	}
	if err := cs.Create(cfg); err != nil {
		t.Fatalf("Create: %v", err)
	}

	result, err := cs.Authenticate("auth2", "wrongpass")
	if result != nil {
		t.Error("expected nil config for wrong password")
	}
	if err == nil {
		t.Fatal("expected error for wrong password")
	}
}

func TestConnectionConfig_Authenticate_Disabled(t *testing.T) {
	store := openTestStore(t)
	cs := NewConnectionConfigStore(store)

	cfg := &ConnectionConfig{
		SystemID: "auth3",
		Password: "correctpass",
		Enabled:  false,
	}
	if err := cs.Create(cfg); err != nil {
		t.Fatalf("Create: %v", err)
	}

	result, err := cs.Authenticate("auth3", "correctpass")
	if result != nil {
		t.Error("expected nil config for disabled account")
	}
	if err == nil {
		t.Fatal("expected error for disabled account")
	}
}

func TestConnectionConfig_Authenticate_NotFound(t *testing.T) {
	store := openTestStore(t)
	cs := NewConnectionConfigStore(store)

	result, err := cs.Authenticate("nonexistent", "pass")
	if result != nil {
		t.Error("expected nil config for non-existent system_id")
	}
	if err != nil {
		t.Errorf("expected nil error for not found, got %v", err)
	}
}

func TestConnectionConfig_CreateDuplicate(t *testing.T) {
	store := openTestStore(t)
	cs := NewConnectionConfigStore(store)

	cfg := &ConnectionConfig{
		SystemID: "dup1",
		Password: "pass",
		Enabled:  true,
	}
	if err := cs.Create(cfg); err != nil {
		t.Fatalf("Create first: %v", err)
	}

	cfg2 := &ConnectionConfig{
		SystemID: "dup1",
		Password: "pass2",
		Enabled:  true,
	}
	err := cs.Create(cfg2)
	if err == nil {
		t.Fatal("expected error for duplicate system_id")
	}
}
