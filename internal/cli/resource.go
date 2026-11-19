package cli

// This file implements the lark-cli-style "resource-group + verb" command
// surface for the Memos 26.xx API. Where the
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
	"net/url"
	"os"
	"strings"

	"github.com/example/memoq/internal/syncer"
)

const (
	methodGet    = "GET"
	methodPost   = "POST"
	methodPatch  = "PATCH"
	methodDelete = "DELETE"
)

// resourceGroupSpec describes one lark-cli-style resource command group.
type resourceGroupSpec struct {
	Name  string
	Use   string
	Short string
}

// resourceGroupSpecs lists the lark-cli-style top-level resource groups.
var resourceGroupSpecs = []resourceGroupSpec{
	{
		Name:  "memo",
		Use:   "memo <verb>",
		Short: "Direct memo API commands",
	},
	{
		Name:  "attachment",
		Use:   "attachment <verb>",
		Short: "Direct attachment API commands",
	},
	{
		Name:  "user",
		Use:   "user <verb>",
		Short: "Direct user API commands",
	},
	{
		Name:  "auth",
		Use:   "auth <verb>",
		Short: "Direct auth API commands",
	},
	{
		Name:  "view",
		Use:   "view <verb>",
		Short: "Direct memo view API commands",
	},
	{
		Name:  "space",
		Use:   "space <verb>",
		Short: "Direct space, member, and invitation API commands",
	},
	{
		Name:  "idp",
		Use:   "idp <verb>",
		Short: "Direct identity provider API commands",
	},
	{
		Name:  "instance",
		Use:   "instance <verb>",
		Short: "Direct instance API commands",
	},
	{
		Name:  "ai",
		Use:   "ai <verb>",
		Short: "Direct AI API commands",
	},
	{
		Name:  "api",
		Use:   "api <METHOD> <PATH>",
		Short: "Generic API escape hatch",
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
	if resource == "api" {
		return []string{"<METHOD> <PATH>"}
	}
	verbs := resourceVerbSpecs[resource]
	out := make([]string, 0, len(verbs))
	for _, spec := range verbs {
		out = append(out, spec.Names[0])
	}
	return out
}

// dispatchResource routes `memoq <resource> <verb> ...` to the right handler.
func dispatchResource(resource string, args []string) error {
	switch resource {
	case "api":
		return cmdAPI(args)
	default:
		if isResourceGroup(resource) {
			return runResourceVerb(resource, args)
		}
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
	dryRun   bool
	yes      bool
	sync     bool
	asJSON   bool
	output   string
	stream   bool
}

func (f *apiFlags) register(fs *flag.FlagSet) {
	fs.StringVar(&f.body, "body", "", "raw JSON request body")
	fs.StringVar(&f.bodyFile, "body-file", "", "read JSON request body from a file ('-' for stdin)")
	fs.Var(&f.fields, "field", "body field as key=value (repeatable; value JSON-parsed, string fallback)")
	fs.Var(&f.queries, "query", "query param as key=value (repeatable)")
	fs.BoolVar(&f.dryRun, "dry-run", false, "preview without sending the request")
	fs.BoolVar(&f.yes, "yes", false, "confirm destructive operation")
	fs.BoolVar(&f.sync, "sync", false, "sync the local memo cache after success")
	fs.BoolVar(&f.asJSON, "json", false, "emit JSON (resource commands always do)")
	fs.StringVar(&f.output, "output", "", "write a GET response to a file")
	fs.BoolVar(&f.stream, "stream", false, "stream a GET response to stdout")
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

func (f *apiFlags) hasBodyInput() bool {
	return f.body != "" || f.bodyFile != "" || len(f.fields) > 0
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
var runAPI = func(method, path string, body any, syncCache bool) error {
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
	if syncCache {
		sy := syncer.New(cl, a.store)
		if _, err := sy.Sync(context.Background()); err != nil {
			warnCache(err)
		}
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

type resourceArgMode int

const (
	argsAny resourceArgMode = iota
	argsOne
	argsTwo
	argsOptionalOne
)

type resourceVerbSpec struct {
	Names         []string
	Method        string
	Args          resourceArgMode
	Body          bool
	Confirm       bool
	SyncMemoCache bool
	Path          func([]string) string
	Handler       func([]string) error
}

var resourceVerbSpecs = map[string][]resourceVerbSpec{
	"memo": {
		verb("list", methodGet, argsAny, false, fixedPath("/api/memos")),
		verb("get", methodGet, argsOne, false, onePath("/api/memos/", "")),
		cacheVerb("create", methodPost, argsAny, true, false, fixedPath("/api/memos")),
		cacheVerb("update", methodPatch, argsOne, true, false, onePath("/api/memos/", "")),
		cacheVerb("delete", methodDelete, argsOne, false, true, onePath("/api/memos/", "")),
		verb("comments", methodGet, argsOne, false, onePath("/api/memos/", "/comments")),
		verb("comment", methodPost, argsOne, true, onePath("/api/memos/", "/comments")),
		verb("relations", methodGet, argsOne, false, onePath("/api/memos/", "/relations")),
		handlerVerb("relate", cmdMemoRelate),
		verb("set-relations", methodPatch, argsOne, true, onePath("/api/memos/", "/relations")),
		verb("reactions", methodGet, argsOne, false, onePath("/api/memos/", "/reactions")),
		verb("react", methodPost, argsOne, true, onePath("/api/memos/", "/reactions")),
		dangerousVerb("unreact", methodDelete, argsTwo, false, twoPath("/api/memos/", "/reactions/", "")),
		verb("attachments", methodGet, argsOne, false, onePath("/api/memos/", "/attachments")),
		verb("set-attachments", methodPatch, argsOne, true, onePath("/api/memos/", "/attachments")),
		verb("shares", methodGet, argsOne, false, onePath("/api/memos/", "/shares")),
		dangerousVerb("share", methodPost, argsOne, true, onePath("/api/memos/", "/shares")),
		dangerousVerb("unshare", methodDelete, argsTwo, false, twoPath("/api/memos/", "/shares/", "")),
		verb("shared", methodGet, argsOne, false, onePath("/api/shares/", "/memo")),
		verb("link-metadata", methodGet, argsAny, false, fixedPath("/api/memos/-/linkMetadata")),
		verb("batch-link-metadata", methodPost, argsAny, true, fixedPath("/api/memos/-/linkMetadata:batchGet")),
	},
	"attachment": {
		handlerVerb("pull", cmdAttachmentPull),
		verb("list", methodGet, argsAny, false, fixedPath("/api/attachments")),
		verb("get", methodGet, argsOne, false, onePath("/api/attachments/", "")),
		verb("create", methodPost, argsAny, true, fixedPath("/api/attachments")),
		verb("upload", methodPost, argsAny, true, fixedPath("/api/attachments:upload")),
		verb("update", methodPatch, argsOne, true, onePath("/api/attachments/", "")),
		dangerousVerb("delete", methodDelete, argsOne, false, onePath("/api/attachments/", "")),
		dangerousVerb("batch-delete", methodPost, argsAny, true, fixedPath("/api/attachments:batchDelete")),
	},
	"user": {
		verb("list", methodGet, argsAny, false, fixedPath("/api/users")),
		verb("batch-get", methodPost, argsAny, true, fixedPath("/api/users:batchGet")),
		verb("get", methodGet, argsOne, false, onePath("/api/users/", "")),
		verb("create", methodPost, argsAny, true, fixedPath("/api/users")),
		verb("update", methodPatch, argsOne, true, onePath("/api/users/", "")),
		dangerousVerb("delete", methodDelete, argsOne, false, onePath("/api/users/", "")),
		verb("all-stats", methodGet, argsAny, false, fixedPath("/api/users/-/stats")),
		verb("list-stats", methodGet, argsOne, false, onePath("/api/users/", "/stats")),
		verb("stats", methodGet, argsOne, false, onePath("/api/users/", ":getStats")),
		verb("usage", methodGet, argsOne, false, onePath("/api/users/", "/usage")),
		verb("settings", methodGet, argsOne, false, onePath("/api/users/", "/settings")),
		verb("setting", methodGet, argsTwo, false, twoPath("/api/users/", "/settings/", "")),
		dangerousVerb("update-setting", methodPatch, argsTwo, true, twoPath("/api/users/", "/settings/", "")),
		verb("tokens", methodGet, argsOne, false, onePath("/api/users/", "/personalAccessTokens")),
		verb("create-token", methodPost, argsOne, true, onePath("/api/users/", "/personalAccessTokens")),
		dangerousVerb("delete-token", methodDelete, argsTwo, false, twoPath("/api/users/", "/personalAccessTokens/", "")),
		verb("webhooks", methodGet, argsOne, false, onePath("/api/users/", "/webhooks")),
		verb("create-webhook", methodPost, argsOne, true, onePath("/api/users/", "/webhooks")),
		verb("update-webhook", methodPatch, argsTwo, true, twoPath("/api/users/", "/webhooks/", "")),
		dangerousVerb("delete-webhook", methodDelete, argsTwo, false, twoPath("/api/users/", "/webhooks/", "")),
		verb("webhook-secret", methodGet, argsTwo, false, twoPath("/api/users/", "/webhooks/", ":getSigningSecret")),
		verb("notifications", methodGet, argsOne, false, onePath("/api/users/", "/notifications")),
		verb("update-notification", methodPatch, argsTwo, true, twoPath("/api/users/", "/notifications/", "")),
		dangerousVerb("delete-notification", methodDelete, argsTwo, false, twoPath("/api/users/", "/notifications/", "")),
		verb("identities", methodGet, argsOne, false, onePath("/api/users/", "/linkedIdentities")),
		verb("identity", methodGet, argsTwo, false, twoPath("/api/users/", "/linkedIdentities/", "")),
		verb("link-identity", methodPost, argsOne, true, onePath("/api/users/", "/linkedIdentities")),
		dangerousVerb("unlink-identity", methodDelete, argsTwo, false, twoPath("/api/users/", "/linkedIdentities/", "")),
		verb("invitations", methodGet, argsOne, false, onePath("/api/users/", "/spaceInvitations")),
		dangerousVerb("import-memos", methodPost, argsOne, true, onePath("/api/users/", ":importMemos")),
		verb("export-memos", methodGet, argsOne, false, onePath("/file/users/", "/export")),
	},
	"auth": {
		aliasVerb([]string{"me", "status"}, methodGet, argsAny, false, fixedPath("/api/auth/me")),
		verb("signin", methodPost, argsAny, true, fixedPath("/api/auth/signin")),
		verb("signout", methodPost, argsAny, false, fixedPath("/api/auth/signout")),
		verb("refresh", methodPost, argsAny, true, fixedPath("/api/auth/refresh")),
	},
	"view": {
		verb("list", methodGet, argsOne, false, onePath("/api/users/", "/views")),
		verb("get", methodGet, argsTwo, false, twoPath("/api/users/", "/views/", "")),
		verb("create", methodPost, argsOne, true, onePath("/api/users/", "/views")),
		verb("update", methodPatch, argsTwo, true, twoPath("/api/users/", "/views/", "")),
		dangerousVerb("delete", methodDelete, argsTwo, false, twoPath("/api/users/", "/views/", "")),
	},
	"space": {
		verb("list", methodGet, argsAny, false, fixedPath("/api/spaces")),
		verb("get", methodGet, argsOne, false, onePath("/api/spaces/", "")),
		verb("create", methodPost, argsAny, true, fixedPath("/api/spaces")),
		verb("update", methodPatch, argsOne, true, onePath("/api/spaces/", "")),
		cacheVerb("delete", methodDelete, argsOne, false, true, onePath("/api/spaces/", "")),
		verb("members", methodGet, argsOne, false, onePath("/api/spaces/", "/members")),
		verb("member", methodGet, argsTwo, false, twoPath("/api/spaces/", "/members/", "")),
		dangerousVerb("update-member", methodPatch, argsTwo, true, twoPath("/api/spaces/", "/members/", "")),
		dangerousVerb("remove-member", methodDelete, argsTwo, false, twoPath("/api/spaces/", "/members/", "")),
		verb("invitations", methodGet, argsOne, false, onePath("/api/spaces/", "/invitations")),
		verb("invitation", methodGet, argsTwo, false, twoPath("/api/spaces/", "/invitations/", "")),
		verb("invite", methodPost, argsOne, true, onePath("/api/spaces/", "/invitations")),
		dangerousVerb("revoke-invitation", methodDelete, argsTwo, false, twoPath("/api/spaces/", "/invitations/", "")),
		verb("accept-invitation", methodPost, argsTwo, true, twoPath("/api/spaces/", "/invitations/", ":accept")),
		dangerousVerb("decline-invitation", methodPost, argsTwo, true, twoPath("/api/spaces/", "/invitations/", ":decline")),
	},
	"idp": {
		verb("list", methodGet, argsAny, false, fixedPath("/api/identity-providers")),
		verb("get", methodGet, argsOne, false, onePath("/api/identity-providers/", "")),
		dangerousVerb("create", methodPost, argsAny, true, fixedPath("/api/identity-providers")),
		dangerousVerb("update", methodPatch, argsOne, true, onePath("/api/identity-providers/", "")),
		dangerousVerb("delete", methodDelete, argsOne, false, onePath("/api/identity-providers/", "")),
	},
	"instance": {
		verb("profile", methodGet, argsAny, false, fixedPath("/api/instance/profile")),
		verb("setting", methodGet, argsOne, false, onePath("/api/instance/settings/", "")),
		verb("batch-settings", methodPost, argsAny, true, fixedPath("/api/instance/settings:batchGet")),
		dangerousVerb("update-setting", methodPatch, argsOne, true, onePath("/api/instance/settings/", "")),
		dangerousVerb("test-email", methodPost, argsAny, true, fixedPath("/api/instance/settings/notification:testEmail")),
		verb("stats", methodGet, argsAny, false, fixedPath("/api/instance/stats")),
		verb("configuration", methodGet, argsAny, false, fixedPath("/api/instance/configuration")),
	},
	"ai": {
		verb("transcribe", methodPost, argsAny, true, fixedPath("/api/ai:transcribe")),
	},
}

func verb(name, method string, args resourceArgMode, body bool, path func([]string) string) resourceVerbSpec {
	return aliasVerb([]string{name}, method, args, body, path)
}

func aliasVerb(names []string, method string, args resourceArgMode, body bool, path func([]string) string) resourceVerbSpec {
	return resourceVerbSpec{Names: names, Method: method, Args: args, Body: body, Path: path}
}

func dangerousVerb(name, method string, args resourceArgMode, body bool, path func([]string) string) resourceVerbSpec {
	spec := verb(name, method, args, body, path)
	spec.Confirm = true
	return spec
}

func cacheVerb(name, method string, args resourceArgMode, body, confirm bool, path func([]string) string) resourceVerbSpec {
	spec := verb(name, method, args, body, path)
	spec.Confirm = confirm
	spec.SyncMemoCache = true
	return spec
}

func handlerVerb(name string, handler func([]string) error) resourceVerbSpec {
	return resourceVerbSpec{Names: []string{name}, Handler: handler}
}

func runResourceVerb(resource string, args []string) error {
	if len(args) == 0 {
		return resourceUsage(resource)
	}
	verbName, rest := args[0], args[1:]
	for _, spec := range resourceVerbSpecs[resource] {
		if !matchesVerb(spec, verbName) {
			continue
		}
		if spec.Handler != nil {
			return spec.Handler(rest)
		}
		return runResourceVerbSpec(resource, verbName, rest, spec)
	}
	return fmt.Errorf("unknown %s verb %q", resource, verbName)
}

func matchesVerb(spec resourceVerbSpec, name string) bool {
	for _, n := range spec.Names {
		if n == name {
			return true
		}
	}
	return false
}

func runResourceVerbSpec(resource, name string, args []string, spec resourceVerbSpec) error {
	fs := flag.NewFlagSet(resource+" "+name, flag.ContinueOnError)
	af := &apiFlags{}
	af.register(fs)
	var uploadFile string
	var uploadMemo, replaceAttachment, conflictPolicy string
	var validateOnly bool
	var attachmentNames multiFlag
	if resource == "attachment" && (name == "create" || name == "upload") {
		fs.StringVar(&uploadFile, "file", "", "local file to upload")
		fs.StringVar(&uploadMemo, "memo", "", "memo resource name to attach the file to")
		fs.StringVar(&replaceAttachment, "replace", "", "replace an attachment's content (26.11.1+; requires --yes)")
	}
	if resource == "user" && name == "import-memos" {
		fs.StringVar(&uploadFile, "file", "", "Memos export archive to import")
		fs.StringVar(&conflictPolicy, "conflict-policy", "SKIP", "SKIP, REPLACE, or DUPLICATE")
		fs.BoolVar(&validateOnly, "validate-only", false, "upload and validate without importing")
	}
	if resource == "memo" && name == "set-attachments" {
		fs.Var(&attachmentNames, "attachment", "attachment resource name (repeatable)")
	}
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	if err := validateResourceArgs(resource, name, spec.Args, pos); err != nil {
		return err
	}
	if uploadFile != "" {
		path := spec.Path(pos)
		if resource == "attachment" {
			path = "/api/attachments:upload"
		}
		if len(af.queries) > 0 || af.output != "" || af.stream {
			return fmt.Errorf("file uploads do not support --query, --output, or --stream")
		}
		if resource == "user" && conflictPolicy != "SKIP" && conflictPolicy != "REPLACE" && conflictPolicy != "DUPLICATE" {
			return fmt.Errorf("conflict-policy must be SKIP, REPLACE, or DUPLICATE")
		}
		return runFileUpload(resource, name, path, uploadFile, uploadMemo, replaceAttachment, conflictPolicy, validateOnly, af)
	}
	if uploadMemo != "" || replaceAttachment != "" || validateOnly {
		return fmt.Errorf("--memo, --replace, and --validate-only require --file")
	}
	if resource == "user" && name == "export-memos" && af.output == "" && !af.stream {
		return fmt.Errorf("export-memos requires --output <file> or --stream")
	}
	var body any
	if spec.Body {
		switch {
		case len(attachmentNames) > 0:
			if af.hasBodyInput() {
				return fmt.Errorf("--attachment cannot be combined with --body, --body-file, or --field")
			}
			attachments := make([]map[string]string, 0, len(attachmentNames))
			for _, attachmentName := range attachmentNames {
				if !strings.HasPrefix(attachmentName, "attachments/") ||
					strings.TrimPrefix(attachmentName, "attachments/") == "" {
					return fmt.Errorf("invalid attachment resource name %q (want attachments/<id>)", attachmentName)
				}
				attachments = append(attachments, map[string]string{"name": attachmentName})
			}
			body = map[string]any{"attachments": attachments}
		default:
			body, err = af.buildBody()
			if err != nil {
				return err
			}
		}
	}
	path := spec.Path(pos) + af.queryString()
	if af.dryRun {
		return printJSON(dryRunResult{
			DryRun:      true,
			Operation:   name,
			Resource:    resource,
			Method:      spec.Method,
			Path:        path,
			Body:        body,
			CachePolicy: cachePolicy(spec.SyncMemoCache || af.sync),
		})
	}
	if spec.Confirm && !af.yes {
		return confirmationRequired(resource + " " + name)
	}
	if af.output != "" || af.stream {
		if spec.Method != methodGet {
			return fmt.Errorf("--output and --stream require GET")
		}
		return runAPIOutput(path, af)
	}
	return runAPI(spec.Method, path, body, spec.SyncMemoCache || af.sync)
}

func validateResourceArgs(resource, name string, mode resourceArgMode, pos []string) error {
	switch mode {
	case argsOne:
		if len(pos) != 1 {
			return fmt.Errorf("usage: memoq %s %s <id> [flags]", resource, name)
		}
	case argsTwo:
		if len(pos) != 2 {
			return fmt.Errorf("usage: memoq %s %s <arg1> <arg2> [flags]", resource, name)
		}
	case argsOptionalOne:
		if len(pos) > 1 {
			return fmt.Errorf("usage: memoq %s %s [id] [flags]", resource, name)
		}
	}
	return nil
}

func fixedPath(path string) func([]string) string {
	return func([]string) string {
		return path
	}
}

func onePath(prefix, suffix string) func([]string) string {
	return func(pos []string) string {
		return prefix + url.PathEscape(pos[0]) + suffix
	}
}

func twoPath(prefix, middle, suffix string) func([]string) string {
	return func(pos []string) string {
		return prefix + url.PathEscape(pos[0]) + middle + url.PathEscape(pos[1]) + suffix
	}
}

func optionalOnePath(collection, itemPrefix string) func([]string) string {
	return func(pos []string) string {
		if len(pos) == 0 {
			return collection
		}
		return itemPrefix + url.PathEscape(pos[0])
	}
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
			"  e.g. memoq api GET /api/memos --query pageSize=10\n" +
			"       memoq api POST /api/memos --field content='hi' --field visibility=PRIVATE")
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
	path += af.queryString()
	if af.dryRun {
		return printJSON(dryRunResult{
			DryRun:      true,
			Operation:   strings.ToLower(method),
			Resource:    "api",
			Method:      method,
			Path:        path,
			Body:        body,
			CachePolicy: cachePolicy(af.sync),
		})
	}
	if requiresAPIConfirmation(method, path) && !af.yes {
		return confirmationRequired(method + " " + path)
	}
	if af.output != "" || af.stream {
		if method != methodGet {
			return fmt.Errorf("--output and --stream require GET")
		}
		return runAPIOutput(path, &af)
	}
	return runAPI(method, path, body, af.sync)
}

func cachePolicy(syncCache bool) string {
	if syncCache {
		return "sync"
	}
	return "unchanged"
}

func requiresAPIConfirmation(method, path string) bool {
	if method == methodDelete {
		return true
	}
	if method == methodPost && (strings.Contains(path, ":batchDelete") || strings.Contains(path, "/shares") ||
		strings.Contains(path, ":importMemos") || strings.Contains(path, ":testEmail") ||
		strings.Contains(path, ":decline") || strings.HasPrefix(path, "/api/identity-providers")) {
		return true
	}
	return method == methodPatch &&
		(strings.Contains(path, "/instance/settings/") || strings.Contains(path, "/users/") && strings.Contains(path, "/settings/") ||
			strings.Contains(path, "/spaces/") && strings.Contains(path, "/members/") || strings.HasPrefix(path, "/api/identity-providers"))
}
