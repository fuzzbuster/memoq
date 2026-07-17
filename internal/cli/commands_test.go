package cli

import (
	"errors"
	"testing"

	"github.com/example/memoq/internal/config"
)

// TestGetConfigKey_TokenNeverEchoed guards the security invariant: reading the
// token back must never reveal the secret, only a masked marker.
func TestGetConfigKey_TokenNeverEchoed(t *testing.T) {
	c := &config.Config{ServerURL: "https://memos.example.com", Token: "super-secret-value", AutoSyncTTLSeconds: 30}

	v, err := getConfigKey(c, "token")
	if err != nil {
		t.Fatal(err)
	}
	if v == "super-secret-value" {
		t.Fatal("SECURITY: getConfigKey leaked the raw token")
	}
	if v != "***set***" {
		t.Errorf("token readout = %q, want ***set***", v)
	}

	// server_url and ttl are not secret and returned verbatim.
	if v, _ := getConfigKey(c, "server_url"); v != "https://memos.example.com" {
		t.Errorf("server_url = %q", v)
	}
	if v, _ := getConfigKey(c, "auto_sync_ttl_seconds"); v != "30" {
		t.Errorf("ttl = %q", v)
	}
}

func TestGetConfigKey_UnsetTokenIsEmpty(t *testing.T) {
	c := &config.Config{ServerURL: "https://x"}
	v, err := getConfigKey(c, "token")
	if err != nil {
		t.Fatal(err)
	}
	if v != "" {
		t.Errorf("unset token readout = %q, want empty", v)
	}
}

func TestGetConfigKey_UnknownKeyErrors(t *testing.T) {
	if _, err := getConfigKey(&config.Config{}, "bogus"); err == nil {
		t.Error("expected error for unknown key")
	}
}

func TestSetConfigKey(t *testing.T) {
	c := &config.Config{}
	if err := setConfigKey(c, "server_url", "https://memos.test/"); err != nil {
		t.Fatal(err)
	}
	if c.ServerURL != "https://memos.test" {
		t.Errorf("server_url = %q, want trailing slash trimmed", c.ServerURL)
	}
	if err := setConfigKey(c, "token", "abc"); err != nil {
		t.Fatal(err)
	}
	if c.Token != "abc" {
		t.Errorf("token = %q", c.Token)
	}
	if err := setConfigKey(c, "auto_sync_ttl_seconds", "90"); err != nil {
		t.Fatal(err)
	}
	if c.AutoSyncTTLSeconds != 90 {
		t.Errorf("ttl = %d, want 90", c.AutoSyncTTLSeconds)
	}
}

func TestSetConfigKey_Errors(t *testing.T) {
	c := &config.Config{}
	if err := setConfigKey(c, "auto_sync_ttl_seconds", "notanumber"); err == nil {
		t.Error("expected error for non-integer ttl")
	}
	if err := setConfigKey(c, "bogus", "x"); err == nil {
		t.Error("expected error for unknown key")
	}
}

// TestParseArgs_FlagsAnywhere verifies flags can appear before, after, or
// interspersed with positional args (agents often write the flag last).
func TestParseArgs_FlagsAnywhere(t *testing.T) {
	// reuse cmdSearch's flexible parser indirectly via parseArgs on a fresh flagset
	// is covered by resource tests; here we just check ordering variants resolve.
	cases := [][]string{
		{"memo", "get", "abc", "--query", "x=1"},
		{"memo", "get", "--query", "x=1", "abc"},
	}
	for _, args := range cases {
		c := stubRunAPI(t)
		if err := Run(args); err != nil {
			t.Fatalf("Run(%v): %v", args, err)
		}
		if c.path != "/api/v1/memos/abc?x=1" {
			t.Errorf("args %v -> path %q, want /api/v1/memos/abc?x=1", args, c.path)
		}
	}
}

func TestParseDate(t *testing.T) {
	from, err := parseDate("2026-01-15", false)
	if err != nil {
		t.Fatal(err)
	}
	to, err := parseDate("2026-01-15", true)
	if err != nil {
		t.Fatal(err)
	}
	// end-of-day must be later than start-of-day by ~a day minus a second
	if to <= from {
		t.Errorf("end-of-day (%d) should exceed start (%d)", to, from)
	}
	if to-from != 24*3600-1 {
		t.Errorf("endOfDay delta = %d, want %d", to-from, 24*3600-1)
	}
	if _, err := parseDate("nonsense", false); err == nil {
		t.Error("expected error for malformed date")
	}
}

func TestFastWriteDryRunAndConfirmation(t *testing.T) {
	if err := cmdCreate([]string{"--content", "hello", "--dry-run"}); err != nil {
		t.Fatalf("create dry-run: %v", err)
	}
	if err := cmdUpdate([]string{"abc", "--content", "hello", "--dry-run"}); err != nil {
		t.Fatalf("update dry-run: %v", err)
	}
	if err := cmdDelete([]string{"abc", "--dry-run"}); err != nil {
		t.Fatalf("delete dry-run: %v", err)
	}

	err := cmdDelete([]string{"abc"})
	var ce *cliError
	if !errors.As(err, &ce) || ce.Code != "confirmation_required" {
		t.Fatalf("delete error = %v, want confirmation_required", err)
	}
}

func TestOneLine_Truncates(t *testing.T) {
	got := oneLine("line one\nline two with many words here", 10)
	r := []rune(got)
	// 10 runes + ellipsis
	if len(r) != 11 {
		t.Errorf("oneLine length = %d runes, want 11 (10 + ellipsis)", len(r))
	}
	if r[len(r)-1] != '…' {
		t.Errorf("truncated line should end with ellipsis, got %q", got)
	}
}
