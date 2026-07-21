package cli

import (
	"context"
	"flag"
	"fmt"
)

func cmdMemoRelate(args []string) error {
	fs := flag.NewFlagSet("memo relate", flag.ContinueOnError)
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 2 {
		return fmt.Errorf("usage: memoq memo relate <memo-uid> <related-memo-uid>")
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
	raw, err := cl.AddMemoReference(context.Background(), pos[0], pos[1])
	if err != nil {
		return err
	}
	return printRawJSON(raw)
}
