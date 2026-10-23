package control

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"mihomori/internal/server/profile"
)

// TestBackupRestoreKeepsSettingsOverlay proves a WebDAV round trip preserves the
// program-owned settings overlay (bundle format needs no change).
func TestBackupRestoreKeepsSettingsOverlay(t *testing.T) {
	var stored string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPut:
			b, _ := io.ReadAll(r.Body)
			stored = string(b)
			w.WriteHeader(http.StatusCreated)
		case http.MethodGet:
			_, _ = io.WriteString(w, stored)
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer srv.Close()

	src := contractRouter(t)
	srcHandler := New(src)
	base, err := src.Profiles.Create(profile.CreateInput{Name: "base", Content: "mixed-port: 7890\n"})
	if err != nil {
		t.Fatalf("create base: %v", err)
	}
	if err := src.Profiles.SetActive(base.ID); err != nil {
		t.Fatalf("SetActive: %v", err)
	}
	if err := src.Profiles.ApplySettingsSection("mixed-port", 7897); err != nil {
		t.Fatalf("ApplySettingsSection: %v", err)
	}
	rec := doRequest(srcHandler, "POST", "/api/control/backup", `{"webdav":{"url":"`+srv.URL+`"}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("backup = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(stored, `"managedBy": "settings"`) {
		t.Fatalf("bundle lost the settings overlay marker: %s", stored)
	}

	// Restore into a fresh store, then compose and check the setting survives.
	dst := contractRouter(t)
	dstHandler := New(dst)
	rec = doRequest(dstHandler, "POST", "/api/control/restore", `{"webdav":{"url":"`+srv.URL+`"}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("restore = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	var restoredBaseID string
	overlays := 0
	for _, m := range dst.Profiles.List() {
		if m.ManagedBy == "settings" {
			overlays++
			continue
		}
		if m.Type != "merge" {
			restoredBaseID = m.ID
		}
	}
	if overlays != 1 {
		t.Fatalf("restored settings overlays = %d, want 1", overlays)
	}
	if restoredBaseID == "" {
		t.Fatal("restored base profile not found")
	}
	if err := dst.Profiles.SetActive(restoredBaseID); err != nil {
		t.Fatalf("SetActive on restored base: %v", err)
	}
	composed, err := os.ReadFile(dst.ActiveConfigPath)
	if err != nil {
		t.Fatalf("read composed active.yaml: %v", err)
	}
	if !strings.Contains(string(composed), "mixed-port: 7897") {
		t.Errorf("composed = %s, want the restored setting", composed)
	}
}
