package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/example/memoq/internal/store"
)

// printJSON writes v as indented JSON to stdout.
func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}

type writeResult struct {
	Operation   string `json:"operation"`
	UID         string `json:"uid"`
	Deleted     bool   `json:"deleted,omitempty"`
	CacheStatus string `json:"cache_status"`
	Attachments any    `json:"attachments,omitempty"`
}

type dryRunResult struct {
	DryRun      bool   `json:"dry_run"`
	Operation   string `json:"operation,omitempty"`
	Resource    string `json:"resource,omitempty"`
	UID         string `json:"uid,omitempty"`
	Method      string `json:"method,omitempty"`
	Path        string `json:"path,omitempty"`
	Body        any    `json:"body,omitempty"`
	CachePolicy string `json:"cache_policy"`
}

type cliError struct {
	Code               string
	Message            string
	RequiresUserAction bool
}

func (e *cliError) Error() string {
	return e.Message
}

func confirmationRequired(operation string) error {
	return &cliError{
		Code:               "confirmation_required",
		Message:            operation + " requires --yes; run with --dry-run first",
		RequiresUserAction: true,
	}
}

func warnCache(err error) {
	fmt.Fprintln(os.Stderr, "memoq: remote write succeeded but local cache is stale ("+err.Error()+")")
}

// WriteError emits a stable JSON error on stderr for JSON and resource
// invocations; plain fast-command invocations keep the concise text error.
func WriteError(err error, args []string) {
	structured := hasArg(args, "--json") || len(args) > 0 && isResourceGroup(args[0])
	if !structured {
		fmt.Fprintln(os.Stderr, "memoq: "+err.Error())
		return
	}
	code := "execution_failed"
	requiresUserAction := false
	var ce *cliError
	if errors.As(err, &ce) {
		code = ce.Code
		requiresUserAction = ce.RequiresUserAction
	} else {
		message := err.Error()
		switch {
		case strings.HasPrefix(message, "usage:") || strings.Contains(message, "unknown command") ||
			strings.Contains(message, "unknown flag") || strings.Contains(message, "flag provided but not defined") ||
			strings.Contains(message, "invalid date"):
			code = "invalid_arguments"
		case strings.Contains(message, "server_url is not set") || strings.Contains(message, "token is not set"):
			code = "configuration_required"
			requiresUserAction = true
		case strings.Contains(message, "-> 401"):
			code = "unauthorized"
			requiresUserAction = true
		case strings.Contains(message, "-> 403"):
			code = "forbidden"
			requiresUserAction = true
		case strings.Contains(message, "-> 404") || strings.HasPrefix(message, "no memo with uid"):
			code = "not_found"
		}
	}
	payload := struct {
		Error struct {
			Code               string `json:"code"`
			Message            string `json:"message"`
			RequiresUserAction bool   `json:"requires_user_action"`
		} `json:"error"`
	}{}
	payload.Error.Code = code
	payload.Error.Message = err.Error()
	payload.Error.RequiresUserAction = requiresUserAction
	enc := json.NewEncoder(os.Stderr)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	_ = enc.Encode(payload)
}

func hasArg(args []string, target string) bool {
	for _, arg := range args {
		if arg == target {
			return true
		}
		if strings.HasPrefix(arg, target+"=") {
			value, err := strconv.ParseBool(strings.TrimPrefix(arg, target+"="))
			return err == nil && value
		}
	}
	return false
}

// tsStr renders a Unix timestamp as YYYY-MM-DD HH:MM (empty when zero).
func tsStr(ts int64) string {
	if ts <= 0 {
		return "-"
	}
	return time.Unix(ts, 0).Format("2006-01-02 15:04")
}

// oneLine collapses a memo's content into a short single-line preview.
func oneLine(content string, max int) string {
	s := strings.ReplaceAll(content, "\n", " ")
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) > max {
		return string(r[:max]) + "…"
	}
	return s
}

// printMemoLine writes a compact one-line summary for list/search output.
func printMemoLine(m *store.Memo) {
	pin := " "
	if m.Pinned {
		pin = "*"
	}
	tags := ""
	if len(m.Tags) > 0 {
		tags = "  #" + strings.Join(m.Tags, " #")
	}
	fmt.Printf("%s %-20s  %s  %s%s\n", pin, m.UID, tsStr(m.CreatedTime), oneLine(m.Content, 60), tags)
}

// printMemoFull writes a full note for the `get` command (plain text).
func printMemoFull(m *store.Memo) {
	fmt.Printf("uid:        %s\n", m.UID)
	fmt.Printf("visibility: %s\n", m.Visibility)
	fmt.Printf("pinned:     %v\n", m.Pinned)
	fmt.Printf("created:    %s\n", tsStr(m.CreatedTime))
	fmt.Printf("updated:    %s\n", tsStr(m.UpdatedTime))
	if len(m.Tags) > 0 {
		fmt.Printf("tags:       #%s\n", strings.Join(m.Tags, " #"))
	}
	fmt.Println("---")
	fmt.Println(m.Content)
}
