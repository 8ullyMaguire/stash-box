package main

import (
	"context"
	"fmt"
	"net"
	"os"

	"github.com/spf13/pflag"
	"github.com/stashapp/stash-box/frontend"
	"github.com/stashapp/stash-box/internal/api"
	"github.com/stashapp/stash-box/internal/config"
	"github.com/stashapp/stash-box/internal/cron"
	"github.com/stashapp/stash-box/internal/database"
	"github.com/stashapp/stash-box/internal/email"
	"github.com/stashapp/stash-box/internal/image"
	"github.com/stashapp/stash-box/internal/models"
	"github.com/stashapp/stash-box/internal/service"
	"github.com/stashapp/stash-box/pkg/logger"
)

func main() {
	// Initialize flags
	pflag.IP("host", net.IPv4(0, 0, 0, 0), "ip address for the host")
	pflag.Int("port", 9998, "port to serve from")
	configFilePath := pflag.String("config_file", "", "location of the config file")
	pflag.Parse()

	// Initialize config
	initConfig(configFilePath)

	// Initialize logger
	logger.Init(config.GetLogFile(), config.GetUserLogFile(), config.GetLogOut(), config.GetLogLevel())

	cleanup := logger.InitTracer()
	//nolint:errcheck
	defer cleanup(context.Background())

	api.InitializeSession()

	// Create email manager
	emailMgr := email.NewManager()

	db := database.Initialize(config.GetDatabasePath())
	fac := service.NewFactory(db, emailMgr)
	fac.User().CreateSystemUsers(context.Background())
	initLintRegistry(fac)
	bootstrapAdminFromEnv(context.Background(), fac)
	api.Start(*fac, frontend.FS)
	cron.Init(*fac)

	if err := image.InitResizer(); err != nil {
		panic(err)
	}

	blockForever()
}

func blockForever() {
	c := make(chan struct{})
	<-c
}

// initLintRegistry seeds the data-lint detectors this build implements and refuses to start on a
// registry that names one it does not (SPEC §7.24.4, migration 98's stated contract: "the registry
// refuses it at startup instead").
//
// SEEDING AND VALIDATION ARE ONE FUNCTION ON PURPOSE. Seeding without validating would let an enabled
// row with no implementation sit there being skipped by the board, which reads exactly like a detector
// that found nothing -- the failure mode migration 98's comment is about. Validating without seeding
// would leave the registry empty on a fresh instance, so the board would be empty and every detector
// would look like it agreed the archive is clean.
//
// It FAILS THE BOOT, deliberately: an operator who has just added a row naming a detector this build
// does not ship needs to hear about it here, not from a board that quietly stopped listing one.
func initLintRegistry(fac *service.Factory) {
	ctx := context.Background()
	inserted, err := fac.Lint().EnsureBuiltins(ctx)
	if err != nil {
		fmt.Printf("The data-lint registry could not be seeded: %s\n", err)
		os.Exit(1)
	}
	if err := fac.Lint().Validate(ctx); err != nil {
		fmt.Printf("The data-lint registry disagrees with this build: %s\n", err)
		os.Exit(1)
	}
	if inserted > 0 {
		fmt.Printf("Seeded %d data-lint detector(s).\n", inserted)
	}
}

// bootstrapAdminFromEnv creates an ADMIN user from STASH_BOX_BOOTSTRAP_ADMIN_*
// env vars when running with is_production: false. Intended for E2E test setup
// and local automation only — it is a no-op in production builds, regardless of
// whether the env vars are set, so it is safe to leave the variables defined in
// a dev shell. Idempotent: if the user already exists, does nothing.
func bootstrapAdminFromEnv(ctx context.Context, fac *service.Factory) {
	username := os.Getenv("STASH_BOX_BOOTSTRAP_ADMIN_USERNAME")
	password := os.Getenv("STASH_BOX_BOOTSTRAP_ADMIN_PASSWORD")
	if username == "" || password == "" {
		return
	}

	if config.GetIsProduction() {
		logger.Warnf("STASH_BOX_BOOTSTRAP_ADMIN_* env vars set but is_production is true — refusing to bootstrap admin user")
		return
	}

	userSvc := fac.User()
	if existing, err := userSvc.FindByName(ctx, username); err == nil && existing != nil {
		return
	}

	emailAddr := os.Getenv("STASH_BOX_BOOTSTRAP_ADMIN_EMAIL")
	if emailAddr == "" {
		emailAddr = username + "@bootstrap.local"
	}

	if _, err := userSvc.Create(ctx, models.UserCreateInput{
		Name:     username,
		Password: password,
		Roles:    []models.RoleEnum{models.RoleEnumAdmin},
		Email:    emailAddr,
	}); err != nil {
		logger.Errorf("failed to bootstrap admin user %q: %v", username, err)
		return
	}
	logger.Infof("bootstrapped admin user %q from env vars", username)
}
