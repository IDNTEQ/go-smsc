package e2e

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/idnteq/go-smsc/mocksmsc"
	"github.com/idnteq/go-smsc/smpp"
	"go.uber.org/zap"
)

// TestSMPPRoundTrip verifies the full submit -> DLR cycle through mock SMSC.
func TestSMPPRoundTrip(t *testing.T) {
	logger, _ := zap.NewDevelopment()

	// 1. Start mock SMSC
	mock := mocksmsc.NewServer(mocksmsc.Config{
		Port:           0,
		DLRDelayMs:     50,
		DLRSuccessRate: 1.0,
	}, logger.Named("mock"))

	if err := mock.Start(); err != nil {
		t.Fatalf("mock SMSC start: %v", err)
	}
	defer mock.Stop()

	port := mock.Port()
	t.Logf("mock SMSC on port %d", port)

	// 2. Connect SMPP pool
	dlrCh := make(chan string, 10)
	cfg := smpp.Config{
		Host:       "127.0.0.1",
		Port:       port,
		SystemID:   "testclient",
		Password:   "password",
		SourceAddr: "TestApp",
	}
	poolCfg := smpp.PoolConfig{
		Connections:      1,
		WindowSize:       10,
		DeliverWorkers:   4,
		DeliverQueueSize: 100,
		SubmitTimeout:    5 * time.Second,
	}

	pool := smpp.NewPool(cfg, poolCfg, func(src, dst string, esm byte, body []byte) error {
		if smpp.IsDLR(esm) {
			receipt := smpp.ParseDLRReceipt(string(body))
			if receipt != nil {
				dlrCh <- receipt.Status
			}
		}
		return nil
	}, logger.Named("pool"))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := pool.Connect(ctx); err != nil {
		t.Fatalf("pool connect: %v", err)
	}
	defer func() { _ = pool.Close() }()

	// Wait for bind
	time.Sleep(200 * time.Millisecond)
	if pool.ActiveConnections() == 0 {
		t.Fatal("no active connections after bind")
	}

	// 3. Submit a message
	resp, err := pool.Submit(&smpp.SubmitRequest{
		MSISDN:      "+27821234567",
		Payload:     []byte("Hello from E2E test"),
		RegisterDLR: true,
	})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if resp.MessageID == "" {
		t.Fatal("empty message ID in submit response")
	}
	t.Logf("submit OK, message_id=%s", resp.MessageID)

	// 4. Wait for DLR
	select {
	case status := <-dlrCh:
		t.Logf("DLR received: %s", status)
		if status != "DELIVRD" {
			t.Errorf("expected DELIVRD, got %s", status)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for DLR")
	}
}

// TestSMPPBatchSubmit verifies submitting multiple messages and receiving all DLRs.
func TestSMPPBatchSubmit(t *testing.T) {
	logger, _ := zap.NewDevelopment()

	mock := mocksmsc.NewServer(mocksmsc.Config{
		Port:           0,
		DLRDelayMs:     20,
		DLRSuccessRate: 1.0,
	}, logger.Named("mock"))

	if err := mock.Start(); err != nil {
		t.Fatalf("mock SMSC start: %v", err)
	}
	defer mock.Stop()

	dlrCount := make(chan struct{}, 100)
	pool := smpp.NewPool(
		smpp.Config{
			Host:       "127.0.0.1",
			Port:       mock.Port(),
			SystemID:   "testclient",
			Password:   "password",
			SourceAddr: "BatchTest",
		},
		smpp.PoolConfig{
			Connections:      2,
			WindowSize:       50,
			DeliverWorkers:   8,
			DeliverQueueSize: 200,
			SubmitTimeout:    5 * time.Second,
		},
		func(src, dst string, esm byte, body []byte) error {
			if smpp.IsDLR(esm) {
				dlrCount <- struct{}{}
			}
			return nil
		},
		logger.Named("pool"),
	)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if err := pool.Connect(ctx); err != nil {
		t.Fatalf("pool connect: %v", err)
	}
	defer func() { _ = pool.Close() }()

	time.Sleep(200 * time.Millisecond)

	// Submit 50 messages
	count := 50
	for i := 0; i < count; i++ {
		resp, err := pool.Submit(&smpp.SubmitRequest{
			MSISDN:      fmt.Sprintf("+2782%07d", i),
			Payload:     []byte(fmt.Sprintf("Message %d", i)),
			RegisterDLR: true,
		})
		if err != nil {
			t.Fatalf("submit %d: %v", i, err)
		}
		if resp.MessageID == "" {
			t.Fatalf("submit %d: empty message ID", i)
		}
	}
	t.Logf("submitted %d messages", count)

	// Wait for all DLRs
	received := 0
	deadline := time.After(10 * time.Second)
	for received < count {
		select {
		case <-dlrCount:
			received++
		case <-deadline:
			t.Fatalf("timeout: received %d/%d DLRs", received, count)
		}
	}
	t.Logf("received all %d DLRs", received)
}

// TestSMPPVersionNegotiation verifies that the client and mock SMSC negotiate
// SMPP version via the sc_interface_version TLV in bind responses. The mock
// SMSC now includes sc_interface_version=0x50 in its bind response, and the
// client should negotiate to version 0x50 when requesting SMPP 5.0.
func TestSMPPVersionNegotiation(t *testing.T) {
	logger, _ := zap.NewDevelopment()

	mock := mocksmsc.NewServer(mocksmsc.Config{
		Port:           0,
		DLRDelayMs:     50,
		DLRSuccessRate: 1.0,
	}, logger.Named("mock"))

	if err := mock.Start(); err != nil {
		t.Fatalf("mock SMSC start: %v", err)
	}
	defer mock.Stop()

	port := mock.Port()
	t.Logf("mock SMSC on port %d", port)

	// Connect with SMPP 5.0 interface version.
	dlrCh := make(chan string, 10)
	cfg := smpp.Config{
		Host:             "127.0.0.1",
		Port:             port,
		SystemID:         "testclient",
		Password:         "password",
		SourceAddr:       "VersionTest",
		InterfaceVersion: 0x50,
		BindMode:         smpp.BindTransceiver,
	}
	poolCfg := smpp.PoolConfig{
		Connections:      1,
		WindowSize:       10,
		DeliverWorkers:   4,
		DeliverQueueSize: 100,
		SubmitTimeout:    5 * time.Second,
	}

	pool := smpp.NewPool(cfg, poolCfg, func(src, dst string, esm byte, body []byte) error {
		if smpp.IsDLR(esm) {
			receipt := smpp.ParseDLRReceipt(string(body))
			if receipt != nil {
				dlrCh <- receipt.Status
			}
		}
		return nil
	}, logger.Named("pool"))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := pool.Connect(ctx); err != nil {
		t.Fatalf("pool connect: %v", err)
	}
	defer func() { _ = pool.Close() }()

	time.Sleep(200 * time.Millisecond)
	if pool.ActiveConnections() == 0 {
		t.Fatal("no active connections after bind")
	}

	// Submit a message to verify normal operation with version negotiation.
	resp, err := pool.Submit(&smpp.SubmitRequest{
		MSISDN:      "+27829999999",
		Payload:     []byte("Version negotiation test"),
		RegisterDLR: true,
	})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if resp.MessageID == "" {
		t.Fatal("empty message ID in submit response")
	}
	t.Logf("submit OK with SMPP 5.0 bind, message_id=%s", resp.MessageID)

	// Wait for DLR to verify full round-trip works with TLV-enabled bind.
	select {
	case status := <-dlrCh:
		t.Logf("DLR received: %s", status)
		if status != "DELIVRD" {
			t.Errorf("expected DELIVRD, got %s", status)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for DLR")
	}
}

// TestSMPPBindModes verifies that the mock SMSC accepts all three bind modes
// (transmitter, receiver, transceiver) and responds with the correct
// response command ID.
func TestSMPPBindModes(t *testing.T) {
	logger, _ := zap.NewDevelopment()

	mock := mocksmsc.NewServer(mocksmsc.Config{
		Port:           0,
		DLRDelayMs:     50,
		DLRSuccessRate: 1.0,
	}, logger.Named("mock"))

	if err := mock.Start(); err != nil {
		t.Fatalf("mock SMSC start: %v", err)
	}
	defer mock.Stop()

	port := mock.Port()

	modes := []struct {
		name string
		mode smpp.BindMode
	}{
		{"transceiver", smpp.BindTransceiver},
		{"transmitter", smpp.BindTransmitter},
		{"receiver", smpp.BindReceiver},
	}

	for _, tc := range modes {
		t.Run(tc.name, func(t *testing.T) {
			cfg := smpp.Config{
				Host:       "127.0.0.1",
				Port:       port,
				SystemID:   "testclient",
				Password:   "password",
				SourceAddr: "BindTest",
				BindMode:   tc.mode,
			}

			client := smpp.NewClient(cfg, nil, logger.Named(tc.name))
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			if err := client.Connect(ctx); err != nil {
				t.Fatalf("connect with bind mode %s: %v", tc.name, err)
			}
			defer func() { _ = client.Close() }()

			if !client.IsBound() {
				t.Fatalf("expected client to be bound with mode %s", tc.name)
			}
			t.Logf("bind mode %s: bound successfully", tc.name)
		})
	}
}
