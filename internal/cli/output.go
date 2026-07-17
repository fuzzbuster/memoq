package cli

import (
	"encoding/json"
	"fmt"
	"os"
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
