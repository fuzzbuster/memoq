package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// withHome points MEMOQ_HOME at a fresh temp dir for the duration of a test.
func withHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("MEMOQ_HOME", dir)
	return dir
}

func TestResolvePaths_UsesMemoqHome(t *testing.T) {
	dir := withHome(t)
	p, err := ResolvePaths()
	if err != nil {
		t.Fatalf("ResolvePaths: %v", err)
	}
	if p.Home != dir {
		t.Errorf("Home = %q, want %q", p.Home, dir)
	}
	if want := filepath.Join(dir, "config.json"); p.ConfigFile != want {
		t.Errorf("ConfigFile = %q, want %q", p.ConfigFile, want)
	}
	if want := filepath.Join(dir, "memoq.db"); p.DBFile != want {
		t.Errorf("DBFile = %q, want %q", p.DBFile, want)
	}
}

func TestResolvePaths_DefaultUnderUserHome(t *testing.T) {
	t.Setenv("MEMOQ_HOME", "")
	p, err := ResolvePaths()
	if err != nil {
		t.Fatalf("ResolvePaths: %v", err)
	}
	u, _ := os.UserHomeDir()
	if want := filepath.Join(u, ".memoq"); p.Home != want {
		t.Errorf("Home = %q, want %q", p.Home, want)
	}
}

func TestLoad_MissingFileReturnsDefaults(t *testing.T) {
	withHome(t)
	p, _ := ResolvePaths()
	c, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.AutoSyncTTLSeconds != 30 {
		t.Errorf("default TTL = %d, want 30", c.AutoSyncTTLSeconds)
	}
	if c.ServerURL != "" || c.Token != "" {
		t.Errorf("expected empty server/token on first run, got %+v", c)
	}
}

func TestSaveThenLoad_RoundTrips(t *testing.T) {
	withHome(t)
	p, _ := ResolvePaths()
	in := &Config{ServerURL: "https://memos.example.com", Token: "secret-token", AutoSyncTTLSeconds: 45}
	if err := Save(p, in); err != nil {
		t.Fatalf("Save: %v", err)
	}
	out, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if *out != *in {
		t.Errorf("round-trip mismatch: got %+v want %+v", out, in)
	}
}

func TestSave_FilePermissionsAre0600(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix permission bits not meaningful on windows")
	}
	withHome(t)
	p, _ := ResolvePaths()
	if err := Save(p, &Config{ServerURL: "https://x", Token: "t"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	fi, err := os.Stat(p.ConfigFile)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Errorf("config perm = %o, want 600 (token must not be world-readable)", perm)
	}
}

func TestLoad_InvalidJSONErrors(t *testing.T) {
	withHome(t)
	p, _ := ResolvePaths()
	if err := os.MkdirAll(p.Home, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.ConfigFile, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(p); err == nil {
		t.Error("expected error on malformed config JSON")
	}
}

func TestValidate(t *testing.T) {
	cases := []struct {
		name    string
		cfg     Config
		wantErr bool
	}{
		{"ok", Config{ServerURL: "https://x", Token: "t"}, false},
		{"missing server", Config{Token: "t"}, true},
		{"missing token", Config{ServerURL: "https://x"}, true},
		{"empty", Config{}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.cfg.Validate()
			if (err != nil) != tc.wantErr {
				t.Errorf("Validate() err = %v, wantErr = %v", err, tc.wantErr)
			}
		})
	}
}

func TestSave_IsValidIndentedJSON(t *testing.T) {
	withHome(t)
	p, _ := ResolvePaths()
	if err := Save(p, &Config{ServerURL: "https://x", Token: "t", AutoSyncTTLSeconds: 30}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(p.ConfigFile)
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("saved config is not valid JSON: %v", err)
	}
}
