package smpp

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"testing"
	"time"

	"go.uber.org/zap"
)

// TestTLSConnect verifies that the SMPP client can establish a TLS connection
// to a TLS-enabled listener using TLSEnabled + TLSInsecureSkipVerify.
func TestTLSConnect(t *testing.T) {
	// Generate a self-signed certificate for the test TLS listener.
	cert, err := generateSelfSignedCert()
	if err != nil {
		t.Fatalf("failed to generate self-signed cert: %v", err)
	}

	tlsCfg := &tls.Config{
		Certificates: []tls.Certificate{cert},
	}

	// Start a TLS listener on a random port.
	ln, err := tls.Listen("tcp", "127.0.0.1:0", tlsCfg)
	if err != nil {
		t.Fatalf("failed to start TLS listener: %v", err)
	}
	defer ln.Close()

	addr := ln.Addr().(*net.TCPAddr)

	// Accept one connection and perform the SMPP bind handshake.
	handshakeDone := make(chan error, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			handshakeDone <- err
			return
		}
		defer conn.Close()

		// Read the bind_transceiver PDU from the client.
		headerBuf := make([]byte, pduHeaderLen)
		_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		if _, err := readFull(conn, headerBuf); err != nil {
			handshakeDone <- err
			return
		}

		pdu, err := DecodePDU(headerBuf)
		if err != nil {
			// Header-only decode may fail if body length > 0; read the full PDU.
			cmdLen := uint32(headerBuf[0])<<24 | uint32(headerBuf[1])<<16 | uint32(headerBuf[2])<<8 | uint32(headerBuf[3])
			fullBuf := make([]byte, cmdLen)
			copy(fullBuf, headerBuf)
			if cmdLen > pduHeaderLen {
				if _, err2 := readFull(conn, fullBuf[pduHeaderLen:]); err2 != nil {
					handshakeDone <- err2
					return
				}
			}
			pdu, err = DecodePDU(fullBuf)
			if err != nil {
				handshakeDone <- err
				return
			}
		}

		if pdu.CommandID != CmdBindTransceiver {
			handshakeDone <- nil
			return
		}

		// Send bind_transceiver_resp with StatusOK.
		resp := &PDU{
			CommandID:      CmdBindTransceiverResp,
			CommandStatus:  StatusOK,
			SequenceNumber: pdu.SequenceNumber,
			Body:           []byte("test-smsc\x00"),
		}
		data := EncodePDU(resp)
		_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
		if _, err := conn.Write(data); err != nil {
			handshakeDone <- err
			return
		}

		handshakeDone <- nil

		// Keep connection open until test completes.
		<-time.After(2 * time.Second)
	}()

	// Create client with TLS enabled.
	logger := zap.NewNop()
	client := NewClient(Config{
		Host:                  "127.0.0.1",
		Port:                  addr.Port,
		SystemID:              "test",
		Password:              "test",
		TLSEnabled:            true,
		TLSInsecureSkipVerify: true,
		EnquireLinkSec:        60,
	}, nil, logger)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Connect(ctx); err != nil {
		t.Fatalf("TLS Connect failed: %v", err)
	}
	defer client.Close()

	// Verify the connection is bound.
	if !client.IsBound() {
		t.Fatal("expected client to be bound after TLS connect")
	}

	// Verify the server-side handshake completed without error.
	if err := <-handshakeDone; err != nil {
		t.Fatalf("server-side handshake error: %v", err)
	}
}

// TestTLSConnectRefusedWithoutInsecure verifies that connecting to a server
// with a self-signed cert fails when TLSInsecureSkipVerify is false.
func TestTLSConnectRefusedWithoutInsecure(t *testing.T) {
	cert, err := generateSelfSignedCert()
	if err != nil {
		t.Fatalf("failed to generate self-signed cert: %v", err)
	}

	tlsCfg := &tls.Config{
		Certificates: []tls.Certificate{cert},
	}

	ln, err := tls.Listen("tcp", "127.0.0.1:0", tlsCfg)
	if err != nil {
		t.Fatalf("failed to start TLS listener: %v", err)
	}
	defer ln.Close()

	addr := ln.Addr().(*net.TCPAddr)

	// Accept connections in background (they will fail the handshake).
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()

	logger := zap.NewNop()
	client := NewClient(Config{
		Host:                  "127.0.0.1",
		Port:                  addr.Port,
		SystemID:              "test",
		Password:              "test",
		TLSEnabled:            true,
		TLSInsecureSkipVerify: false, // should reject self-signed cert
		EnquireLinkSec:        60,
	}, nil, logger)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err = client.Connect(ctx)
	if err == nil {
		client.Close()
		t.Fatal("expected TLS connection to fail with self-signed cert when InsecureSkipVerify=false")
	}

	// The error should be a certificate verification error.
	t.Logf("expected TLS error: %v", err)
}

// TestPlainConnectUnchanged verifies that the plain (non-TLS) path still works.
func TestPlainConnectUnchanged(t *testing.T) {
	// Start a plain TCP listener.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to start TCP listener: %v", err)
	}
	defer ln.Close()

	addr := ln.Addr().(*net.TCPAddr)

	handshakeDone := make(chan error, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			handshakeDone <- err
			return
		}
		defer conn.Close()

		headerBuf := make([]byte, pduHeaderLen)
		_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		if _, err := readFull(conn, headerBuf); err != nil {
			handshakeDone <- err
			return
		}

		cmdLen := uint32(headerBuf[0])<<24 | uint32(headerBuf[1])<<16 | uint32(headerBuf[2])<<8 | uint32(headerBuf[3])
		fullBuf := make([]byte, cmdLen)
		copy(fullBuf, headerBuf)
		if cmdLen > pduHeaderLen {
			if _, err := readFull(conn, fullBuf[pduHeaderLen:]); err != nil {
				handshakeDone <- err
				return
			}
		}

		pdu, err := DecodePDU(fullBuf)
		if err != nil {
			handshakeDone <- err
			return
		}

		resp := &PDU{
			CommandID:      CmdBindTransceiverResp,
			CommandStatus:  StatusOK,
			SequenceNumber: pdu.SequenceNumber,
			Body:           []byte("test-smsc\x00"),
		}
		data := EncodePDU(resp)
		_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
		if _, err := conn.Write(data); err != nil {
			handshakeDone <- err
			return
		}

		handshakeDone <- nil
		<-time.After(2 * time.Second)
	}()

	logger := zap.NewNop()
	client := NewClient(Config{
		Host:           "127.0.0.1",
		Port:           addr.Port,
		SystemID:       "test",
		Password:       "test",
		TLSEnabled:     false, // plain TCP
		EnquireLinkSec: 60,
	}, nil, logger)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Connect(ctx); err != nil {
		t.Fatalf("plain Connect failed: %v", err)
	}
	defer client.Close()

	if !client.IsBound() {
		t.Fatal("expected client to be bound after plain connect")
	}

	if err := <-handshakeDone; err != nil {
		t.Fatalf("server-side handshake error: %v", err)
	}
}

// readFull reads exactly len(buf) bytes from conn.
func readFull(conn net.Conn, buf []byte) (int, error) {
	n := 0
	for n < len(buf) {
		nn, err := conn.Read(buf[n:])
		n += nn
		if err != nil {
			return n, err
		}
	}
	return n, nil
}

// generateSelfSignedCert creates a self-signed TLS certificate for testing.
func generateSelfSignedCert() (tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}

	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "test-smsc"},
		NotBefore:    time.Now().Add(-1 * time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}

	certDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, err
	}

	return tls.Certificate{
		Certificate: [][]byte{certDER},
		PrivateKey:  key,
	}, nil
}
