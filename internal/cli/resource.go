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
		Name:  "shortcut",
		Use:   "shortcut <verb>",
		Short: "Direct shortcut API commands",
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

type resourceArgMode int

const (
	argsAny resourceArgMode = iota
	argsOne
	argsTwo
	argsOptionalOne
)

type resourceVerbSpec struct {
	Names   []string
	Method  string
	Args    resourceArgMode
	Body    bool
	Path    func([]string) string
	Handler func([]string) error
}

var resourceVerbSpecs = map[string][]resourceVerbSpec{
	"memo": {
		verb("list", http.MethodGet, argsAny, false, fixedPath("/api/v1/memos")),
		verb("get", http.MethodGet, argsOne, false, onePath("/api/v1/memos/", "")),
		verb("create", http.MethodPost, argsAny, true, fixedPath("/api/v1/memos")),
		verb("update", http.MethodPatch, argsOne, true, onePath("/api/v1/memos/", "")),
		verb("delete", http.MethodDelete, argsOne, false, onePath("/api/v1/memos/", "")),
		verb("comments", http.MethodGet, argsOne, false, onePath("/api/v1/memos/", "/comments")),
		verb("comment", http.MethodPost, argsOne, true, onePath("/api/v1/memos/", "/comments")),
		verb("relations", http.MethodGet, argsOne, false, onePath("/api/v1/memos/", "/relations")),
		verb("set-relations", http.MethodPatch, argsOne, true, onePath("/api/v1/memos/", "/relations")),
		verb("reactions", http.MethodGet, argsOne, false, onePath("/api/v1/memos/", "/reactions")),
		verb("react", http.MethodPost, argsOne, true, onePath("/api/v1/memos/", "/reactions")),
		verb("unreact", http.MethodDelete, argsTwo, false, twoPath("/api/v1/memos/", "/reactions/", "")),
		verb("attachments", http.MethodGet, argsOne, false, onePath("/api/v1/memos/", "/attachments")),
		verb("set-attachments", http.MethodPatch, argsOne, true, onePath("/api/v1/memos/", "/attachments")),
		verb("shares", http.MethodGet, argsOne, false, onePath("/api/v1/memos/", "/shares")),
		verb("share", http.MethodPost, argsOne, true, onePath("/api/v1/memos/", "/shares")),
		verb("unshare", http.MethodDelete, argsTwo, false, twoPath("/api/v1/memos/", "/shares/", "")),
		verb("link-metadata", http.MethodGet, argsAny, false, fixedPath("/api/v1/memos/-/linkMetadata")),
	},
	"attachment": {
		handlerVerb("pull", cmdAttachmentPull),
		verb("list", http.MethodGet, argsAny, false, fixedPath("/api/v1/attachments")),
		verb("get", http.MethodGet, argsOne, false, onePath("/api/v1/attachments/", "")),
		verb("create", http.MethodPost, argsAny, true, fixedPath("/api/v1/attachments")),
		verb("update", http.MethodPatch, argsOne, true, onePath("/api/v1/attachments/", "")),
		verb("delete", http.MethodDelete, argsOne, false, onePath("/api/v1/attachments/", "")),
		verb("batch-delete", http.MethodPost, argsAny, true, fixedPath("/api/v1/attachments:batchDelete")),
	},
	"user": {
		verb("list", http.MethodGet, argsAny, false, fixedPath("/api/v1/users")),
		verb("get", http.MethodGet, argsOne, false, onePath("/api/v1/users/", "")),
		verb("create", http.MethodPost, argsAny, true, fixedPath("/api/v1/users")),
		verb("update", http.MethodPatch, argsOne, true, onePath("/api/v1/users/", "")),
		verb("delete", http.MethodDelete, argsOne, false, onePath("/api/v1/users/", "")),
		verb("all-stats", http.MethodGet, argsAny, false, fixedPath("/api/v1/users:stats")),
		verb("stats", http.MethodGet, argsOne, false, onePath("/api/v1/users/", ":getStats")),
		verb("settings", http.MethodGet, argsOne, false, onePath("/api/v1/users/", "/settings")),
		verb("setting", http.MethodGet, argsTwo, false, twoPath("/api/v1/users/", "/settings/", "")),
		verb("update-setting", http.MethodPatch, argsTwo, true, twoPath("/api/v1/users/", "/settings/", "")),
		verb("tokens", http.MethodGet, argsOne, false, onePath("/api/v1/users/", "/personalAccessTokens")),
		verb("create-token", http.MethodPost, argsOne, true, onePath("/api/v1/users/", "/personalAccessTokens")),
		verb("delete-token", http.MethodDelete, argsTwo, false, twoPath("/api/v1/users/", "/personalAccessTokens/", "")),
		verb("webhooks", http.MethodGet, argsOne, false, onePath("/api/v1/users/", "/webhooks")),
	},
	"auth": {
		aliasVerb([]string{"me", "status"}, http.MethodGet, argsAny, false, fixedPath("/api/v1/auth/me")),
		verb("signin", http.MethodPost, argsAny, true, fixedPath("/api/v1/auth/signin")),
		verb("signout", http.MethodPost, argsAny, false, fixedPath("/api/v1/auth/signout")),
		verb("refresh", http.MethodPost, argsAny, true, fixedPath("/api/v1/auth/refresh")),
	},
	"shortcut": {
		verb("list", http.MethodGet, argsOne, false, onePath("/api/v1/users/", "/shortcuts")),
		verb("get", http.MethodGet, argsTwo, false, twoPath("/api/v1/users/", "/shortcuts/", "")),
		verb("create", http.MethodPost, argsOne, true, onePath("/api/v1/users/", "/shortcuts")),
		verb("update", http.MethodPatch, argsTwo, true, twoPath("/api/v1/users/", "/shortcuts/", "")),
		verb("delete", http.MethodDelete, argsTwo, false, twoPath("/api/v1/users/", "/shortcuts/", "")),
	},
	"instance": {
		verb("profile", http.MethodGet, argsAny, false, fixedPath("/api/v1/instance/profile")),
		verb("setting", http.MethodGet, argsOptionalOne, false, optionalOnePath("/api/v1/instance/settings", "/api/v1/instance/settings/")),
		verb("update-setting", http.MethodPatch, argsOne, true, onePath("/api/v1/instance/settings/", "")),
		verb("stats", http.MethodGet, argsAny, false, fixedPath("/api/v1/instance/stats")),
	},
	"ai": {
		verb("transcribe", http.MethodPost, argsAny, true, fixedPath("/api/v1/ai:transcribe")),
	},
}

func verb(name, method string, args resourceArgMode, body bool, path func([]string) string) resourceVerbSpec {
	return aliasVerb([]string{name}, method, args, body, path)
}

func aliasVerb(names []string, method string, args resourceArgMode, body bool, path func([]string) string) resourceVerbSpec {
	return resourceVerbSpec{Names: names, Method: method, Args: args, Body: body, Path: path}
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
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	if err := validateResourceArgs(resource, name, spec.Args, pos); err != nil {
		return err
	}
	var body any
	if spec.Body {
		body, err = af.buildBody()
		if err != nil {
			return err
		}
	}
	return runAPI(spec.Method, spec.Path(pos)+af.queryString(), body)
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
	return runResourceVerb("memo", args)
}

// ---- attachment resource ---------------------------------------------------

func cmdAttachmentResource(args []string) error {
	return runResourceVerb("attachment", args)
}

// ---- user resource ---------------------------------------------------------

func cmdUserResource(args []string) error {
	return runResourceVerb("user", args)
}

// ---- auth resource ---------------------------------------------------------

func cmdAuthResource(args []string) error {
	return runResourceVerb("auth", args)
}

// ---- shortcut resource -----------------------------------------------------

func cmdShortcutResource(args []string) error {
	return runResourceVerb("shortcut", args)
}

// ---- instance resource -----------------------------------------------------

func cmdInstanceResource(args []string) error {
	return runResourceVerb("instance", args)
}

// ---- ai resource -----------------------------------------------------------

func cmdAIResource(args []string) error {
	return runResourceVerb("ai", args)
}
