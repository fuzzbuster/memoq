package cli

// This file implements the lark-cli-style "resource-group + verb" command
// surface that gives memoq FULL coverage of the Memos v1 API. Where the
// top-level quick commands (search/list/get/...) serve the fast local cache,
// these resource commands talk to the server directly and print the raw JSON
// response, so they always reflect the exact server contract regardless of the
// Memos version in use.
//
// Design (mirrors lark-cli):
//   - `memoq <resource> <verb> [positional] [flags]` for the common operations.
//   - a generic `memoq api <METHOD> <PATH>` escape hatch that guarantees 100%
//     coverage of any endpoint, current or future.
//
// Every resource command is built on the single generic primitive
// Client.Do(ctx, method, path, body) -> json.RawMessage.

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
)

// resourceGroupSpec describes one lark-cli-style resource command group.
type resourceGroupSpec struct {
	Name  string
	Use   string
	Short string
	Verbs []string
}

// resourceGroupSpecs is the single source of truth for resource command help.
var resourceGroupSpecs = []resourceGroupSpec{
	{
		Name:  "memo",
		Use:   "memo <verb>",
		Short: "Direct memo API commands",
		Verbs: []string{
			"list", "get", "create", "update", "delete",
			"comments", "comment", "relations", "set-relations",
			"reactions", "react", "unreact",
			"attachments", "set-attachments",
			"shares", "share", "unshare", "link-metadata",
		},
	},
	{
		Name:  "attachment",
		Use:   "attachment <verb>",
		Short: "Direct attachment API commands",
		Verbs: []string{"list", "get", "create", "update", "delete", "batch-delete", "pull"},
	},
	{
		Name:  "user",
		Use:   "user <verb>",
		Short: "Direct user API commands",
		Verbs: []string{
			"list", "get", "create", "update", "delete",
			"all-stats", "stats", "settings", "setting", "update-setting",
			"tokens", "create-token", "delete-token", "webhooks",
		},
	},
	{
		Name:  "auth",
		Use:   "auth <verb>",
		Short: "Direct auth API commands",
		Verbs: []string{"me", "signin", "signout", "refresh"},
	},
	{
		Name:  "shortcut",
		Use:   "shortcut <verb>",
		Short: "Direct shortcut API commands",
		Verbs: []string{"list", "get", "create", "update", "delete"},
	},
	{
		Name:  "instance",
		Use:   "instance <verb>",
		Short: "Direct instance API commands",
		Verbs: []string{"profile", "setting", "update-setting", "stats"},
	},
	{
		Name:  "ai",
		Use:   "ai <verb>",
		Short: "Direct AI API commands",
		Verbs: []string{"transcribe"},
	},
	{
		Name:  "api",
		Use:   "api <METHOD> <PATH>",
		Short: "Generic API escape hatch",
		Verbs: []string{"<METHOD> <PATH>"},
	},
}

// isResourceGroup reports whether cmd names an API resource group handled here.
func isResourceGroup(cmd string) bool {
	for _, spec := range resourceGroupSpecs {
		if spec.Name == cmd {
			return true
		}
	}
	return false
}

func resourceVerbs(resource string) []string {
	for _, spec := range resourceGroupSpecs {
		if spec.Name == resource {
			return spec.Verbs
		}
	}
	return nil
}

// dispatchResource routes `memoq <resource> <verb> ...` to the right handler.
func dispatchResource(resource string, args []string) error {
	switch resource {
	case "api":
		return cmdAPI(args)
	case "memo":
		return cmdMemoResource(args)
	case "attachment":
		return cmdAttachmentResource(args)
	case "user":
		return cmdUserResource(args)
	case "auth":
		return cmdAuthResource(args)
	case "shortcut":
		return cmdShortcutResource(args)
	case "instance":
		return cmdInstanceResource(args)
	case "ai":
		return cmdAIResource(args)
	default:
		return fmt.Errorf("unknown resource %q", resource)
	}
}

// ---- shared plumbing -------------------------------------------------------

// apiFlags is the common flag set shared by resource verbs: it supports supplying
// a request body inline, from a file, or by repeated --field k=v pairs, plus
// repeated --query k=v pairs appended to the URL.
type apiFlags struct {
	body     string
	bodyFile string
	fields   kvFlag
	queries  kvFlag
}

func (f *apiFlags) register(fs *flag.FlagSet) {
	fs.StringVar(&f.body, "body", "", "raw JSON request body")
	fs.StringVar(&f.bodyFile, "body-file", "", "read JSON request body from a file ('-' for stdin)")
	fs.Var(&f.fields, "field", "body field as key=value (repeatable; value JSON-parsed, string fallback)")
	fs.Var(&f.queries, "query", "query param as key=value (repeatable)")
}

// buildBody assembles the request body from --body / --body-file / --field.
// Precedence: --body wins, else --body-file, else --field pairs, else nil.
func (f *apiFlags) buildBody() (any, error) {
	if f.body != "" {
		return json.RawMessage(f.body), nil
	}
	if f.bodyFile != "" {
		var data []byte
		var err error
		if f.bodyFile == "-" {
			data, err = readAllStdin()
		} else {
			data, err = os.ReadFile(f.bodyFile)
		}
		if err != nil {
			return nil, err
		}
		return json.RawMessage(data), nil
	}
	if len(f.fields) > 0 {
		obj := map[string]any{}
		for _, kv := range f.fields {
			k, v := splitKV(kv)
			obj[k] = coerceJSON(v)
		}
		return obj, nil
	}
	return nil, nil
}

// queryString renders the collected --query pairs as "?a=b&c=d" (or "").
func (f *apiFlags) queryString() string {
	if len(f.queries) == 0 {
		return ""
	}
	q := url.Values{}
	for _, kv := range f.queries {
		k, v := splitKV(kv)
		q.Add(k, v)
	}
	return "?" + q.Encode()
}

// kvFlag collects repeatable key=value flags.
type kvFlag []string

func (m *kvFlag) String() string { return strings.Join(*m, ",") }
func (m *kvFlag) Set(v string) error {
	*m = append(*m, v)
	return nil
}

func splitKV(s string) (string, string) {
	if i := strings.IndexByte(s, '='); i >= 0 {
		return s[:i], s[i+1:]
	}
	return s, ""
}

// coerceJSON tries to parse v as JSON (number/bool/object/array/null); on
// failure it is kept as a plain string. This lets --field pinned=true and
// --field content="hello world" both do the right thing.
func coerceJSON(v string) any {
	var out any
	if err := json.Unmarshal([]byte(v), &out); err == nil {
		return out
	}
	return v
}

func readAllStdin() ([]byte, error) {
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(os.Stdin); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// runAPI executes a request through the generic client primitive and prints the
// pretty-printed JSON response to stdout. This is the single choke point every
// resource verb funnels through. It is a package variable so tests can stub it
// to capture the constructed method/path/body without touching the network.
var runAPI = func(method, path string, body any) error {
	a, err := openApp()
	if err != nil {
		return err
	}
	defer a.close()
	cl, err := a.client()
	if err != nil {
		return err
	}
	raw, err := cl.Do(context.Background(), method, path, body)
	if err != nil {
		return err
	}
	return printRawJSON(raw)
}

// printRawJSON pretty-prints a json.RawMessage; if it is not valid JSON (should
// not happen), it is written verbatim.
func printRawJSON(raw json.RawMessage) error {
	var buf bytes.Buffer
	if err := json.Indent(&buf, raw, "", "  "); err != nil {
		fmt.Println(string(raw))
		return nil
	}
	fmt.Println(buf.String())
	return nil
}

// resourceUsage builds a "usage: memoq <res> <verbs...>" error.
func resourceUsage(resource string, verbs ...string) error {
	if len(verbs) == 0 {
		verbs = resourceVerbs(resource)
	}
	return fmt.Errorf("usage: memoq %s <%s> [args] [flags]", resource, strings.Join(verbs, "|"))
}

// ---- generic escape hatch --------------------------------------------------

// cmdAPI implements `memoq api <METHOD> <PATH> [--body|--body-file|--field] [--query]`.
// This alone guarantees full API coverage: any endpoint, any verb.
func cmdAPI(args []string) error {
	fs := flag.NewFlagSet("api", flag.ContinueOnError)
	var af apiFlags
	af.register(fs)
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	if len(pos) < 2 {
		return fmt.Errorf("usage: memoq api <METHOD> <PATH> [--body '{...}'] [--field k=v] [--query k=v]\n" +
			"  e.g. memoq api GET /api/v1/memos --query pageSize=10\n" +
			"       memoq api POST /api/v1/memos --field content='hi' --field visibility=PRIVATE")
	}
	method := strings.ToUpper(pos[0])
	path := pos[1]
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	body, err := af.buildBody()
	if err != nil {
		return err
	}
	return runAPI(method, path+af.queryString(), body)
}

// ---- memo resource ---------------------------------------------------------

func cmdMemoResource(args []string) error {
	if len(args) == 0 {
		return resourceUsage("memo")
	}
	verb, rest := args[0], args[1:]
	switch verb {
	case "list":
		fs := flag.NewFlagSet("memo list", flag.ContinueOnError)
		var af apiFlags
		af.register(fs)
		if _, err := parseArgs(fs, rest); err != nil {
			return err
		}
		return runAPI(http.MethodGet, "/api/v1/memos"+af.queryString(), nil)
	case "get":
		uid, af, err := oneArgWithFlags("memo get", rest)
		if err != nil {
			return err
		}
		return runAPI(http.MethodGet, "/api/v1/memos/"+url.PathEscape(uid)+af.queryString(), nil)
	case "create":
		return bodyVerb("memo create", http.MethodPost, "/api/v1/memos", "", rest)
	case "update":
		return uidBodyVerb("memo update", http.MethodPatch, "/api/v1/memos/", "", rest)
	case "delete":
		uid, af, err := oneArgWithFlags("memo delete", rest)
		if err != nil {
			return err
		}
		return runAPI(http.MethodDelete, "/api/v1/memos/"+url.PathEscape(uid)+af.queryString(), nil)
	case "comments":
		uid, af, err := oneArgWithFlags("memo comments", rest)
		if err != nil {
			return err
		}
		return runAPI(http.MethodGet, "/api/v1/memos/"+url.PathEscape(uid)+"/comments"+af.queryString(), nil)
	case "comment":
		return uidBodyVerb("memo comment", http.MethodPost, "/api/v1/memos/", "/comments", rest)
	case "relations":
		uid, af, err := oneArgWithFlags("memo relations", rest)
		if err != nil {
			return err
		}
		return runAPI(http.MethodGet, "/api/v1/memos/"+url.PathEscape(uid)+"/relations"+af.queryString(), nil)
	case "set-relations":
		return uidBodyVerb("memo set-relations", http.MethodPatch, "/api/v1/memos/", "/relations", rest)
	case "reactions":
		uid, af, err := oneArgWithFlags("memo reactions", rest)
		if err != nil {
			return err
		}
		return runAPI(http.MethodGet, "/api/v1/memos/"+url.PathEscape(uid)+"/reactions"+af.queryString(), nil)
	case "react":
		return uidBodyVerb("memo react", http.MethodPost, "/api/v1/memos/", "/reactions", rest)
	case "unreact":
		// DeleteMemoReaction: DELETE /api/v1/{name=memos/*/reactions/*}
		uid, rid, af, err := twoArgsWithFlags("memo unreact", rest)
		if err != nil {
			return err
		}
		return runAPI(http.MethodDelete,
			"/api/v1/memos/"+url.PathEscape(uid)+"/reactions/"+url.PathEscape(rid)+af.queryString(), nil)
	case "attachments":
		uid, af, err := oneArgWithFlags("memo attachments", rest)
		if err != nil {
			return err
		}
		return runAPI(http.MethodGet, "/api/v1/memos/"+url.PathEscape(uid)+"/attachments"+af.queryString(), nil)
	case "set-attachments":
		return uidBodyVerb("memo set-attachments", http.MethodPatch, "/api/v1/memos/", "/attachments", rest)
	case "shares":
		// ListMemoShares: GET /api/v1/{parent=memos/*}/shares
		uid, af, err := oneArgWithFlags("memo shares", rest)
		if err != nil {
			return err
		}
		return runAPI(http.MethodGet, "/api/v1/memos/"+url.PathEscape(uid)+"/shares"+af.queryString(), nil)
	case "share":
		// CreateMemoShare: POST /api/v1/{parent=memos/*}/shares
		return uidBodyVerb("memo share", http.MethodPost, "/api/v1/memos/", "/shares", rest)
	case "unshare":
		// DeleteMemoShare: DELETE /api/v1/{name=memos/*/shares/*}
		uid, sid, af, err := twoArgsWithFlags("memo unshare", rest)
		if err != nil {
			return err
		}
		return runAPI(http.MethodDelete,
			"/api/v1/memos/"+url.PathEscape(uid)+"/shares/"+url.PathEscape(sid)+af.queryString(), nil)
	case "link-metadata":
		// GetLinkMetadata: GET /api/v1/memos/-/linkMetadata?link=...
		fs := flag.NewFlagSet("memo link-metadata", flag.ContinueOnError)
		var af apiFlags
		af.register(fs)
		if _, err := parseArgs(fs, rest); err != nil {
			return err
		}
		return runAPI(http.MethodGet, "/api/v1/memos/-/linkMetadata"+af.queryString(), nil)
	default:
		return fmt.Errorf("unknown memo verb %q", verb)
	}
}

// ---- attachment resource ---------------------------------------------------

func cmdAttachmentResource(args []string) error {
	if len(args) == 0 {
		return resourceUsage("attachment")
	}
	verb, rest := args[0], args[1:]
	switch verb {
	case "pull":
		return cmdAttachmentPull(rest)
	case "list":
		fs := flag.NewFlagSet("attachment list", flag.ContinueOnError)
		var af apiFlags
		af.register(fs)
		if _, err := parseArgs(fs, rest); err != nil {
			return err
		}
		return runAPI(http.MethodGet, "/api/v1/attachments"+af.queryString(), nil)
	case "get":
		id, af, err := oneArgWithFlags("attachment get", rest)
		if err != nil {
			return err
		}
		return runAPI(http.MethodGet, "/api/v1/attachments/"+url.PathEscape(id)+af.queryString(), nil)
	case "create":
		return bodyVerb("attachment create", http.MethodPost, "/api/v1/attachments", "", rest)
	case "update":
		return uidBodyVerb("attachment update", http.MethodPatch, "/api/v1/attachments/", "", rest)
	case "delete":
		id, af, err := oneArgWithFlags("attachment delete", rest)
		if err != nil {
			return err
		}
		return runAPI(http.MethodDelete, "/api/v1/attachments/"+url.PathEscape(id)+af.queryString(), nil)
	case "batch-delete":
		// BatchDeleteAttachments: POST /api/v1/attachments:batchDelete
		return bodyVerb("attachment batch-delete", http.MethodPost, "/api/v1/attachments:batchDelete", "", rest)
	default:
		return fmt.Errorf("unknown attachment verb %q", verb)
	}
}

// ---- user resource ---------------------------------------------------------

func cmdUserResource(args []string) error {
	if len(args) == 0 {
		return resourceUsage("user")
	}
	verb, rest := args[0], args[1:]
	switch verb {
	case "list":
		fs := flag.NewFlagSet("user list", flag.ContinueOnError)
		var af apiFlags
		af.register(fs)
		if _, err := parseArgs(fs, rest); err != nil {
			return err
		}
		return runAPI(http.MethodGet, "/api/v1/users"+af.queryString(), nil)
	case "get":
		id, af, err := oneArgWithFlags("user get", rest)
		if err != nil {
			return err
		}
		return runAPI(http.MethodGet, "/api/v1/users/"+url.PathEscape(id)+af.queryString(), nil)
	case "create":
		return bodyVerb("user create", http.MethodPost, "/api/v1/users", "", rest)
	case "update":
		return uidBodyVerb("user update", http.MethodPatch, "/api/v1/users/", "", rest)
	case "delete":
		id, af, err := oneArgWithFlags("user delete", rest)
		if err != nil {
			return err
		}
		return runAPI(http.MethodDelete, "/api/v1/users/"+url.PathEscape(id)+af.queryString(), nil)
	case "all-stats":
		// ListAllUserStats: GET /api/v1/users:stats
		fs := flag.NewFlagSet("user all-stats", flag.ContinueOnError)
		var af apiFlags
		af.register(fs)
		if _, err := parseArgs(fs, rest); err != nil {
			return err
		}
		return runAPI(http.MethodGet, "/api/v1/users:stats"+af.queryString(), nil)
	case "stats":
		// GetUserStats: GET /api/v1/{name=users/*}:getStats
		id, af, err := oneArgWithFlags("user stats", rest)
		if err != nil {
			return err
		}
		return runAPI(http.MethodGet, "/api/v1/users/"+url.PathEscape(id)+":getStats"+af.queryString(), nil)
	case "settings":
		// ListUserSettings: GET /api/v1/{parent=users/*}/settings
		id, af, err := oneArgWithFlags("user settings", rest)
		if err != nil {
			return err
		}
		return runAPI(http.MethodGet, "/api/v1/users/"+url.PathEscape(id)+"/settings"+af.queryString(), nil)
	case "setting":
		// GetUserSetting: GET /api/v1/{name=users/*/settings/*}
		id, key, af, err := twoArgsWithFlags("user setting", rest)
		if err != nil {
			return err
		}
		return runAPI(http.MethodGet,
			"/api/v1/users/"+url.PathEscape(id)+"/settings/"+url.PathEscape(key)+af.queryString(), nil)
	case "update-setting":
		// UpdateUserSetting: PATCH /api/v1/{setting.name=users/*/settings/*}
		id, key, af, body, err := twoArgsBody("user update-setting", rest)
		if err != nil {
			return err
		}
		return runAPI(http.MethodPatch,
			"/api/v1/users/"+url.PathEscape(id)+"/settings/"+url.PathEscape(key)+af.queryString(), body)
	case "tokens":
		// ListPersonalAccessTokens: GET /api/v1/{parent=users/*}/personalAccessTokens
		id, af, err := oneArgWithFlags("user tokens", rest)
		if err != nil {
			return err
		}
		return runAPI(http.MethodGet, "/api/v1/users/"+url.PathEscape(id)+"/personalAccessTokens"+af.queryString(), nil)
	case "create-token":
		// CreatePersonalAccessToken: POST /api/v1/{parent=users/*}/personalAccessTokens
		return uidBodyVerb("user create-token", http.MethodPost, "/api/v1/users/", "/personalAccessTokens", rest)
	case "delete-token":
		// DeletePersonalAccessToken: DELETE /api/v1/{name=users/*/personalAccessTokens/*}
		user, tid, af, err := twoArgsWithFlags("user delete-token", rest)
		if err != nil {
			return err
		}
		return runAPI(http.MethodDelete,
			"/api/v1/users/"+url.PathEscape(user)+"/personalAccessTokens/"+url.PathEscape(tid)+af.queryString(), nil)
	case "webhooks":
		// ListUserWebhooks: GET /api/v1/{parent=users/*}/webhooks
		id, af, err := oneArgWithFlags("user webhooks", rest)
		if err != nil {
			return err
		}
		return runAPI(http.MethodGet, "/api/v1/users/"+url.PathEscape(id)+"/webhooks"+af.queryString(), nil)
	default:
		return fmt.Errorf("unknown user verb %q", verb)
	}
}

// ---- auth resource ---------------------------------------------------------

func cmdAuthResource(args []string) error {
	if len(args) == 0 {
		return resourceUsage("auth")
	}
	verb, rest := args[0], args[1:]
	switch verb {
	case "me", "status":
		// GetCurrentSession: GET /api/v1/auth/me (was /auth/status on older servers).
		fs := flag.NewFlagSet("auth me", flag.ContinueOnError)
		var af apiFlags
		af.register(fs)
		if _, err := parseArgs(fs, rest); err != nil {
			return err
		}
		return runAPI(http.MethodGet, "/api/v1/auth/me"+af.queryString(), nil)
	case "signin":
		// CreateSession: POST /api/v1/auth/signin
		return bodyVerb("auth signin", http.MethodPost, "/api/v1/auth/signin", "", rest)
	case "signout":
		// DeleteSession: POST /api/v1/auth/signout
		return runAPI(http.MethodPost, "/api/v1/auth/signout", nil)
	case "refresh":
		// RefreshSession: POST /api/v1/auth/refresh
		return bodyVerb("auth refresh", http.MethodPost, "/api/v1/auth/refresh", "", rest)
	default:
		return fmt.Errorf("unknown auth verb %q", verb)
	}
}

// ---- shortcut resource -----------------------------------------------------

func cmdShortcutResource(args []string) error {
	if len(args) == 0 {
		return resourceUsage("shortcut")
	}
	verb, rest := args[0], args[1:]
	// Shortcuts are scoped under a user: /api/v1/users/{user}/shortcuts.
	// The first positional after the verb is the user identifier.
	switch verb {
	case "list":
		user, af, err := oneArgWithFlags("shortcut list", rest)
		if err != nil {
			return err
		}
		return runAPI(http.MethodGet, "/api/v1/users/"+url.PathEscape(user)+"/shortcuts"+af.queryString(), nil)
	case "get":
		user, sid, af, err := twoArgsWithFlags("shortcut get", rest)
		if err != nil {
			return err
		}
		return runAPI(http.MethodGet, "/api/v1/users/"+url.PathEscape(user)+"/shortcuts/"+url.PathEscape(sid)+af.queryString(), nil)
	case "create":
		return uidBodyVerb("shortcut create", http.MethodPost, "/api/v1/users/", "/shortcuts", rest)
	case "update":
		user, sid, af, body, err := twoArgsBody("shortcut update", rest)
		if err != nil {
			return err
		}
		return runAPI(http.MethodPatch, "/api/v1/users/"+url.PathEscape(user)+"/shortcuts/"+url.PathEscape(sid)+af.queryString(), body)
	case "delete":
		user, sid, af, err := twoArgsWithFlags("shortcut delete", rest)
		if err != nil {
			return err
		}
		return runAPI(http.MethodDelete, "/api/v1/users/"+url.PathEscape(user)+"/shortcuts/"+url.PathEscape(sid)+af.queryString(), nil)
	default:
		return fmt.Errorf("unknown shortcut verb %q", verb)
	}
}

// ---- instance resource -----------------------------------------------------

func cmdInstanceResource(args []string) error {
	if len(args) == 0 {
		return resourceUsage("instance")
	}
	verb, rest := args[0], args[1:]
	switch verb {
	case "profile":
		// GetInstanceProfile: GET /api/v1/instance/profile (was /workspace/profile).
		fs := flag.NewFlagSet("instance profile", flag.ContinueOnError)
		var af apiFlags
		af.register(fs)
		if _, err := parseArgs(fs, rest); err != nil {
			return err
		}
		return runAPI(http.MethodGet, "/api/v1/instance/profile"+af.queryString(), nil)
	case "setting":
		// GetInstanceSetting: GET /api/v1/instance/settings/{key}. With no key,
		// fall back to the collection endpoint /api/v1/instance/settings.
		fs := flag.NewFlagSet("instance setting", flag.ContinueOnError)
		var af apiFlags
		af.register(fs)
		pos, err := parseArgs(fs, rest)
		if err != nil {
			return err
		}
		path := "/api/v1/instance/settings"
		if len(pos) == 1 {
			path = "/api/v1/instance/settings/" + url.PathEscape(pos[0])
		}
		return runAPI(http.MethodGet, path+af.queryString(), nil)
	case "update-setting":
		// UpdateInstanceSetting: PATCH /api/v1/instance/settings/{key}.
		return uidBodyVerb("instance update-setting", http.MethodPatch, "/api/v1/instance/settings/", "", rest)
	case "stats":
		// GetInstanceStats: GET /api/v1/instance/stats.
		fs := flag.NewFlagSet("instance stats", flag.ContinueOnError)
		var af apiFlags
		af.register(fs)
		if _, err := parseArgs(fs, rest); err != nil {
			return err
		}
		return runAPI(http.MethodGet, "/api/v1/instance/stats"+af.queryString(), nil)
	default:
		return fmt.Errorf("unknown instance verb %q", verb)
	}
}

// ---- ai resource -----------------------------------------------------------

func cmdAIResource(args []string) error {
	if len(args) == 0 {
		return resourceUsage("ai")
	}
	verb, rest := args[0], args[1:]
	switch verb {
	case "transcribe":
		// TranscribeAttachment: POST /api/v1/ai:transcribe (custom verb).
		return bodyVerb("ai transcribe", http.MethodPost, "/api/v1/ai:transcribe", "", rest)
	default:
		return fmt.Errorf("unknown ai verb %q", verb)
	}
}

// ---- verb helpers ----------------------------------------------------------

// oneArgWithFlags parses exactly one positional plus the shared apiFlags.
func oneArgWithFlags(name string, args []string) (string, *apiFlags, error) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	af := &apiFlags{}
	af.register(fs)
	pos, err := parseArgs(fs, args)
	if err != nil {
		return "", nil, err
	}
	if len(pos) != 1 {
		return "", nil, fmt.Errorf("usage: memoq %s <id> [flags]", name)
	}
	return pos[0], af, nil
}

// twoArgsWithFlags parses exactly two positionals plus the shared apiFlags.
func twoArgsWithFlags(name string, args []string) (string, string, *apiFlags, error) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	af := &apiFlags{}
	af.register(fs)
	pos, err := parseArgs(fs, args)
	if err != nil {
		return "", "", nil, err
	}
	if len(pos) != 2 {
		return "", "", nil, fmt.Errorf("usage: memoq %s <arg1> <arg2> [flags]", name)
	}
	return pos[0], pos[1], af, nil
}

// twoArgsBody parses two positionals plus a body (from --body/--field/...).
func twoArgsBody(name string, args []string) (string, string, *apiFlags, any, error) {
	a1, a2, af, err := twoArgsWithFlags(name, args)
	if err != nil {
		return "", "", nil, nil, err
	}
	body, err := af.buildBody()
	if err != nil {
		return "", "", nil, nil, err
	}
	return a1, a2, af, body, nil
}

// bodyVerb runs a bodied request against a fixed path (create-style). It reads
// the body from --body/--body-file/--field.
func bodyVerb(name, method, path, suffix string, args []string) error {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	af := &apiFlags{}
	af.register(fs)
	if _, err := parseArgs(fs, args); err != nil {
		return err
	}
	body, err := af.buildBody()
	if err != nil {
		return err
	}
	return runAPI(method, path+suffix+af.queryString(), body)
}

// uidBodyVerb runs a bodied request against prefix+<uid>+suffix (update-style),
// taking exactly one positional id and a body.
func uidBodyVerb(name, method, prefix, suffix string, args []string) error {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	af := &apiFlags{}
	af.register(fs)
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return fmt.Errorf("usage: memoq %s <id> [--field k=v | --body '{...}']", name)
	}
	body, err := af.buildBody()
	if err != nil {
		return err
	}
	return runAPI(method, prefix+url.PathEscape(pos[0])+suffix+af.queryString(), body)
}
