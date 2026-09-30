// Command sdbimport imports records from a stash-box-compatible source
// (stashdb.org by default) into this instance through the service layer.
//
// Usage:
//
//	sdbimport -config <path> [-entities tags,sites,studios,performers,scenes]
//	           [-limit-performer N] [-limit-scene N] [-dry-run] [-delay D]
//
// The source credential is read from SDBSRC_USER / SDBSRC_PASS in the
// environment. It is never accepted as a flag: a flag lands in the shell
// history and in `ps` output, and this is a real account password.
//
// ORDER IS FIXED and not configurable: tags, sites, studios, performers, then
// scenes. Scenes reference the other four, and the destination assigns its own
// ids, so a scene cannot be written before its dependencies are resolved.
//
// RE-RUN SAFETY IS PARTIAL, and the difference is not accidental. Tags, sites,
// studios and performers are deduplicated on their natural key, so a re-run
// resumes safely. Scenes are NOT deduplicated: the only available key is
// title+date, and using it would silently drop two genuinely distinct scenes
// that share both. Re-running with "scenes" in -entities therefore duplicates
// them. Use -entities to re-run just the entities you need.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/viper"

	"github.com/stashapp/stash-box/internal/config"
	"github.com/stashapp/stash-box/internal/database"
	"github.com/stashapp/stash-box/internal/queries"
	"github.com/stashapp/stash-box/internal/service"

	"github.com/stashapp/stash-box/internal/sdbimport"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "sdbimport: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		configPath = flag.String("config", "", "path to the destination stash-box config file (required)")
		entities   = flag.String("entities", "tags,sites,studios,performers,scenes", "comma-separated entity list")
		limitP     = flag.Int("limit-performer", 0, "import at most N performers (0 = all)")
		limitS     = flag.Int("limit-scene", 0, "import at most N scenes (0 = all)")
		dryRun     = flag.Bool("dry-run", false, "read and validate from the source, write nothing")
		delay      = flag.Duration("delay", 0, "pause between source pages")
	)
	flag.Parse()

	if *configPath == "" {
		return errors.New("-config is required")
	}

	// Credentials come from the environment, never a flag. See the package doc.
	user := os.Getenv("SDBSRC_USER")
	pass := os.Getenv("SDBSRC_PASS")
	if user == "" || pass == "" {
		return errors.New("SDBSRC_USER and SDBSRC_PASS must be set in the environment")
	}

	// Ctrl-C stops between records rather than mid-write, so an interrupted run
	// leaves a consistent database.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Same wiring as cmd/stash-box: viper reads the config file into the global
	// config, then the pool is opened (which also runs migrations).
	// Config wiring, and the ORDER IS LOAD-BEARING.
	//
	// ReadInConfig() must come FIRST. config.InitializeDefaults() ends in
	// viper.WriteConfig(), so calling it before the file has been read makes
	// viper write its *defaults* over the real config -- silently replacing the
	// DSN, the JWT key and the session key with defaults. That is not a crash
	// and not a warning: the process starts happily against the wrong database
	// and the real config file is destroyed on disk.
	//
	// It happened during development of this tool and cost a live deployment its
	// config, so the ordering is called out here rather than left to look
	// equivalent to cmd/stash-box/init.go (which reads first and only calls
	// InitializeDefaults on a NEW config file).
	viper.SetConfigFile(*configPath)
	if err := viper.ReadInConfig(); err != nil {
		return fmt.Errorf("reading config %s: %w", *configPath, err)
	}
	if err := config.Initialize(); err != nil {
		return fmt.Errorf("loading destination config %s: %w", *configPath, err)
	}

	db := database.Initialize(config.GetDatabasePath())
	defer db.Close()

	imp := sdbimport.NewImporter(
		sdbimport.NewClient(sourceEndpoint()),
		service.NewFactory(db, nil),
		queries.New(db),
	)
	imp.Ctx = ctx
	imp.DryRun = *dryRun
	imp.Delay = *delay

	client := sdbimport.NewClient(sourceEndpoint())
	fmt.Println("authenticating to source...")
	if err := client.Authenticate(ctx, sourceLoginURL(), user, pass); err != nil {
		return fmt.Errorf("source authentication: %w", err)
	}
	imp.Client = client

	// Read the source totals up front so the run states its own denominator
	// instead of leaving progress to be inferred from the destination.
	totals, err := sourceTotals(ctx, client)
	if err != nil {
		return err
	}
	fmt.Printf("source totals: %d performers, %d scenes, %d studios, %d tags, %d sites\n",
		totals["performers"], totals["scenes"], totals["studios"], totals["tags"], totals["sites"])
	if *dryRun {
		fmt.Println("DRY RUN: nothing will be written")
	}

	want := map[string]bool{}
	for _, e := range strings.Split(*entities, ",") {
		if e = strings.TrimSpace(e); e != "" {
			want[e] = true
		}
	}

	r := imp.NewResolver()
	start := time.Now()

	for _, step := range []struct {
		name string
		fn   func(*sdbimport.Resolver) error
	}{
		{"tags", func(r *sdbimport.Resolver) error { return imp.ImportTags(r) }},
		{"sites", func(r *sdbimport.Resolver) error { return imp.ImportSites(r) }},
		{"studios", func(r *sdbimport.Resolver) error { return imp.ImportStudios(r) }},
		{"performers", func(r *sdbimport.Resolver) error { return imp.ImportPerformers(r, *limitP) }},
		{"scenes", func(r *sdbimport.Resolver) error { return imp.ImportScenes(r, *limitS) }},
	} {
		if !want[step.name] {
			continue
		}
		if err := step.fn(r); err != nil {
			if errors.Is(err, context.Canceled) {
				fmt.Println("\ninterrupted; stopping between entities")
				break
			}
			return fmt.Errorf("importing %s: %w", step.name, err)
		}
		report(step.name, imp.Stats[step.name], time.Since(start))
	}

	fmt.Printf("\ntotal elapsed %s\n", time.Since(start).Round(time.Second))
	return nil
}

// sourceTotals reads every entity's count from the source.
func sourceTotals(ctx context.Context, c *sdbimport.Client) (map[string]int, error) {
	out := map[string]int{}
	for _, q := range []struct {
		key  string
		verb string
	}{
		{"performers", sdbimport.CountPerformers},
		{"scenes", sdbimport.CountScenes},
		{"studios", sdbimport.CountStudios},
		{"tags", sdbimport.CountTags},
	} {
		var resp map[string]struct {
			Count int `json:"count"`
		}
		// Each count query has a different root field, so the response is read
		// generically and the single value taken.
		if err := c.Query(ctx, q.verb, nil, &resp); err != nil {
			return nil, fmt.Errorf("counting %s: %w", q.key, err)
		}
		for _, v := range resp {
			out[q.key] = v.Count
		}
	}

	var sites struct {
		QuerySites struct {
			Count int `json:"count"`
		} `json:"querySites"`
	}
	if err := c.Query(ctx, sdbimport.SiteCountQuery, nil, &sites); err != nil {
		return nil, fmt.Errorf("counting sites: %w", err)
	}
	out["sites"] = sites.QuerySites.Count
	return out, nil
}

// report prints one entity's outcome, including what was DROPPED.
//
// The drop list is the point. An import that reports "111,705 performers
// imported" while silently discarding every URL and every unrecognised enum
// value has not succeeded; it has produced a plausible-looking instance missing
// data, and the drop counts are the only thing that distinguishes the two.
func report(entity string, s *sdbimport.Stats, elapsed time.Duration) {
	if s == nil {
		return
	}
	fmt.Printf("\n== %s ==\n", entity)
	fmt.Printf("  created %d  skipped %d  failed %d   (%s)\n",
		s.Created, s.Skipped, s.Failed, elapsed.Round(time.Millisecond))

	if len(s.Dropped) > 0 {
		keys := make([]string, 0, len(s.Dropped))
		total := 0
		for k, n := range s.Dropped {
			keys = append(keys, k)
			total += n
		}
		sort.Slice(keys, func(a, b int) bool { return s.Dropped[keys[a]] > s.Dropped[keys[b]] })
		fmt.Printf("  DROPPED %d values across %d categories:\n", total, len(keys))
		for i, k := range keys {
			if i == 20 {
				fmt.Printf("    ... and %d more categories\n", len(keys)-20)
				break
			}
			fmt.Printf("    %-46s %d\n", k, s.Dropped[k])
		}
	}
	if len(s.FirstErr) > 0 {
		keys := make([]string, 0, len(s.FirstErr))
		for k := range s.FirstErr {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		fmt.Println("  first error per category:")
		for _, k := range keys {
			fmt.Printf("    %-18s %s\n", k, truncate(s.FirstErr[k], 120))
		}
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func sourceEndpoint() string {
	if v := os.Getenv("SDBSRC_ENDPOINT"); v != "" {
		return v
	}
	return "https://stashdb.org/graphql"
}

func sourceLoginURL() string {
	if v := os.Getenv("SDBSRC_LOGIN"); v != "" {
		return v
	}
	return "https://stashdb.org/login"
}
