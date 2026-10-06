package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"uuid"

	"github.com/yashok111/mocker/internal/store"
)

func runBackendStorage(ctx context.Context, args []string, out, stderr io.Writer) error {
	if len(args) == 0 || (args[0] != "verify" && args[0] != "rebuild") {
		return fmt.Errorf("usage: mocker backend-storage verify --db PATH | rebuild --db PATH --project UUID")
	}
	flags := flag.NewFlagSet("backend-storage "+args[0], flag.ContinueOnError)
	flags.SetOutput(stderr)
	path := flags.String("db", "", "existing offline Store27 database")
	project := flags.String("project", "", "project UUID (rebuild only)")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if *path == "" || flags.NArg() != 0 {
		return fmt.Errorf("--db PATH required; positional arguments are not allowed")
	}
	rebuild := args[0] == "rebuild"
	if rebuild {
		if _, err := uuid.Parse(*project); err != nil {
			return fmt.Errorf("rebuild requires --project UUID")
		}
	} else if *project != "" {
		return fmt.Errorf("verify checks the whole database; --project is only for rebuild")
	}
	if err := store.BackendStorage(ctx, *path, *project, rebuild); err != nil {
		return err
	}
	_, err := fmt.Fprintln(out, "backend-storage", args[0]+": OK")
	return err
}
