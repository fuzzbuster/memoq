package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"mime"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/example/memoq/internal/config"
	"github.com/example/memoq/internal/memos"
	"github.com/example/memoq/internal/store"
	"github.com/example/memoq/internal/syncer"
)

// ---- search ----------------------------------------------------------------

func cmdSearch(args []string) error {
	fs := flag.NewFlagSet("search", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "emit JSON")
	noSync := fs.Bool("no-sync", false, "skip read-time auto-sync")
	limit := fs.Int("limit", 10, "max results")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	query := strings.TrimSpace(strings.Join(pos, " "))
	if query == "" {
		return fmt.Errorf("usage: memoq search <query> [--limit N] [--json]")
	}
	a, err := openApp()
	if err != nil {
		return err
	}
	defer a.close()
	a.autoSync(*noSync)

	hits, err := a.store.Search(query, *limit)
	if err != nil {
		return err
	}
	if *asJSON {
		return printJSON(hits)
	}
	if len(hits) == 0 {
		fmt.Println("(no matches; try different keywords)")
		return nil
	}
	for _, h := range hits {
		printMemoLine(&h.Memo)
	}
	return nil
}

// ---- list ------------------------------------------------------------------

func cmdList(args []string) error {
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "emit JSON")
	noSync := fs.Bool("no-sync", false, "skip read-time auto-sync")
	tag := fs.String("tag", "", "filter by tag (without #)")
	from := fs.String("from", "", "created on/after (YYYY-MM-DD)")
	to := fs.String("to", "", "created on/before (YYYY-MM-DD)")
	vis := fs.String("visibility", "", "PUBLIC/PRIVATE/PROTECTED")
	limit := fs.Int("limit", 50, "max results")
	offset := fs.Int("offset", 0, "skip N results")
	if _, err := parseArgs(fs, args); err != nil {
		return err
	}
	a, err := openApp()
	if err != nil {
		return err
	}
	defer a.close()
	a.autoSync(*noSync)

	f := store.ListFilter{
		Tag:        *tag,
		Visibility: *vis,
		Limit:      *limit,
		Offset:     *offset,
	}
	if *from != "" {
		ts, err := parseDate(*from, false)
		if err != nil {
			return err
		}
		f.FromUnix = ts
	}
	if *to != "" {
		ts, err := parseDate(*to, true)
		if err != nil {
			return err
		}
		f.ToUnix = ts
	}
	memos, err := a.store.List(f)
	if err != nil {
		return err
	}
	if *asJSON {
		return printJSON(memos)
	}
	if len(memos) == 0 {
		fmt.Println("(no notes)")
		return nil
	}
	for _, m := range memos {
		printMemoLine(m)
	}
	return nil
}

// ---- get -------------------------------------------------------------------

func cmdGet(args []string) error {
	fs := flag.NewFlagSet("get", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "emit JSON")
	noSync := fs.Bool("no-sync", false, "skip read-time auto-sync")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return fmt.Errorf("usage: memoq get <uid> [--json]")
	}
	a, err := openApp()
	if err != nil {
		return err
	}
	defer a.close()
	a.autoSync(*noSync)

	m, err := a.store.Get(pos[0])
	if err != nil {
		return err
	}
	if m == nil {
		return fmt.Errorf("no memo with uid %q (try 'memoq sync')", pos[0])
	}
	if *asJSON {
		return printJSON(m)
	}
	printMemoFull(m)
	return nil
}

// ---- create ----------------------------------------------------------------

func cmdCreate(args []string) error {
	fs := flag.NewFlagSet("create", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "emit JSON")
	content := fs.String("content", "", "note content (else read from stdin)")
	vis := fs.String("visibility", "PRIVATE", "PUBLIC/PRIVATE/PROTECTED")
	var tags multiFlag
	var attachments multiFlag
	fs.Var(&tags, "tag", "tag to append as #tag (repeatable)")
	fs.Var(&attachments, "attach", "local file to attach (repeatable)")
	if _, err := parseArgs(fs, args); err != nil {
		return err
	}
	body := *content
	if body == "" {
		piped, _ := io.ReadAll(os.Stdin)
		body = strings.TrimRight(string(piped), "\n")
	}
	if strings.TrimSpace(body) == "" {
		return fmt.Errorf("empty content (use --content or pipe via stdin)")
	}
	// Append tags as Memos-style hashtags so the server indexes them.
	for _, t := range tags {
		t = strings.TrimPrefix(t, "#")
		body += "\n#" + t
	}

	a, err := openApp()
	if err != nil {
		return err
	}
	defer a.close()
	cl, err := a.client()
	if err != nil {
		return err
	}
	rm, err := cl.Create(context.Background(), body, *vis)
	if err != nil {
		return err
	}
	// Reflect immediately into local cache (dynamic freshness without waiting
	// for the next sync).
	_ = a.store.Upsert(&store.Memo{
		UID:         rm.UIDValue(),
		Content:     rm.Content,
		Tags:        rm.TagList(),
		Visibility:  rm.Visibility,
		Pinned:      rm.Pinned,
		CreatedTime: nowOr(rm.CreateTime),
		UpdatedTime: nowOr(rm.UpdateTime),
		ContentHash: rm.ContentMD5(),
	})
	uploaded := make([]*memos.Attachment, 0, len(attachments))
	for _, path := range attachments {
		content, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("created memo %s but cannot read attachment %q: %w", rm.UIDValue(), path, err)
		}
		filename := filepath.Base(path)
		attachment, err := cl.CreateAttachment(
			context.Background(),
			rm.UIDValue(),
			filename,
			mime.TypeByExtension(filepath.Ext(filename)),
			content,
		)
		if err != nil {
			return fmt.Errorf("created memo %s but cannot upload attachment %q: %w", rm.UIDValue(), path, err)
		}
		uploaded = append(uploaded, attachment)
		_ = a.store.UpsertAttachment(&store.Attachment{
			UID:          attachment.IDValue(),
			MemoUID:      rm.UIDValue(),
			Filename:     attachment.Filename,
			Type:         attachment.Type,
			Size:         attachment.Size,
			ExternalLink: attachment.ExternalLink,
			CreatedTime:  nowOr(attachment.CreateTime),
		})
	}
	if *asJSON {
		return printJSON(struct {
			UID         string              `json:"uid"`
			Attachments []*memos.Attachment `json:"attachments,omitempty"`
		}{UID: rm.UIDValue(), Attachments: uploaded})
	}
	fmt.Printf("created %s\n", rm.UIDValue())
	return nil
}

// ---- update ----------------------------------------------------------------

func cmdUpdate(args []string) error {
	fs := flag.NewFlagSet("update", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "emit JSON")
	content := fs.String("content", "", "new content")
	vis := fs.String("visibility", "", "new visibility")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return fmt.Errorf("usage: memoq update <uid> --content ... [--visibility ...]")
	}
	if *content == "" && *vis == "" {
		return fmt.Errorf("nothing to update (pass --content and/or --visibility)")
	}
	uid := pos[0]
	a, err := openApp()
	if err != nil {
		return err
	}
	defer a.close()
	cl, err := a.client()
	if err != nil {
		return err
	}
	rm, err := cl.Update(context.Background(), uid, *content, *vis)
	if err != nil {
		return err
	}
	_ = a.store.Upsert(&store.Memo{
		UID:         rm.UIDValue(),
		Content:     rm.Content,
		Tags:        rm.TagList(),
		Visibility:  rm.Visibility,
		Pinned:      rm.Pinned,
		CreatedTime: nowOr(rm.CreateTime),
		UpdatedTime: nowOr(rm.UpdateTime),
		ContentHash: rm.ContentMD5(),
	})
	if *asJSON {
		return printJSON(map[string]string{"uid": rm.UIDValue()})
	}
	fmt.Printf("updated %s\n", rm.UIDValue())
	return nil
}

// ---- delete ----------------------------------------------------------------

func cmdDelete(args []string) error {
	fs := flag.NewFlagSet("delete", flag.ContinueOnError)
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return fmt.Errorf("usage: memoq delete <uid>")
	}
	uid := pos[0]
	a, err := openApp()
	if err != nil {
		return err
	}
	defer a.close()
	cl, err := a.client()
	if err != nil {
		return err
	}
	if err := cl.Delete(context.Background(), uid); err != nil {
		return err
	}
	_ = a.store.Delete(uid)
	fmt.Printf("deleted %s\n", uid)
	return nil
}

// ---- stats -----------------------------------------------------------------

func cmdStats(args []string) error {
	fs := flag.NewFlagSet("stats", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "emit JSON")
	noSync := fs.Bool("no-sync", false, "skip read-time auto-sync")
	if _, err := parseArgs(fs, args); err != nil {
		return err
	}
	a, err := openApp()
	if err != nil {
		return err
	}
	defer a.close()
	a.autoSync(*noSync)

	st, err := a.store.Stats()
	if err != nil {
		return err
	}
	if *asJSON {
		return printJSON(st)
	}
	fmt.Printf("total:     %d\n", st.Total)
	fmt.Printf("pinned:    %d\n", st.Pinned)
	fmt.Printf("newest:    %s\n", tsStr(st.NewestTS))
	fmt.Printf("oldest:    %s\n", tsStr(st.OldestTS))
	fmt.Printf("last sync: %s\n", tsStr(st.LastSyncTS))
	if len(st.TagCounts) > 0 {
		fmt.Println("tags:")
		for _, tc := range topTags(st.TagCounts, 20) {
			fmt.Printf("  #%-20s %d\n", tc.tag, tc.n)
		}
	}
	return nil
}

// ---- sync ------------------------------------------------------------------

func cmdSync(args []string) error {
	fs := flag.NewFlagSet("sync", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "emit JSON")
	if _, err := parseArgs(fs, args); err != nil {
		return err
	}
	a, err := openApp()
	if err != nil {
		return err
	}
	defer a.close()
	cl, err := a.client()
	if err != nil {
		return err
	}
	sy := syncer.New(cl, a.store)
	res, err := sy.Sync(context.Background())
	if err != nil {
		return err
	}
	if *asJSON {
		return printJSON(res)
	}
	fmt.Printf("synced: +%d added, ~%d updated, -%d deleted, %d unchanged (%d remote) in %s\n",
		res.Added, res.Updated, res.Deleted, res.Skipped, res.Total, res.Duration.Round(time.Millisecond))
	return nil
}

// ---- config ----------------------------------------------------------------

func cmdConfig(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: memoq config <get|set|path|list> ...")
	}
	paths, err := config.ResolvePaths()
	if err != nil {
		return err
	}
	cfg, err := config.Load(paths)
	if err != nil {
		return err
	}
	switch args[0] {
	case "path":
		fmt.Println(paths.ConfigFile)
		fmt.Println(paths.DBFile)
		return nil
	case "list":
		masked := *cfg
		if masked.Token != "" {
			masked.Token = "***set***" // never echo the secret
		}
		return printJSON(masked)
	case "get":
		if len(args) != 2 {
			return fmt.Errorf("usage: memoq config get <key>")
		}
		v, err := getConfigKey(cfg, args[1])
		if err != nil {
			return err
		}
		fmt.Println(v)
		return nil
	case "set":
		if len(args) != 3 {
			return fmt.Errorf("usage: memoq config set <key> <value>")
		}
		if err := setConfigKey(cfg, args[1], args[2]); err != nil {
			return err
		}
		if err := config.Save(paths, cfg); err != nil {
			return err
		}
		fmt.Printf("set %s\n", args[1])
		return nil
	default:
		return fmt.Errorf("unknown config subcommand %q", args[0])
	}
}

func getConfigKey(c *config.Config, key string) (string, error) {
	switch key {
	case "server_url":
		return c.ServerURL, nil
	case "token":
		if c.Token == "" {
			return "", nil
		}
		return "***set***", nil // never echo the secret
	case "auto_sync_ttl_seconds":
		return fmt.Sprintf("%d", c.AutoSyncTTLSeconds), nil
	default:
		return "", fmt.Errorf("unknown key %q", key)
	}
}

func setConfigKey(c *config.Config, key, val string) error {
	switch key {
	case "server_url":
		c.ServerURL = strings.TrimRight(val, "/")
	case "token":
		c.Token = val
	case "auto_sync_ttl_seconds":
		var n int
		if _, err := fmt.Sscanf(val, "%d", &n); err != nil {
			return fmt.Errorf("auto_sync_ttl_seconds must be an integer")
		}
		c.AutoSyncTTLSeconds = n
	default:
		return fmt.Errorf("unknown key %q (server_url|token|auto_sync_ttl_seconds)", key)
	}
	return nil
}

// ---- helpers ---------------------------------------------------------------

// parseArgs parses flags that may appear before, after, or interspersed with
// positional arguments (Go's flag package stops at the first positional).
// It returns the collected positional arguments. This matters because coding
// agents frequently write `memoq search foo --json` with the flag last.
func parseArgs(fs *flag.FlagSet, args []string) ([]string, error) {
	var positionals []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		rest := fs.Args()
		if len(rest) == 0 {
			break
		}
		positionals = append(positionals, rest[0])
		args = rest[1:]
	}
	return positionals, nil
}

// multiFlag collects a repeatable string flag (e.g. --tag a --tag b).
type multiFlag []string

func (m *multiFlag) String() string { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error {
	*m = append(*m, v)
	return nil
}

// parseDate parses YYYY-MM-DD. endOfDay pushes to 23:59:59 for inclusive --to.
func parseDate(s string, endOfDay bool) (int64, error) {
	t, err := time.ParseInLocation("2006-01-02", s, time.Local)
	if err != nil {
		return 0, fmt.Errorf("invalid date %q (want YYYY-MM-DD)", s)
	}
	if endOfDay {
		t = t.Add(24*time.Hour - time.Second)
	}
	return t.Unix(), nil
}

func nowOr(t *time.Time) int64 {
	if t != nil {
		return t.Unix()
	}
	return time.Now().Unix()
}

type tagCount struct {
	tag string
	n   int
}

// topTags returns the n most frequent tags, sorted by count desc then name.
func topTags(m map[string]int, n int) []tagCount {
	out := make([]tagCount, 0, len(m))
	for t, c := range m {
		out = append(out, tagCount{t, c})
	}
	// simple insertion-ish sort; tag sets are small
	for i := 1; i < len(out); i++ {
		for j := i; j > 0; j-- {
			a, b := out[j-1], out[j]
			if b.n > a.n || (b.n == a.n && b.tag < a.tag) {
				out[j-1], out[j] = out[j], out[j-1]
			} else {
				break
			}
		}
	}
	if n > 0 && len(out) > n {
		out = out[:n]
	}
	return out
}
