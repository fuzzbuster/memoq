package cli

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type captured struct {
	method    string
	path      string
	body      any
	syncCache bool
}

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

// Independent HTTP binding snapshot from all eight upstream service protos.
// Both 26.10.0 and 26.11.1 have these 93 bindings; x/y replace path wildcards.
const upstream26Endpoints = `
DELETE /api/attachments/x
DELETE /api/identity-providers/x
DELETE /api/memos/x
DELETE /api/memos/x/reactions/y
DELETE /api/memos/x/shares/y
DELETE /api/spaces/x
DELETE /api/spaces/x/invitations/y
DELETE /api/spaces/x/members/y
DELETE /api/users/x
DELETE /api/users/x/linkedIdentities/y
DELETE /api/users/x/notifications/y
DELETE /api/users/x/personalAccessTokens/y
DELETE /api/users/x/views/y
DELETE /api/users/x/webhooks/y
GET /api/attachments
GET /api/attachments/x
GET /api/auth/me
GET /api/identity-providers
GET /api/identity-providers/x
GET /api/instance/configuration
GET /api/instance/profile
GET /api/instance/settings/x
GET /api/instance/stats
GET /api/memos
GET /api/memos/-/linkMetadata
GET /api/memos/x
GET /api/memos/x/attachments
GET /api/memos/x/comments
GET /api/memos/x/reactions
GET /api/memos/x/relations
GET /api/memos/x/shares
GET /api/shares/x/memo
GET /api/spaces
GET /api/spaces/x
GET /api/spaces/x/invitations
GET /api/spaces/x/invitations/y
GET /api/spaces/x/members
GET /api/spaces/x/members/y
GET /api/users
GET /api/users/x
GET /api/users/x/linkedIdentities
GET /api/users/x/linkedIdentities/y
GET /api/users/x/notifications
GET /api/users/x/personalAccessTokens
GET /api/users/x/settings
GET /api/users/x/settings/y
GET /api/users/x/spaceInvitations
GET /api/users/x/stats
GET /api/users/x/usage
GET /api/users/x/views
GET /api/users/x/views/y
GET /api/users/x/webhooks
GET /api/users/x/webhooks/y:getSigningSecret
GET /api/users/x:getStats
PATCH /api/attachments/x
PATCH /api/identity-providers/x
PATCH /api/instance/settings/x
PATCH /api/memos/x
PATCH /api/memos/x/attachments
PATCH /api/memos/x/relations
PATCH /api/spaces/x
PATCH /api/spaces/x/members/y
PATCH /api/users/x
PATCH /api/users/x/notifications/y
PATCH /api/users/x/settings/y
PATCH /api/users/x/views/y
PATCH /api/users/x/webhooks/y
POST /api/ai:transcribe
POST /api/attachments
POST /api/attachments:batchDelete
POST /api/attachments:upload
POST /api/auth/refresh
POST /api/auth/signin
POST /api/auth/signout
POST /api/identity-providers
POST /api/instance/settings/notification:testEmail
POST /api/instance/settings:batchGet
POST /api/memos
POST /api/memos/-/linkMetadata:batchGet
POST /api/memos/x/comments
POST /api/memos/x/reactions
POST /api/memos/x/shares
POST /api/spaces
POST /api/spaces/x/invitations
POST /api/spaces/x/invitations/y:accept
POST /api/spaces/x/invitations/y:decline
POST /api/users
POST /api/users/x/linkedIdentities
POST /api/users/x/personalAccessTokens
POST /api/users/x/views
POST /api/users/x/webhooks
POST /api/users/x:importMemos
POST /api/users:batchGet
`

func TestResourcePaths(t *testing.T) {
	want := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(upstream26Endpoints), "\n") {
		want[line] = true
	}
	if len(want) != 93 {
		t.Fatalf("upstream snapshot has %d bindings, want 93", len(want))
	}
	for resource, specs := range resourceVerbSpecs {
		for _, spec := range specs {
			if spec.Handler != nil || spec.Names[0] == "export-memos" || spec.Names[0] == "all-stats" {
				continue
			}
			pos := []string{}
			if spec.Args == argsOne || spec.Args == argsTwo {
				pos = append(pos, "x")
			}
			if spec.Args == argsTwo {
				pos = append(pos, "y")
			}
			key := spec.Method + " " + spec.Path(pos)
			if !want[key] {
				t.Errorf("unexpected binding %s (%s %s)", key, resource, spec.Names[0])
			}
			delete(want, key)
			t.Run(resource+" "+spec.Names[0], func(t *testing.T) {
				c := stubRunAPI(t)
				args := append([]string{resource, spec.Names[0]}, pos...)
				args = append(args, "--yes")
				if err := Run(args); err != nil {
					t.Fatal(err)
				}
				if got := c.method + " " + c.path; got != key {
					t.Fatalf("request = %s, want %s", got, key)
				}
			})
		}
	}
	for key := range want {
		t.Errorf("missing upstream binding: %s", key)
	}
}

func TestResourceQueriesAndAliases(t *testing.T) {
	for _, tc := range []struct {
		args []string
		path string
	}{
		{[]string{"memo", "list", "--query", "pageSize=10"}, "/api/memos?pageSize=10"},
		{[]string{"memo", "link-metadata", "--query", "url=http://x"}, "/api/memos/-/linkMetadata?url=http%3A%2F%2Fx"},
		{[]string{"auth", "status"}, "/api/auth/me"},
		{[]string{"user", "all-stats"}, "/api/users/-/stats"},
		{[]string{"api", "get", "api/memos", "--query", "pageSize=5"}, "/api/memos?pageSize=5"},
	} {
		c := stubRunAPI(t)
		if err := Run(tc.args); err != nil {
			t.Fatal(err)
		}
		if c.method != "GET" || c.path != tc.path {
			t.Fatalf("request = %s %s, want GET %s", c.method, c.path, tc.path)
		}
	}
}

func TestBodyBuilding_FieldCoercion(t *testing.T) {
	c := stubRunAPI(t)
	if err := Run([]string{"memo", "create", "--field", "content=hello world", "--field", "pinned=true", "--field", "priority=3"}); err != nil {
		t.Fatal(err)
	}
	obj := c.body.(map[string]any)
	if obj["content"] != "hello world" || obj["pinned"] != true || obj["priority"] != float64(3) {
		t.Fatalf("body = %#v", obj)
	}
}

func TestBodyBuilding_RawBodyWinsOverFields(t *testing.T) {
	c := stubRunAPI(t)
	if err := Run([]string{"memo", "create", "--body", `{"content":"raw"}`, "--field", "content=ignored"}); err != nil {
		t.Fatal(err)
	}
	if string(c.body.(json.RawMessage)) != `{"content":"raw"}` {
		t.Fatalf("body = %s", c.body)
	}
}

func TestAttachmentFileValidation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sample.bin")
	if err := os.WriteFile(path, []byte("content"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"attachment", "create", "--file", path, "--body", `{}`},
		{"attachment", "upload", "--file", path, "--memo", "m", "--replace", "a", "--yes"},
		{"attachment", "create", "--file", path + ".missing"},
		{"attachment", "upload", "--replace", "a", "--yes"},
		{"user", "import-memos", "x", "--file", path, "--conflict-policy", "BAD", "--yes"},
	} {
		c := stubRunAPI(t)
		if err := Run(args); err == nil {
			t.Fatalf("expected validation error for %v", args)
		}
		if c.method != "" {
			t.Fatal("invalid input called API")
		}
	}
}

func TestMemoSetAttachmentsBuildsEnvelope(t *testing.T) {
	c := stubRunAPI(t)
	if err := Run([]string{"memo", "set-attachments", "memo1", "--attachment", "attachments/a", "--attachment", "attachments/b"}); err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(c.body)
	if c.path != "/api/memos/memo1/attachments" || string(encoded) != `{"attachments":[{"name":"attachments/a"},{"name":"attachments/b"}]}` {
		t.Fatalf("request = %s %s", c.path, encoded)
	}
	for _, args := range [][]string{
		{"memo", "set-attachments", "memo1", "--attachment", "a"},
		{"memo", "set-attachments", "memo1", "--attachment", "attachments/a", "--field", "attachments=[]"},
	} {
		if err := Run(args); err == nil {
			t.Fatalf("expected error for %v", args)
		}
	}
}

func TestResourceConfirmationAndDryRun(t *testing.T) {
	for _, args := range [][]string{
		{"memo", "delete", "abc"},
		{"api", "DELETE", "/api/memos/abc"},
		{"space", "remove-member", "x", "y"},
		{"idp", "create"},
		{"instance", "test-email"},
	} {
		c := stubRunAPI(t)
		err := Run(args)
		var ce *cliError
		if !errors.As(err, &ce) || ce.Code != "confirmation_required" || c.method != "" {
			t.Fatalf("confirmation policy for %v: %v", args, err)
		}
		if err := Run(append(args, "--dry-run")); err != nil || c.method != "" {
			t.Fatalf("dry run for %v: %v", args, err)
		}
	}
}

func TestResourceCacheSyncPolicy(t *testing.T) {
	c := stubRunAPI(t)
	if err := Run([]string{"memo", "update", "abc", "--field", "content=x"}); err != nil || !c.syncCache {
		t.Fatalf("memo update sync: %v", err)
	}
	c = stubRunAPI(t)
	if err := Run([]string{"api", "PATCH", "/api/memos/abc", "--field", "content=x", "--sync"}); err != nil || !c.syncCache {
		t.Fatalf("api sync: %v", err)
	}
	c = stubRunAPI(t)
	if err := Run([]string{"api", "GET", "/api/memos/abc/shares"}); err != nil || c.syncCache {
		t.Fatalf("read shares: %v", err)
	}
}

func TestUnknownVerbAndResource(t *testing.T) {
	for _, args := range [][]string{{"memo", "bogus"}, {"nonsense"}, {"memo"}, {"memo", "relate", "source"}} {
		if err := Run(args); err == nil {
			t.Fatalf("expected usage error for %v", args)
		}
	}
	for _, g := range []string{"memo", "attachment", "user", "auth", "view", "space", "idp", "instance", "ai", "api"} {
		if !isResourceGroup(g) {
			t.Errorf("missing resource group %s", g)
		}
	}
	if isResourceGroup("shortcut") {
		t.Fatal("obsolete shortcut group still registered")
	}
}

func TestCoerceJSON(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want any
	}{{"true", true}, {"42", float64(42)}, {`"quoted"`, "quoted"}, {"plain string", "plain string"}, {"null", nil}} {
		if got := coerceJSON(tc.in); got != tc.want {
			t.Errorf("coerceJSON(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}
