package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

func deviceServer(t *testing.T, polls int32, final map[string]string) *httptest.Server {
	t.Helper()
	var n int32
	mux := http.NewServeMux()
	mux.HandleFunc("/auth/device", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"device_code": "dev123", "user_code": "ABCD-EFGH", "verify_url": "http://example/#device-ABCD-EFGH",
			"interval": 1, "expires_in": 60,
		})
	})
	mux.HandleFunc("/auth/device/token", func(w http.ResponseWriter, r *http.Request) {
		c := atomic.AddInt32(&n, 1)
		if c < polls {
			json.NewEncoder(w).Encode(map[string]string{"status": "pending"})
			return
		}
		json.NewEncoder(w).Encode(final)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestLoginSavesTheTokenAndConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("TEND_HOME", home)
	srv := deviceServer(t, 2, map[string]string{"status": "ok", "token": "tend_abc123", "user": "Ann"})

	if err := cmdLogin([]string{srv.URL}); err != nil {
		t.Fatal(err)
	}

	tokenPath := filepath.Join(home, "coordinator.token")
	b, err := os.ReadFile(tokenPath)
	if err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(tokenPath); fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", fi.Mode())
	}
	if got := string(b); got != "tend_abc123\n" {
		t.Fatalf("%q", got)
	}
	cfg := loadConfig()
	if cfg.Coordinator == nil || cfg.Coordinator.URL != srv.URL || cfg.Coordinator.TokenFile != tokenPath {
		t.Fatalf("%+v", cfg.Coordinator)
	}
}

func TestLoginReportsDenial(t *testing.T) {
	home := t.TempDir()
	t.Setenv("TEND_HOME", home)
	srv := deviceServer(t, 1, map[string]string{"status": "denied"})

	err := cmdLogin([]string{srv.URL})
	if err == nil {
		t.Fatal("expected an error")
	}
	if _, statErr := os.Stat(filepath.Join(home, "coordinator.token")); statErr == nil {
		t.Fatal("no token should be saved on denial")
	}
}

func TestLoginNeedsAURL(t *testing.T) {
	home := t.TempDir()
	t.Setenv("TEND_HOME", home)
	if err := cmdLogin(nil); err == nil {
		t.Fatal("expected an error with no url and no configured coordinator")
	}
}
