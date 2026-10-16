// Command lintquests is the data-lint quest board from the command line (SPEC §7.24.4).
//
// WHY A CLI EXISTS FOR A FEATURE THAT IS SUPPOSED TO REACH THE UI. The plan's remaining work on this
// item is the four frontend surfaces, and a service with no caller until then is a service nobody can
// check: "the detectors run" would be a claim about code rather than about the archive. This binary is
// the caller that makes the detectors RUNNABLE -- against a real database, with real rows, by an
// operator who wants to know what is wrong -- and it is also the instrument the plan's verification
// uses. When the frontend lands it will call the same service, and this stays useful for the two jobs
// a web page is worst at: a scheduled sweep, and an import validation pass.
//
// FOUR MODES, and each prints something a person can act on:
//
//	-validate    refuse a registry that names a detector this build does not implement
//	-ensure      seed the registry rows for this build's detectors, then stop
//	(default)    run every enabled detector and print the board
//	-entity      validate SPECIFIC entity ids, which is §7.24.4's import pass
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/gofrs/uuid"
	"github.com/spf13/viper"

	"github.com/stashapp/stash-box/internal/config"
	"github.com/stashapp/stash-box/internal/database"
	"github.com/stashapp/stash-box/internal/queries"
	"github.com/stashapp/stash-box/internal/service/lint"
)

func main() {
	configPath := flag.String("config", "", "path to the stash-box config file (required)")
	detector := flag.String("detector", "", "run ONE detector by slug instead of the enabled board")
	limit := flag.Int("limit", 25, "candidates to show per detector")
	validate := flag.Bool("validate", false, "check the registry against this build and exit")
	ensure := flag.Bool("ensure", false, "seed this build's detector rows into the registry and exit")
	entityType := flag.String("entity", "", "with -ids: the entity type to validate (performer|scene)")
	ids := flag.String("ids", "", "with -entity: comma-separated entity ids to validate (the import pass)")
	asJSON := flag.Bool("json", false, "print the board as JSON")
	flag.Parse()

	if err := run(*configPath, *detector, *limit, *validate, *ensure, *entityType, *ids, *asJSON); err != nil {
		fmt.Fprintln(os.Stderr, "lintquests:", err)
		os.Exit(1)
	}
}

func run(configPath, detector string, limit int, validate, ensure bool, entityType, ids string, asJSON bool) error {
	if configPath == "" {
		return fmt.Errorf("-config is required (the same file the server reads)")
	}

	// THE ORDER IS LOAD-BEARING, copied from cmd/sdbimport for the reason recorded there:
	// ReadInConfig FIRST, because config.InitializeDefaults ends in viper.WriteConfig and would
	// overwrite the real config file with defaults.
	viper.SetConfigFile(configPath)
	if err := viper.ReadInConfig(); err != nil {
		return fmt.Errorf("reading config %s: %w", configPath, err)
	}
	if err := config.Initialize(); err != nil {
		return fmt.Errorf("loading config %s: %w", configPath, err)
	}

	db := database.Initialize(config.GetDatabasePath())
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	svc := lint.NewService(queries.New(db))

	switch {
	case ensure:
		inserted, err := svc.EnsureBuiltins(ctx)
		if err != nil {
			return err
		}
		fmt.Printf("seeded %d detector(s); the registry now holds every detector this build implements\n", inserted)
		return nil

	case validate:
		if err := svc.Validate(ctx); err != nil {
			return err
		}
		fmt.Printf("registry agrees with this build: %d detector(s) implemented, every enabled row has one\n",
			len(lint.Builtins()))
		return nil

	case entityType != "":
		refs, err := parseRefs(entityType, ids)
		if err != nil {
			return err
		}
		reports, err := svc.ValidateEntities(ctx, refs)
		if err != nil {
			return err
		}
		return printReports(reports, asJSON, fmt.Sprintf("validated %d %s id(s)", len(refs), entityType))

	case detector != "":
		rep, err := svc.Candidates(ctx, detector, int32(limit))
		if err != nil {
			return err
		}
		return printReports([]lint.Report{rep}, asJSON, "")

	default:
		reports, err := svc.Board(ctx, int32(limit))
		if err != nil {
			return err
		}
		return printReports(reports, asJSON, "")
	}
}

// parseRefs turns the CLI's two-part form into entity references, and refuses anything it cannot
// understand rather than dropping it: a validation pass that silently skipped an id would report the
// import as clean.
func parseRefs(entityType, ids string) ([]lint.EntityRef, error) {
	if ids == "" {
		return nil, fmt.Errorf("-ids is required with -entity")
	}
	var refs []lint.EntityRef
	for _, raw := range strings.Split(ids, ",") {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		id, err := uuid.FromString(raw)
		if err != nil {
			return nil, fmt.Errorf("%q is not a UUID: %w", raw, err)
		}
		refs = append(refs, lint.EntityRef{EntityType: entityType, EntityID: id})
	}
	if len(refs) == 0 {
		return nil, fmt.Errorf("-ids contained no ids")
	}
	return refs, nil
}

func printReports(reports []lint.Report, asJSON bool, headline string) error {
	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(reports)
	}

	if headline != "" {
		fmt.Println(headline)
	}
	total := 0
	for _, r := range reports {
		total += r.Count
		marker := ""
		if r.Count == 0 {
			marker = "  (nothing found)"
		}
		fmt.Printf("\n%s -- %s  [%d finding(s), %d points each]%s\n", r.Slug, r.Description, r.Count, r.BountyPoints, marker)
		if r.LimitNotice != "" {
			fmt.Printf("  ! %s\n", r.LimitNotice)
		}
		for _, c := range r.Candidates {
			name := c.EntityName
			if name == "" {
				name = "(unnamed)"
			}
			fmt.Printf("  %s  %s  %s\n", c.EntityID, name, c.Detail)
		}
	}
	fmt.Printf("\ntotal: %d finding(s) across %d detector(s)\n", total, len(reports))
	return nil
}
