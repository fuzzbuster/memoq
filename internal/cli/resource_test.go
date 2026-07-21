package cli

import (
	"encoding/json"
	"errors"
	"testing"
)

// captured records the arguments the stubbed runAPI received.
type captured struct {
	method    string
	path      string
	body      any
	syncCache bool
}

// stubRunAPI replaces the package-level runAPI for the duration of a test,
// capturing the constructed request instead of performing it.
func stubRunAPI(t *testing.T) *captured {
	t.Helper()
	c := &captured{}
	orig := runAPI
	runAPI = func(method, path string, body any, syncCache bool) error {
		c.method, c.path, c.body, c.syncCache = method, path, body, syncCache
		return nil
	}
	t.Cleanup(func() { runAPI = orig })
	return c
}

// TestResourcePaths asserts the exact method+path each resource verb builds.
// These are the regression guards for the upstream-drift corrections: attachments
// (not resources), instance (not workspace), auth/me (not auth/status),
// personalAccessTokens, custom verbs with ':' etc.
func TestResourcePaths(t *testing.T) {
	cases := []struct {
		name       string
		args       []string // full argv after the program name
		wantMethod string
		wantPath   string
	}{
		// memo
		{"memo list", []string{"memo", "list"}, "GET", "/api/v1/memos"},
		{"memo list query", []string{"memo", "list", "--query", "pageSize=10"}, "GET", "/api/v1/memos?pageSize=10"},
		{"memo get", []string{"memo", "get", "abc"}, "GET", "/api/v1/memos/abc"},
		{"memo create", []string{"memo", "create", "--field", "content=hi"}, "POST", "/api/v1/memos"},
		{"memo update", []string{"memo", "update", "abc", "--field", "pinned=true", "--query", "updateMask=pinned"}, "PATCH", "/api/v1/memos/abc?updateMask=pinned"},
		{"memo delete", []string{"memo", "delete", "abc", "--yes"}, "DELETE", "/api/v1/memos/abc"},
		{"memo comments", []string{"memo", "comments", "abc"}, "GET", "/api/v1/memos/abc/comments"},
		{"memo comment", []string{"memo", "comment", "abc", "--field", "content=hey"}, "POST", "/api/v1/memos/abc/comments"},
		{"memo relations", []string{"memo", "relations", "abc"}, "GET", "/api/v1/memos/abc/relations"},
		{"memo set-relations", []string{"memo", "set-relations", "abc", "--body", "{}"}, "PATCH", "/api/v1/memos/abc/relations"},
		{"memo reactions", []string{"memo", "reactions", "abc"}, "GET", "/api/v1/memos/abc/reactions"},
		{"memo react", []string{"memo", "react", "abc", "--field", "reactionType=THUMBS_UP"}, "POST", "/api/v1/memos/abc/reactions"},
		{"memo unreact", []string{"memo", "unreact", "abc", "r1", "--yes"}, "DELETE", "/api/v1/memos/abc/reactions/r1"},
		{"memo attachments", []string{"memo", "attachments", "abc"}, "GET", "/api/v1/memos/abc/attachments"},
		{"memo set-attachments", []string{"memo", "set-attachments", "abc", "--body", "{}"}, "PATCH", "/api/v1/memos/abc/attachments"},
		{"memo shares", []string{"memo", "shares", "abc"}, "GET", "/api/v1/memos/abc/shares"},
		{"memo share", []string{"memo", "share", "abc", "--body", "{}", "--yes"}, "POST", "/api/v1/memos/abc/shares"},
		{"memo unshare", []string{"memo", "unshare", "abc", "s1", "--yes"}, "DELETE", "/api/v1/memos/abc/shares/s1"},
		{"memo link-metadata", []string{"memo", "link-metadata", "--query", "link=http://x"}, "GET", "/api/v1/memos/-/linkMetadata?link=http%3A%2F%2Fx"},

		// attachment
		{"attachment list", []string{"attachment", "list"}, "GET", "/api/v1/attachments"},
		{"attachment get", []string{"attachment", "get", "id1"}, "GET", "/api/v1/attachments/id1"},
		{"attachment create", []string{"attachment", "create", "--body", "{}"}, "POST", "/api/v1/attachments"},
		{"attachment update", []string{"attachment", "update", "id1", "--body", "{}"}, "PATCH", "/api/v1/attachments/id1"},
		{"attachment delete", []string{"attachment", "delete", "id1", "--yes"}, "DELETE", "/api/v1/attachments/id1"},
		{"attachment batch-delete", []string{"attachment", "batch-delete", "--body", "{}", "--yes"}, "POST", "/api/v1/attachments:batchDelete"},

		// user
		{"user list", []string{"user", "list"}, "GET", "/api/v1/users"},
		{"user get", []string{"user", "get", "me"}, "GET", "/api/v1/users/me"},
		{"user create", []string{"user", "create", "--body", "{}"}, "POST", "/api/v1/users"},
		{"user update", []string{"user", "update", "1", "--body", "{}"}, "PATCH", "/api/v1/users/1"},
		{"user delete", []string{"user", "delete", "1", "--yes"}, "DELETE", "/api/v1/users/1"},
		{"user all-stats", []string{"user", "all-stats"}, "GET", "/api/v1/users:stats"},
		{"user stats", []string{"user", "stats", "1"}, "GET", "/api/v1/users/1:getStats"},
		{"user settings", []string{"user", "settings", "1"}, "GET", "/api/v1/users/1/settings"},
		{"user setting", []string{"user", "setting", "1", "GENERAL"}, "GET", "/api/v1/users/1/settings/GENERAL"},
		{"user update-setting", []string{"user", "update-setting", "1", "GENERAL", "--body", "{}", "--yes"}, "PATCH", "/api/v1/users/1/settings/GENERAL"},
		{"user tokens", []string{"user", "tokens", "1"}, "GET", "/api/v1/users/1/personalAccessTokens"},
		{"user create-token", []string{"user", "create-token", "1", "--body", "{}"}, "POST", "/api/v1/users/1/personalAccessTokens"},
		{"user delete-token", []string{"user", "delete-token", "1", "tok9", "--yes"}, "DELETE", "/api/v1/users/1/personalAccessTokens/tok9"},
		{"user webhooks", []string{"user", "webhooks", "1"}, "GET", "/api/v1/users/1/webhooks"},

		// auth (drift-corrected)
		{"auth me", []string{"auth", "me"}, "GET", "/api/v1/auth/me"},
		{"auth status alias", []string{"auth", "status"}, "GET", "/api/v1/auth/me"},
		{"auth signin", []string{"auth", "signin", "--body", "{}"}, "POST", "/api/v1/auth/signin"},
		{"auth signout", []string{"auth", "signout"}, "POST", "/api/v1/auth/signout"},
		{"auth refresh", []string{"auth", "refresh", "--body", "{}"}, "POST", "/api/v1/auth/refresh"},

		// shortcut
		{"shortcut list", []string{"shortcut", "list", "1"}, "GET", "/api/v1/users/1/shortcuts"},
		{"shortcut get", []string{"shortcut", "get", "1", "s2"}, "GET", "/api/v1/users/1/shortcuts/s2"},
		{"shortcut create", []string{"shortcut", "create", "1", "--body", "{}"}, "POST", "/api/v1/users/1/shortcuts"},
		{"shortcut update", []string{"shortcut", "update", "1", "s2", "--body", "{}"}, "PATCH", "/api/v1/users/1/shortcuts/s2"},
		{"shortcut delete", []string{"shortcut", "delete", "1", "s2", "--yes"}, "DELETE", "/api/v1/users/1/shortcuts/s2"},

		// instance (drift-corrected)
		{"instance profile", []string{"instance", "profile"}, "GET", "/api/v1/instance/profile"},
		{"instance setting collection", []string{"instance", "setting"}, "GET", "/api/v1/instance/settings"},
		{"instance setting key", []string{"instance", "setting", "GENERAL"}, "GET", "/api/v1/instance/settings/GENERAL"},
		{"instance update-setting", []string{"instance", "update-setting", "GENERAL", "--body", "{}", "--yes"}, "PATCH", "/api/v1/instance/settings/GENERAL"},
		{"instance stats", []string{"instance", "stats"}, "GET", "/api/v1/instance/stats"},

		// ai (custom verb)
		{"ai transcribe", []string{"ai", "transcribe", "--body", "{}"}, "POST", "/api/v1/ai:transcribe"},

		// generic escape hatch
		{"api get", []string{"api", "GET", "/api/v1/memos", "--query", "pageSize=5"}, "GET", "/api/v1/memos?pageSize=5"},
		{"api post lowercase method", []string{"api", "post", "/api/v1/memos", "--field", "content=x"}, "POST", "/api/v1/memos"},
		{"api path missing slash", []string{"api", "GET", "api/v1/memos"}, "GET", "/api/v1/memos"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := stubRunAPI(t)
			if err := Run(tc.args); err != nil {
				t.Fatalf("Run(%v): %v", tc.args, err)
			}
			if c.method != tc.wantMethod {
				t.Errorf("method = %q, want %q", c.method, tc.wantMethod)
			}
			if c.path != tc.wantPath {
				t.Errorf("path = %q, want %q", c.path, tc.wantPath)
			}
		})
	}
}

func TestBodyBuilding_FieldCoercion(t *testing.T) {
	c := stubRunAPI(t)
	if err := Run([]string{"memo", "create",
		"--field", "content=hello world",
		"--field", "pinned=true",
		"--field", "priority=3",
	}); err != nil {
		t.Fatal(err)
	}
	obj, ok := c.body.(map[string]any)
	if !ok {
		t.Fatalf("body type = %T, want map", c.body)
	}
	if obj["content"] != "hello world" {
		t.Errorf("content = %v (want string)", obj["content"])
	}
	if obj["pinned"] != true {
		t.Errorf("pinned = %v (want bool true)", obj["pinned"])
	}
	// JSON numbers decode to float64
	if obj["priority"] != float64(3) {
		t.Errorf("priority = %v (want number 3)", obj["priority"])
	}
}

func TestBodyBuilding_RawBodyWinsOverFields(t *testing.T) {
	c := stubRunAPI(t)
	if err := Run([]string{"memo", "create",
		"--body", `{"content":"raw"}`,
		"--field", "content=ignored",
	}); err != nil {
		t.Fatal(err)
	}
	raw, ok := c.body.(json.RawMessage)
	if !ok {
		t.Fatalf("body type = %T, want json.RawMessage", c.body)
	}
	if string(raw) != `{"content":"raw"}` {
		t.Errorf("raw body = %s", raw)
	}
}

func TestResourceDryRunDoesNotCallAPI(t *testing.T) {
	c := stubRunAPI(t)
	if err := Run([]string{"memo", "delete", "abc", "--dry-run"}); err != nil {
		t.Fatal(err)
	}
	if c.method != "" {
		t.Fatalf("dry-run called API with method %q", c.method)
	}
}

func TestResourceConfirmationPolicy(t *testing.T) {
	c := stubRunAPI(t)
	err := Run([]string{"memo", "delete", "abc"})
	if err == nil {
		t.Fatal("memo delete without --yes should fail")
	}
	var ce *cliError
	if !errors.As(err, &ce) || ce.Code != "confirmation_required" {
		t.Fatalf("error = %v, want confirmation_required", err)
	}
	if c.method != "" {
		t.Fatalf("unconfirmed delete called API with method %q", c.method)
	}

	if err := Run([]string{"memo", "update", "abc", "--field", "content=x"}); err != nil {
		t.Fatalf("high-frequency update should not require confirmation: %v", err)
	}
}

func TestResourceCacheSyncPolicy(t *testing.T) {
	c := stubRunAPI(t)
	if err := Run([]string{"memo", "update", "abc", "--field", "content=x"}); err != nil {
		t.Fatal(err)
	}
	if !c.syncCache {
		t.Fatal("memo update should sync the local memo cache")
	}

	c = stubRunAPI(t)
	if err := Run([]string{"api", "PATCH", "/api/v1/memos/abc", "--field", "content=x", "--sync"}); err != nil {
		t.Fatal(err)
	}
	if !c.syncCache {
		t.Fatal("api --sync should sync the local memo cache")
	}
}

func TestGenericAPIDeleteRequiresConfirmation(t *testing.T) {
	c := stubRunAPI(t)
	if err := Run([]string{"api", "DELETE", "/api/v1/memos/abc"}); err == nil {
		t.Fatal("generic DELETE without --yes should fail")
	}
	if c.method != "" {
		t.Fatalf("unconfirmed generic DELETE called API with method %q", c.method)
	}
	if err := Run([]string{"api", "DELETE", "/api/v1/memos/abc", "--yes"}); err != nil {
		t.Fatal(err)
	}
}

func TestGenericAPIReadSharesDoesNotRequireConfirmation(t *testing.T) {
	c := stubRunAPI(t)
	if err := Run([]string{"api", "GET", "/api/v1/memos/abc/shares"}); err != nil {
		t.Fatal(err)
	}
	if c.method != "GET" {
		t.Fatalf("method = %q, want GET", c.method)
	}
}

func TestResourceDryRunReportsExplicitSync(t *testing.T) {
	if got := cachePolicy(true); got != "sync" {
		t.Fatalf("cachePolicy(true) = %q, want sync", got)
	}
}

func TestUnknownVerbAndResource(t *testing.T) {
	stubRunAPI(t)
	if err := Run([]string{"memo", "bogus"}); err == nil {
		t.Error("expected error for unknown memo verb")
	}
	if err := Run([]string{"nonsense"}); err == nil {
		t.Error("expected error for unknown top-level command")
	}
}

func TestResourceGroupNoVerbShowsUsage(t *testing.T) {
	stubRunAPI(t)
	// Calling a resource group with no verb returns a usage error (non-nil).
	if err := Run([]string{"memo"}); err == nil {
		t.Error("expected usage error when resource group called with no verb")
	}
}

func TestMemoRelateRequiresTwoMemoUIDs(t *testing.T) {
	if err := Run([]string{"memo", "relate", "source"}); err == nil {
		t.Fatal("memo relate with one UID should fail")
	}
}

func TestMemoResourceVerbsIncludeRelate(t *testing.T) {
	for _, verb := range resourceVerbs("memo") {
		if verb == "relate" {
			return
		}
	}
	t.Fatal("memo resource verbs do not include relate")
}

func TestIsResourceGroup(t *testing.T) {
	for _, g := range []string{"memo", "attachment", "user", "auth", "shortcut", "instance", "ai", "api"} {
		if !isResourceGroup(g) {
			t.Errorf("isResourceGroup(%q) = false, want true", g)
		}
	}
	for _, g := range []string{"search", "list", "get", "bogus"} {
		if isResourceGroup(g) {
			t.Errorf("isResourceGroup(%q) = true, want false", g)
		}
	}
}

func TestCoerceJSON(t *testing.T) {
	cases := []struct {
		in   string
		want any
	}{
		{"true", true},
		{"42", float64(42)},
		{`"quoted"`, "quoted"},
		{"plain string", "plain string"},
		{"null", nil},
	}
	for _, tc := range cases {
		got := coerceJSON(tc.in)
		if got != tc.want {
			t.Errorf("coerceJSON(%q) = %v (%T), want %v (%T)", tc.in, got, got, tc.want, tc.want)
		}
	}
}
