package webhook

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"sigs.k8s.io/dranet/pkg/cloudprovider"
)

func TestWebhookCapabilitiesAndPost(t *testing.T) {
	// Start a mock HTTP server
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == PathHealth {
			caps := Capabilities{CloudProvider: false, ProfileProvider: true}
			json.NewEncoder(w).Encode(caps)
			return
		}
		if r.URL.Path == PathGetProfileConfig {
			// Dummy response
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	ctx := context.Background()

	// 1. Test OnWebhook (should succeed because /health returns 200)
	if !OnWebhook(ctx, srv.URL) {
		t.Errorf("OnWebhook failed to discover a healthy webhook")
	}

	// 2. Test NewWebhookProvider stores capabilities
	provider, err := NewWebhookProvider(ctx, srv.URL)
	if err != nil {
		t.Fatalf("NewWebhookProvider failed: %v", err)
	}
	if provider.caps.CloudProvider {
		t.Errorf("Expected CloudProvider capability to be false")
	}
	if !provider.caps.ProfileProvider {
		t.Errorf("Expected ProfileProvider capability to be true")
	}

	// 3. Test Unsupported CloudProvider Method returns 501
	id := cloudprovider.DeviceIdentifiers{}
	attrs := provider.GetDeviceAttributes(id)
	if attrs != nil {
		t.Errorf("Expected GetDeviceAttributes to fail and return nil due to lack of capability")
	}

	// 4. Test Supported ProfileProvider Method works
	_, err = provider.GetProfileConfig(id, "test-profile", "claim-123")
	if err != nil {
		t.Errorf("Expected GetProfileConfig to succeed, got error: %v", err)
	}
}

func TestWebhookUnixSocket(t *testing.T) {
	// Create a temporary file for the unix socket
	f, err := os.CreateTemp("", "webhook-test-*.sock")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	sockPath := f.Name()
	f.Close()
	os.Remove(sockPath)

	l, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("failed to listen on unix socket: %v", err)
	}
	defer l.Close()
	defer os.Remove(sockPath)

	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == PathHealth {
				caps := Capabilities{CloudProvider: true, ProfileProvider: true}
				json.NewEncoder(w).Encode(caps)
				return
			}
			w.WriteHeader(http.StatusOK)
		}),
	}
	go srv.Serve(l)
	defer srv.Close()

	// Give the server a moment to start
	time.Sleep(100 * time.Millisecond)

	ctx := context.Background()
	socketURL := "unix://" + sockPath

	if !OnWebhook(ctx, socketURL) {
		t.Errorf("OnWebhook failed over unix socket")
	}

	provider, err := NewWebhookProvider(ctx, socketURL)
	if err != nil {
		t.Fatalf("NewWebhookProvider failed over unix socket: %v", err)
	}

	if !provider.caps.CloudProvider || !provider.caps.ProfileProvider {
		t.Errorf("Failed to retrieve capabilities over unix socket")
	}
}
