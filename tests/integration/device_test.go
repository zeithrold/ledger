//go:build integration

package integration_test

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/zeithrold/ledger/internal/accounting"
	"github.com/zeithrold/ledger/internal/auth"
	"github.com/zeithrold/ledger/internal/httpserver"
	"github.com/zeithrold/ledger/internal/identity"
)

// TestAccountingDeviceServer exposes only an isolated test database. The fake
// verifier is compiled into integration tests, never the production application.
// A native test uses test-token, and the stop file ends the fixture cleanly.
func TestAccountingDeviceServer(t *testing.T) {
	path := os.Getenv("LEDGER_DEVICE_FIXTURE")
	if path == "" {
		t.Skip("set LEDGER_DEVICE_FIXTURE to a temporary output path for native tests")
	}
	directory := filepath.Dir(path)
	if directory != "/tmp" && directory != "/private/tmp" && directory != filepath.Clean(os.TempDir()) {
		t.Fatal("native fixture output must be directly inside a temporary directory")
	}
	rootDirectory := os.TempDir()
	if directory == "/tmp" || directory == "/private/tmp" {
		rootDirectory = "/tmp"
	}
	root, err := os.OpenRoot(rootDirectory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := root.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})
	filename := filepath.Base(path)
	db := phaseOneDB(t)
	backend := identity.New(db)
	_, _, err = backend.Bootstrap(t.Context(), auth.Identity{Issuer: "test", Subject: "test-token"}, identity.BootstrapInput{BaseCurrency: "USD", Timezone: "Asia/Shanghai", Locale: "en"})
	if err != nil {
		t.Fatal(err)
	}
	router, err := httpserver.New(db, httpserver.Dependencies{Backend: backend, Accounting: accounting.New(db), Verifier: localVerifier{}})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(router)
	defer server.Close()
	data, err := json.Marshal(map[string]string{"url": server.URL})
	if err != nil {
		t.Fatal(err)
	}
	if err = root.WriteFile(filename, data, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Log("native fixture ready at", server.URL)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	deadline := time.NewTimer(25 * time.Minute)
	defer deadline.Stop()
	for {
		select {
		case <-t.Context().Done():
			return
		case <-deadline.C:
			t.Fatal("native fixture timed out without a stop file")
		case <-ticker.C:
			if _, err = root.Stat(filename + ".stop"); err == nil {
				return
			}
		}
	}
}
