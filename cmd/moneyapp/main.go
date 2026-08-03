// Command moneyapp is the whole application: HTTP server, migration runner and a
// handful of operational commands, in one static binary.
//
// This file is wiring only. Logic lives in internal/.
package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/antlko/moneyapp/internal/config"
	"github.com/antlko/moneyapp/internal/domain/account"
	"github.com/antlko/moneyapp/internal/domain/auth"
	"github.com/antlko/moneyapp/internal/domain/budget"
	"github.com/antlko/moneyapp/internal/domain/capital"
	"github.com/antlko/moneyapp/internal/domain/category"
	"github.com/antlko/moneyapp/internal/domain/currency"
	"github.com/antlko/moneyapp/internal/domain/fx"
	"github.com/antlko/moneyapp/internal/domain/importer"
	"github.com/antlko/moneyapp/internal/domain/metrics"
	"github.com/antlko/moneyapp/internal/domain/period"
	"github.com/antlko/moneyapp/internal/domain/seed"
	"github.com/antlko/moneyapp/internal/domain/setting"
	telegramdomain "github.com/antlko/moneyapp/internal/domain/telegram"
	"github.com/antlko/moneyapp/internal/domain/transaction"
	"github.com/antlko/moneyapp/internal/platform/clock"
	"github.com/antlko/moneyapp/internal/platform/logging"
	"github.com/antlko/moneyapp/internal/provider"
	"github.com/antlko/moneyapp/internal/scheduler"
	"github.com/antlko/moneyapp/internal/store"
	"github.com/antlko/moneyapp/internal/transport/rest"
	"github.com/antlko/moneyapp/internal/transport/telegram"
)

// Set by -ldflags at build time.
var (
	version = "dev"
	commit  = "none"
	builtAt = ""
)

const defaultConfigPath = "/config/config.yaml"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "moneyapp: "+err.Error())
		os.Exit(1)
	}
}

func usage() string {
	return `moneyapp — self-hosted budget and net-worth tracker

Usage:
  moneyapp serve                        Run the HTTP server (default)
  moneyapp migrate up|down|status       Apply, roll back one, or report migrations
  moneyapp migrate down-all             Roll back to an empty schema (CI only)
  moneyapp backup now                   Take a verified, gzipped backup
  moneyapp backup list                  List retained backups
  moneyapp migrate-excel --file=F       Load the workbook's history (--dry-run first)
  moneyapp reset-password --email=E     Set a password from the terminal
  moneyapp healthcheck                  Probe the local server, for HEALTHCHECK
  moneyapp version                      Print build information

Flags:
  --config PATH                         Config file (default ` + defaultConfigPath + `)

Environment:
  MONEYAPP_CONFIG                       Config file path
  MONEYAPP_BOOTSTRAP_PASSWORD           First-run admin password
  MONEYAPP_*                            Per-key configuration overrides`
}

func run(args []string) error {
	command, rest := "serve", args
	if len(args) > 0 && !isFlag(args[0]) {
		command, rest = args[0], args[1:]
	}
	switch command {
	case "serve":
		return runServe(rest)
	case "migrate":
		return runMigrate(rest)
	case "backup":
		return runBackup(rest)
	case "migrate-excel":
		return runMigrateExcel(rest)
	case "reset-password":
		return runResetPassword(rest)
	case "healthcheck":
		return runHealthcheck(rest)
	case "version":
		fmt.Printf("moneyapp %s (%s) %s\n", version, commit, builtAt)
		return nil
	case "help", "-h", "--help":
		fmt.Println(usage())
		return nil
	default:
		return fmt.Errorf("unknown command %q\n\n%s", command, usage())
	}
}

func isFlag(s string) bool { return len(s) > 0 && s[0] == '-' }

// app is everything wired together, shared by the server and the CLI commands.
type app struct {
	cfg         config.Config
	log         *slog.Logger
	clock       clock.Clock
	db          *sql.DB
	deps        rest.Deps
	auth        *auth.Service
	settings    *setting.Service
	provisioner *seed.Provisioner
	links       *telegramdomain.Service
	backups     *store.Backup
	workbook    seed.WorkbookTarget
	offsets     telegramdomain.OffsetStore
	imports     importer.Service
	closer      func()
}

func setup(configPath string) (*app, error) {
	cfg, err := config.Load(configPath)
	if err != nil {
		return nil, err
	}
	log := logging.New(os.Stdout, logging.Options{
		Level: cfg.Log.Level, Format: cfg.Log.Format, MaskPII: cfg.Log.MaskPII,
	})
	clk := clock.Real()

	db, err := store.Open(cfg.Database)
	if err != nil {
		return nil, err
	}

	currencyRepo := store.NewCurrencyRepo(db)
	categoryRepo := store.NewCategoryRepo(db)
	accountRepo := store.NewAccountRepo(db)
	authRepo := store.NewAuthRepo(db)
	fxRepo := store.NewFxRepo(db)
	txnRepo := store.NewTransactionRepo(db)
	budgetRepo := store.NewBudgetRepo(db)
	idempotencyRepo := store.NewIdempotencyRepo(db)
	importRepo := store.NewImportRepo(db)
	metricsRepo := store.NewMetricsRepo(db)
	snapshotRepo := store.NewSnapshotRepo(db)
	settingRepo := store.NewSettingRepo(db)
	telegramRepo := store.NewTelegramRepo(db)

	currencies := currency.NewService(currencyRepo)
	categories := category.NewService(categoryRepo, clk)
	accounts := account.NewService(accountRepo, currencies, clk)
	fxSvc := fx.NewService(fxRepo, currencies)
	transactions := transaction.NewService(txnRepo, accounts, categories, currencies, fxSvc, clk)
	budgets := budget.NewService(budgetRepo, categories, currencies, clk)
	provisioner := seed.NewProvisioner(categoryRepo, accountRepo, settingRepo, clk)

	// The parser needs the currency exponents, which live in the database, so it
	// is built lazily on first use rather than at boot: migrations have not
	// necessarily run yet when setup() is called.
	imports := importer.NewService(
		importRepo,
		importer.NewDirFileStore(cfg.Imports.UploadDir),
		importer.MonefyFactory(currencies),
		categories, accounts, fxSvc, clk,
		cfg.Reports.BaseCurrency, cfg.Imports.MaxUploadBytes,
	)

	// The provider chain is built here because it is the outermost layer: the
	// domain declares what it needs and never reaches for HTTP itself.
	providerClient := provider.NewClient()
	refresher := fx.NewRefresher(fxSvc,
		[]fx.Provider{
			// Empty endpoints fall back to the constructors' verified defaults.
			provider.NewOpenErAPI(providerClient, ""),
			provider.NewFawazahmed0(providerClient, ""),
		},
		cfg.Providers.FX.PlausibilityMaxChange,
		fx.Quotes([]string{"USD", "HUF", "UAH"}),
	)
	settings := setting.NewService(settingRepo,
		fx.NewSettingFetcher(refresher, fxSvc, clk), settingRepo, clk)
	links := telegramdomain.NewService(telegramRepo, clk)

	authSvc, err := auth.NewService(authRepo, clk, cfg.Auth.BcryptCost, cfg.Auth.SessionTTL, provisioner)
	if err != nil {
		_ = db.Close()
		return nil, err
	}

	deps := rest.Deps{
		Config:       cfg,
		Log:          log,
		Clock:        clk,
		Build:        rest.BuildInfo{Version: version, Commit: commit, BuiltAt: builtAt},
		Readiness:    store.NewReadiness(db),
		Auth:         authSvc,
		Categories:   categories,
		Accounts:     accounts,
		Transactions: transactions,
		Budgets:      budgets,
		FX:           fxSvc,
		Currencies:   currencies,
		Imports:      imports,
		Metrics:      metrics.NewService(metricsRepo, categories),
		Capital:      capital.NewService(snapshotRepo, accounts, fxSvc, currencies, clk),
		Settings:     settings,
		Telegram:     links,
		Actuals:      txnRepo,
		Idempotency:  idempotencyRepo,
	}
	return &app{
		cfg: cfg, log: log, clock: clk, db: db, deps: deps, auth: authSvc,
		settings: settings, provisioner: provisioner,
		links: links, offsets: telegramRepo, imports: imports,
		backups: store.NewBackup(db, cfg.BackupDir(), cfg.Backup.Keep),
		workbook: seed.WorkbookTarget{
			Categories: categories, Accounts: accounts, Budgets: budgets,
			Capital:      capital.NewService(snapshotRepo, accounts, fxSvc, currencies, clk),
			Transactions: transactions,
			RecordedPeriods: func(ctx context.Context, userID int64) ([]period.Period, error) {
				return metricsRepo.RecordedPeriods(ctx, userID)
			},
			StoreRate: func(ctx context.Context, asOf time.Time, quote, rate string) error {
				parsed, err := fx.ParseRate(rate)
				if err != nil {
					return err
				}
				_, err = fxSvc.Upsert(ctx, fx.Rate{
					AsOf: asOf, Base: fx.StorageBase, Quote: quote,
					Rate: parsed, Source: "excel-import", FetchedAt: clk.Now(),
				})
				return err
			},
		},
		closer: func() { _ = db.Close() },
	}, nil
}

func runServe(args []string) error {
	configPath := configPathFrom(args)
	a, err := setup(configPath)
	if err != nil {
		return err
	}
	defer a.closer()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// The bootstrap admin is created once, on first boot. Migrations are NOT run
	// here: they are an explicit, human-triggered step
	// (docs/adr/0011-explicit-migrations.md).
	if err := bootstrap(ctx, a); err != nil {
		return err
	}

	// The scheduler is the only network caller in the process. It runs beside the
	// server rather than inside a request, so a slow provider can never be
	// something a user waits on (docs/adr/0009-dated-fx-provider-chain.md).
	jobs := scheduler.New(a.log, a.clock)
	jobs.Add(scheduler.Job{
		Name:      "fx-refresh",
		Interval:  a.cfg.Providers.FX.Refresh,
		Jitter:    30 * time.Minute,
		RunOnBoot: true,
		Run:       fx.RefreshJob(a.settings),
	})
	if a.cfg.Backup.Enabled {
		// A backup is worth more at a predictable hour than at whatever second
		// the process happened to start.
		jobs.Add(scheduler.Job{
			Name:       "backup",
			Interval:   24 * time.Hour,
			FirstDelay: scheduler.DailyAt(a.cfg.Backup.Schedule, a.clock.Now(), time.Hour),
			Jitter:     5 * time.Minute,
			Run: func(ctx context.Context) error {
				result, err := a.backups.Now(ctx, a.clock.Now())
				if err != nil {
					return err
				}
				a.log.Info("backup written",
					"path", result.Path, "bytes", result.Bytes, "pruned", len(result.Pruned))
				return nil
			},
		})
	}
	jobs.Start(ctx)

	// The bot is opt-in and runs beside the server under its own supervision, so
	// a crash in it cannot take the web server down with it
	// (docs/adr/0013-telegram-in-process.md).
	if a.cfg.Telegram.Enabled {
		worker := telegram.NewWorker(
			telegram.NewAPIClient(a.cfg.Telegram.Token),
			a.links, a.imports, a.offsets, a.log, a.clock,
			a.cfg.Server.BaseURL, a.cfg.Telegram.MaxFileBytes,
		)
		go worker.Run(ctx)
	} else {
		a.log.Info("telegram: disabled")
	}

	server := rest.New(a.deps)
	errCh := make(chan error, 1)
	go func() {
		a.log.Info("starting server",
			"addr", a.cfg.Server.Addr, "version", version, "commit", commit,
			"database", a.cfg.Database.Path, "telegram_enabled", a.cfg.Telegram.Enabled)
		if err := server.App().Listen(a.cfg.Server.Addr); err != nil {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return fmt.Errorf("server: %w", err)
	case <-ctx.Done():
		a.log.Info("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return server.App().ShutdownWithContext(shutdownCtx)
	}
}

func bootstrap(ctx context.Context, a *app) error {
	// A database that has not been migrated cannot be bootstrapped; /readyz will
	// report why, so this is a warning rather than a fatal error.
	version, err := store.SchemaVersion(ctx, a.db)
	if err != nil || version == 0 {
		a.log.Warn("database not migrated; run `moneyapp migrate up`",
			"schema_version", version, "expected", store.ExpectedSchemaVersion())
		return nil
	}
	result, err := a.auth.Bootstrap(ctx, a.cfg.Auth.BootstrapAdminEmail, a.cfg.Auth.BootstrapPassword)
	if err != nil {
		return fmt.Errorf("bootstrap: %w", err)
	}
	switch {
	case result.Created:
		a.log.Info("bootstrap admin created; the password must be changed at first login",
			"email", result.Email)
	case result.Skipped != "":
		a.log.Info("bootstrap skipped", "reason", result.Skipped)
	}

	// Settings are seeded at user creation, so an account that predates a stage
	// which added one would upgrade into an empty settings screen. Filling the
	// gaps is idempotent and leaves every existing value alone.
	users, err := a.auth.ListUsers(ctx)
	if err != nil {
		return fmt.Errorf("bootstrap: listing users: %w", err)
	}
	for _, u := range users {
		installed, err := a.provisioner.EnsureSettings(ctx, u.ID)
		if err != nil {
			return fmt.Errorf("bootstrap: settings for %s: %w", u.Email, err)
		}
		if installed > 0 {
			a.log.Info("installed missing settings", "email", u.Email, "count", installed)
		}
		// Additive only: an existing category is never touched, because the user
		// may have renamed or archived it on purpose.
		added, err := a.provisioner.EnsureCategories(ctx, u.ID)
		if err != nil {
			return fmt.Errorf("bootstrap: categories for %s: %w", u.Email, err)
		}
		if added > 0 {
			a.log.Info("installed missing categories", "email", u.Email, "count", added)
		}
	}
	return nil
}

func runMigrate(args []string) error {
	if len(args) == 0 {
		return errors.New("migrate needs a subcommand: up, down, down-all or status")
	}
	sub := args[0]
	a, err := setup(configPathFrom(args[1:]))
	if err != nil {
		return err
	}
	defer a.closer()

	ctx := context.Background()
	store.SetMigrationLogger(os.Stdout)

	// A schema change is the moment a copy is worth most, and the cheapest moment
	// to have one is before it. A first run has nothing to back up, which is why
	// an empty schema is exempt rather than fatal.
	if sub == "up" || sub == "down" || sub == "down-all" {
		if version, err := store.SchemaVersion(ctx, a.db); err == nil && version > 0 {
			result, err := a.backups.Now(ctx, a.clock.Now())
			if err != nil {
				return fmt.Errorf("refusing to migrate without a backup: %w", err)
			}
			fmt.Printf("backup: %s\n", result.Path)
		}
	}

	switch sub {
	case "up":
		if err := store.MigrateUp(ctx, a.db); err != nil {
			return err
		}
	case "down":
		if err := store.MigrateDown(ctx, a.db); err != nil {
			return err
		}
	case "down-all":
		if err := store.MigrateDownAll(ctx, a.db); err != nil {
			return err
		}
	case "status":
		if err := store.MigrateStatus(ctx, a.db); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unknown migrate subcommand %q", sub)
	}

	current, err := store.SchemaVersion(ctx, a.db)
	if err != nil {
		return err
	}
	fmt.Printf("schema version: %d (binary expects %d)\n", current, store.ExpectedSchemaVersion())
	return nil
}

func runResetPassword(args []string) error {
	email := flagValue(args, "email")
	password := flagValue(args, "password")
	if email == "" {
		return errors.New("reset-password needs --email=someone@example.com")
	}
	if password == "" {
		password = os.Getenv("MONEYAPP_NEW_PASSWORD")
	}
	if password == "" {
		// Reading from an environment variable or a flag keeps this scriptable
		// without an interactive terminal, which a container exec may not have.
		return errors.New("supply the new password with --password=... or MONEYAPP_NEW_PASSWORD")
	}
	a, err := setup(configPathFrom(args))
	if err != nil {
		return err
	}
	defer a.closer()

	if err := a.auth.ResetPassword(context.Background(), email, password); err != nil {
		return err
	}
	fmt.Printf("password reset for %s; every session was revoked and a change is required at next login\n", email)
	return nil
}

// runHealthcheck is what the container HEALTHCHECK invokes, so the image needs no
// curl and can stay distroless.
func runHealthcheck(args []string) error {
	addr := flagValue(args, "addr")
	if addr == "" {
		addr = os.Getenv("MONEYAPP_HEALTHCHECK_URL")
	}
	if addr == "" {
		addr = "http://127.0.0.1:8080/healthz"
	}
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(addr) //nolint:noctx // short-lived probe with its own timeout
	if err != nil {
		return fmt.Errorf("healthcheck: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("healthcheck: %s returned %d", addr, resp.StatusCode)
	}
	return nil
}

func configPathFrom(args []string) string {
	if v := flagValue(args, "config"); v != "" {
		return v
	}
	if v := os.Getenv("MONEYAPP_CONFIG"); v != "" {
		return v
	}
	return defaultConfigPath
}

// flagValue reads --name=value and --name value, which is all this CLI needs.
func flagValue(args []string, name string) string {
	long := "--" + name
	short := "-" + name
	for i, a := range args {
		switch {
		case a == long || a == short:
			if i+1 < len(args) && !isFlag(args[i+1]) {
				return args[i+1]
			}
		case len(a) > len(long)+1 && a[:len(long)+1] == long+"=":
			return a[len(long)+1:]
		case len(a) > len(short)+1 && a[:len(short)+1] == short+"=":
			return a[len(short)+1:]
		}
	}
	return ""
}

// runBackup is the operator's manual handle on the same machinery the scheduler
// uses nightly.
func runBackup(args []string) error {
	sub := "now"
	if len(args) > 0 && !isFlag(args[0]) {
		sub, args = args[0], args[1:]
	}
	a, err := setup(configPathFrom(args))
	if err != nil {
		return err
	}
	defer a.closer()

	switch sub {
	case "now":
		result, err := a.backups.Now(context.Background(), a.clock.Now())
		if err != nil {
			return err
		}
		fmt.Printf("wrote %s (%d bytes) in %s\n", result.Path, result.Bytes, result.Took.Round(time.Millisecond))
		for _, name := range result.Pruned {
			fmt.Printf("pruned %s\n", name)
		}
		return nil
	case "list":
		archives, err := a.backups.List()
		if err != nil {
			return err
		}
		if len(archives) == 0 {
			fmt.Println("no backups yet")
			return nil
		}
		for _, path := range archives {
			info, err := os.Stat(path)
			if err != nil {
				continue
			}
			fmt.Printf("%s  %8d bytes  %s\n", path, info.Size(),
				info.ModTime().UTC().Format(time.RFC3339))
		}
		return nil
	default:
		return fmt.Errorf("backup needs a subcommand: now or list")
	}
}

// runMigrateExcel loads the workbook's history.
//
// --dry-run prints what it would do and writes nothing. It is the default: a
// migration that touches real finances should have to be asked for twice.
func runMigrateExcel(args []string) error {
	file := "testdata/parity/workbook-2025-2026.json"
	email := ""
	commit := false
	for _, arg := range args {
		switch {
		case strings.HasPrefix(arg, "--file="):
			file = strings.TrimPrefix(arg, "--file=")
		case strings.HasPrefix(arg, "--email="):
			email = strings.TrimPrefix(arg, "--email=")
		case arg == "--commit":
			commit = true
		case arg == "--dry-run":
			commit = false
		}
	}

	a, err := setup(configPathFrom(args))
	if err != nil {
		return err
	}
	defer a.closer()

	ctx := context.Background()
	users, err := a.auth.ListUsers(ctx)
	if err != nil {
		return err
	}
	var target auth.User
	switch {
	case email != "":
		for _, u := range users {
			if strings.EqualFold(u.Email, email) {
				target = u
			}
		}
		if target.ID == 0 {
			return fmt.Errorf("no user with email %q", email)
		}
	case len(users) == 1:
		target = users[0]
	default:
		return fmt.Errorf("several users exist; name one with --email=")
	}

	wb, err := seed.LoadWorkbook(file)
	if err != nil {
		return err
	}

	// A backup before a migration that writes, for the same reason `migrate up`
	// takes one: the cheapest moment to have a copy is before the change.
	if commit {
		result, err := a.backups.Now(ctx, a.clock.Now())
		if err != nil {
			return fmt.Errorf("refusing to migrate without a backup: %w", err)
		}
		fmt.Printf("backup: %s\n", result.Path)
	}

	plan, err := a.migrateWorkbook(ctx, target.ID, wb, !commit)
	if err != nil {
		return err
	}

	mode := "would write"
	if commit {
		mode = "wrote"
	}
	fmt.Printf("%s: %s\n", file, wb.Source)
	fmt.Printf("%s %d budget rows, %d snapshots, %d aggregate expenses, %d income rows, %d rates\n",
		mode, plan.Budgets, plan.Snapshots, plan.Aggregates, plan.Income, plan.Rates)
	for _, note := range plan.Notes {
		fmt.Printf("note: %s\n", note)
	}
	if !commit {
		fmt.Println("nothing was written. re-run with --commit to apply.")
	}
	return nil
}

// migrateWorkbook is the wiring seam: the command owns the flags, the domain owns
// what a migration means.
func (a *app) migrateWorkbook(
	ctx context.Context, userID int64, wb seed.Workbook, dryRun bool,
) (seed.MigrationPlan, error) {
	return seed.MigrateWorkbook(ctx, userID, wb, a.workbook, dryRun)
}
