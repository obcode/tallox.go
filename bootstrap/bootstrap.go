// Package bootstrap is the server entrypoint: flags, configuration, wiring, HTTP router and
// graceful shutdown. It is the only package allowed to call log.Fatal.
package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/handler/extension"
	"github.com/99designs/gqlgen/graphql/handler/lru"
	"github.com/99designs/gqlgen/graphql/handler/transport"
	"github.com/99designs/gqlgen/graphql/playground"
	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/vektah/gqlparser/v2/ast"

	"github.com/obcode/tallox.go/graph"
	"github.com/obcode/tallox.go/graph/generated"
	"github.com/obcode/tallox.go/internal/auth"
	"github.com/obcode/tallox.go/internal/buildinfo"
	"github.com/obcode/tallox.go/internal/domain"
	"github.com/obcode/tallox.go/internal/obs"
	"github.com/obcode/tallox.go/internal/principal"
	"github.com/obcode/tallox.go/internal/store"
	"github.com/obcode/tallox.go/internal/zpa"
)

// EnvDatabaseURL is where the connection string comes from.
//
// The environment rather than the configuration file, because it is the one value that
// differs between the DevContainer, CI and the host, and because it is the value a deploy
// script sets. Secrets that are not per-environment belong in tallox.yaml.
const EnvDatabaseURL = "TALLOX_DB_URL"

// EnvSentryDSN turns error reporting on; empty or unset means none at all, which is what
// the DevContainer and CI want.
//
// The environment rather than tallox.yaml, like EnvDatabaseURL and for the same reason — it
// differs per installation — and additionally because reporting starts BEFORE the
// configuration file is read. A file that will not load is the most interesting startup
// failure there is, and it cannot report itself if the reporter is configured inside it.
//
// SENTRY_*, not GLITCHTIP_*: it is the name the SDK itself uses, and the name plexams.go
// already carries. The collector happens to be GlitchTip; the protocol and the variable
// are Sentry's, and one name across both installations is worth more than naming the
// product we currently point at.
const EnvSentryDSN = "SENTRY_DSN"

// EnvSentryEnvironment separates production from a test installation in the collector's
// UI. Defaults to production: an installation nobody labelled is the one that matters.
const EnvSentryEnvironment = "SENTRY_ENVIRONMENT"

// Options is everything Handler needs. A struct rather than a parameter list: this grows with
// every subsystem, and a positional bool that means "playground" in one call site and
// something else in the next is a bug waiting for a hurried afternoon.
type Options struct {
	// Build is the version stamp, served by /healthz and by the buildInfo query.
	Build buildinfo.Info
	// Playground enables the GraphQL playground at "/".
	Playground bool
	// DisableIntrospection turns schema introspection off.
	//
	// Negated on purpose, so that the zero value leaves it on. Introspection is on by
	// default here, in production too — the API is a product, and introspection is what makes
	// editor completion, codegen and schema exploration work for colleagues writing their own
	// evaluations. A field whose zero value silently contradicted the documented default
	// would turn every future `Options{…}` literal in a test into a different server from the
	// one that ships.
	DisableIntrospection bool
	// Auth configures both doors: the mode, and the two lookups.
	Auth auth.Config
	// Tokens is token management. Nil is legitimate for the tests that never reach a token
	// field; a request that does reach one then fails loudly rather than answering wrongly.
	Tokens *domain.TokenService
	// People is user administration. Nil like Tokens: legitimate for a test that never
	// reaches one of its fields.
	People *domain.PeopleService
	// Planning is the semester workflow, on the same terms.
	Planning *domain.SemesterService
	// Import is the module master data import. Nil in tests that do not exercise it.
	Import *domain.ZPASyncService
	// Catalogue is the module catalogue, on the same terms.
	Catalogue *domain.CatalogueService
	// Demand is the demand planning, on the same terms.
	Demand *domain.DemandService
	// SubjectGroups is the faculty's own grouping of modules and people, on the same terms.
	SubjectGroups *domain.SubjectGroupService
	// Wishes is the wish phase, on the same terms.
	Wishes *domain.WishService
	// Staffing is the assignment phase, on the same terms. Named as it is in graph.Resolver, for
	// the collision explained there.
	Staffing *domain.AssignmentService
	// Marks is what opens and closes the planning, on the same terms.
	Marks *domain.PlanningMarkService
	// Access is the access log. Nil is legitimate and means the installation does not record
	// accesses — which is what every test that does not care about the log runs as, and what
	// keeps a missing log from being a missing server.
	Access *domain.AccessService
}

// Serve parses flags, sets up logging and runs the HTTP server until a signal arrives.
func Serve(build buildinfo.Info) {
	var (
		verbose = flag.Bool("v", false, "verbose (debug) logging")
		addr    = flag.String("addr", ":8080", "listen address")
		// The playground is a development convenience, not a public surface: in production
		// Caddy routes only /query and /api/graphql to this container, so "/" is not
		// reachable from outside anyway. Introspection is a separate matter and stays on —
		// see CLAUDE.md, the API is a product here.
		playgroundEnabled = flag.Bool("playground", true, "serve the GraphQL playground at /")
		// The default is production. A server that falls back to dev mode when nobody
		// configured it is a server that hands out an administrator on the day somebody
		// forgets a flag.
		authMode = flag.String("auth-mode", string(auth.ModeProxy),
			"how to authenticate: dev | proxy | off-token")
		devUser = flag.String("auth-dev-user", auth.DefaultDevUser,
			"mail address of the injected development user (auth-mode=dev only)")
		// Read-only, and it exits instead of serving. The question it answers — "what happens
		// to the schema if I start this image" — is asked while standing in front of a
		// production database, which is exactly when a command must not be able to change the
		// answer it is reporting.
		migrateStatus = flag.Bool("migrate-status", false,
			"report which migrations are applied and pending, then exit")
		// Also read-only, and deliberately not folded into -migrate-status even though the two
		// look alike. That one needs a reachable database; this one must work when the database
		// is the thing that is down, because the question it answers — "will this image start
		// with this file" — is asked before swapping an image, and a new configuration key is
		// forward-only. See the Config doc.
		checkConfig = flag.Bool("check-config", false,
			"read the configuration file, report what it configures, then exit")
		// The nightly import, as a separate process rather than a timer inside the server.
		//
		// There is exactly one precedent for scheduled work in this installation — the backup
		// script in the host's crontab — and this is a line beside it. It keeps the request and
		// shutdown paths untouched, makes the schedule editable without a redeploy, turns a
		// failure into an exit code and a log line the operator already reads, and gives the
		// emergency stop the right shape: comment the line out and no timer exists in the
		// running process that could be wrong about whether it is engaged.
		zpaSync = flag.Bool("zpa-sync", false,
			"fetch the module master data from the ZPA and apply it, then exit")
		// The tool for the first run against an unknown catalogue, and for "did the token stop
		// working last night". Fetches and diffs, writes nothing at all.
		dryRun = flag.Bool("dry-run", false,
			"with -zpa-sync: fetch and report what would change, without writing")
		// The nightly access report, on the same terms and in the same crontab.
		//
		// It also prunes: the retention period is only real if something enforces it, and a
		// second cron line for the pruning is a second line somebody can forget to add on a new
		// host — with the failure mode of keeping a year of colleagues' movements while everyone
		// believes it keeps ninety days.
		accessReport = flag.Bool("access-report", false,
			"summarise the access log, prune what has expired, then exit")
		// The window the report covers. A flag rather than a constant because the answer to
		// "what happened while I was away" is a longer window run by hand, and because the
		// nightly line should be able to say what it means.
		since = flag.Duration("since", 24*time.Hour,
			"with -access-report: how far back to summarise")
		// Where the configuration file is. Empty means "look for tallox.yaml in . and $HOME",
		// which is what the container and the development loop both want.
		configPath = flag.String("config", "",
			"path to the configuration file (default: tallox.yaml in . or $HOME)")
	)
	flag.Parse()

	// Call sites as repository-relative paths — see repoRelativeCaller.
	zerolog.CallerMarshalFunc = obs.RepoRelativeCaller

	reporter, flushReports := setupReporting(build)
	defer flushReports()

	cfg, configFile, err := LoadConfig(*configPath)
	if err != nil {
		// Before setupLogging, so this uses the zerolog default writer rather than the
		// configured one. That is the right way round: the thing that failed is the source of
		// the logging configuration. setupReporting has already attached the reporter to that
		// default writer, so this line is reported as well as printed.
		//
		// gocritic is right that the deferred flush above will not run — log.Fatal exits.
		// It does not have to: the reporter flushes synchronously at Fatal level, precisely
		// because os.Exit would otherwise drop the one event that mattered most.
		//nolint:gocritic // exitAfterDefer: the reporter flushes itself on Fatal
		log.Fatal().Err(err).Msg("cannot start")
	}
	cfg, err = ApplyFlagOverrides(cfg, FlagsSet(), FlagOverrides{
		Addr:       *addr,
		Playground: *playgroundEnabled,
		AuthMode:   *authMode,
		DevUser:    *devUser,
		Verbose:    *verbose,
	})
	if err != nil {
		log.Fatal().Err(err).Msg("cannot start")
	}

	setupLogging(cfg.Log.Level, reporter)
	if configFile != "" {
		log.Info().Str("file", configFile).Msg("configuration loaded")
	} else {
		log.Info().Msg("no configuration file found — flags and defaults only")
	}
	logZPAConfiguration(cfg.ZPA)
	log.Info().
		Str("version", build.Version).
		Str("commit", build.Commit).
		Str("builtAt", build.BuiltAt).
		Msg("tallox starting")

	// Before the database url is even looked at. Getting here at all means the file parsed and
	// validated, which is the whole of what this flag asserts — and it has to be assertable on
	// an installation whose database is exactly what is broken.
	if *checkConfig {
		reportConfiguration(cfg, configFile, reporter != nil)
		return
	}

	dsn := os.Getenv(EnvDatabaseURL)
	if dsn == "" {
		log.Fatal().Str("variable", EnvDatabaseURL).
			Msg("no database url — the server cannot authenticate anybody without one")
	}

	ctx := context.Background()

	// Before anything else, and before the auth mode is even validated: this path talks to
	// the database and exits, so a wrong -auth-mode is not a reason to refuse to answer a
	// question about the schema.
	if *migrateStatus {
		reportMigrationStatus(ctx, dsn)
		return
	}

	// Also before the auth mode is validated, and for the same reason: this path serves nobody.
	//
	// It deliberately does NOT migrate. The serving container owns the schema, and a cron job
	// that could apply a migration would mean the schema changes at 02:30 on whatever image the
	// crontab happens to invoke, rather than on a deploy somebody is watching.
	if *zpaSync {
		runZPASync(ctx, cfg, dsn, *dryRun)
		return
	}

	// Beside -zpa-sync, before the auth mode is validated, and for the same reason: this path
	// serves nobody, and it must be able to answer on an installation whose auth configuration
	// is exactly what somebody is in the middle of repairing.
	if *accessReport {
		runAccessReport(ctx, dsn, *since)
		return
	}

	mode, err := auth.ParseMode(cfg.Auth.Mode)
	if err != nil {
		log.Fatal().Err(err).Msg("cannot start")
	}

	// Migrate before opening the pool that serves requests. Embedded migrations plus "apply at
	// startup" means a container that has the binary has the schema, by construction: there is
	// no deploy step that can copy one and forget the other.
	applied, err := store.MigrateUpDSN(ctx, dsn)
	if err != nil {
		log.Fatal().Err(err).Msg("cannot migrate the database")
	}
	log.Info().Int("applied", applied).Msg("migrations up to date")

	// Before the pool, so that a failure here may still use log.Fatal — after `defer
	// pool.Close()` is installed it would skip the close. Nothing about the client needs the
	// database anyway; it is built entirely from the configuration.
	//
	// The import service exists even when the ZPA is not configured: reading the runs has to
	// work so that the page can say "not configured", and a nil source is what makes starting
	// a run refuse rather than panic.
	var zpaSource domain.ZPASource
	if cfg.ZPA.Configured() {
		client, err := zpa.New(zpa.Config{BaseURL: cfg.ZPA.BaseURL, Token: cfg.ZPA.Token})
		if err != nil {
			log.Fatal().Err(err).Msg("cannot build the zpa client")
		}
		zpaSource = client
	}

	pool, err := store.Open(ctx, dsn)
	if err != nil {
		log.Fatal().Err(err).Msg("cannot reach the database")
	}

	// The reconciliation runs before `defer pool.Close()` is installed, so that its failure
	// path may use log.Fatal like every other startup step. Afterwards the pool lives for as
	// long as the process does.
	//
	// After the migrations, before serving. It is what makes a fresh database usable at all —
	// nobody can sign in and nobody can be given a token until somebody is in the person table
	// — and it is the way back in after an administrator has been removed by accident. See
	// store.ReconcileProtectedAdmins.
	reconcileProtectedAdmins(ctx, pool, cfg.Auth.ProtectedAdmins)
	reconcileDevelopmentUser(ctx, pool, mode, cfg.Auth.DevUser)

	defer pool.Close()

	directory := store.NewDirectory(pool)
	tokens := domain.NewTokenService(store.NewTokens(pool), nil)
	people := domain.NewPeopleService(store.NewPeople(pool), nil)
	planning := domain.NewSemesterService(store.NewSemesters(pool), nil)

	zpaCache := store.NewZPA(pool)
	catalogueProjection := store.NewCatalogue(pool)
	modules := store.NewModules(pool)
	catalogue := domain.NewCatalogueService(modules)
	demand := domain.NewDemandService(store.NewDemand(pool, modules), modules, planning)
	subjectGroups := domain.NewSubjectGroupService(store.NewSubjectGroups(pool))
	wishes := domain.NewWishService(store.NewWishes(pool), planning)
	staffing := domain.NewAssignmentService(store.NewAssignments(pool), planning)
	marks := domain.NewPlanningMarkService(store.NewPlanningMarks(pool), planning)
	imports := domain.NewZPASyncService(zpaCache, zpaSource, store.NewZPALock(pool), catalogueProjection)
	access := domain.NewAccessService(store.NewAccess(pool))

	// Beside reconcileProtectedAdmins, and for a related reason: a process that died mid-sync
	// leaves a RUNNING row that the interface would show as a run in progress forever. Not
	// fatal — a stale row is untidy, not dangerous.
	if failed, err := zpaCache.FailAbandonedRuns(ctx, abandonedRunAge); err != nil {
		log.Warn().Err(err).Msg("cannot clear abandoned zpa sync runs")
	} else if failed > 0 {
		log.Warn().Int("runs", failed).Msg("marked abandoned zpa sync runs as failed")
	}

	srv := &http.Server{
		Addr: cfg.Server.Addr(),
		Handler: Handler(Options{
			Build:                build,
			Playground:           cfg.Server.Playground,
			DisableIntrospection: !cfg.Server.Introspection,
			Auth: auth.Config{
				Mode:    mode,
				Users:   directory,
				Tokens:  directory,
				DevUser: cfg.Auth.DevUser,
			},
			Tokens:        tokens,
			People:        people,
			Planning:      planning,
			Import:        imports,
			Catalogue:     catalogue,
			Demand:        demand,
			SubjectGroups: subjectGroups,
			Wishes:        wishes,
			Staffing:      staffing,
			Marks:         marks,
			Access:        access,
		}),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal().Err(err).Str("addr", cfg.Server.Addr()).Msg("cannot listen")
		}
	}()
	log.Info().Str("addr", cfg.Server.Addr()).Str("authMode", string(mode)).Msg("listening")

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	log.Info().Msg("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error().Err(err).Msg("cannot shut down cleanly")
	}
}

// reconcileProtectedAdmins runs the list from tallox.yaml and reports what it changed.
//
// A failure here is fatal, deliberately. The list is the thing that says who can get into
// this installation, and a server that starts having failed to apply it looks healthy while
// being in exactly the state the list exists to prevent. Refusing to start is the loud
// version of the same problem, and it is the one somebody notices.
//
// An empty list is not a failure and not a warning. Every start after the first, on an
// installation whose administrators are all in the database, is allowed to be quiet — but a
// list that does nothing is worth saying once, because "I put my address in the file" and
// "the file the container reads" are two different things more often than one would like.
func reconcileProtectedAdmins(ctx context.Context, pool store.Pool, admins []ProtectedAdmin) {
	if len(admins) == 0 {
		log.Warn().Msg("no protected administrators configured — if the last ADMIN is ever " +
			"removed, the only way back in is psql on the host. See auth.protectedadmins.")
		return
	}

	entries := make([]store.ProtectedAdmin, 0, len(admins))
	for _, a := range admins {
		entries = append(entries, store.ProtectedAdmin{Mail: a.Mail, Name: a.Name})
	}

	outcomes, err := store.ReconcileProtectedAdmins(ctx, pool, entries)
	if err != nil {
		log.Fatal().Err(err).Msg("cannot reconcile the protected administrators")
	}

	for _, o := range outcomes {
		if !o.Changed() {
			continue
		}
		// At Warn, with the address in it. Somebody now holds ADMIN who was not granted it by
		// a human — which is the whole purpose of the mechanism and precisely the reason it
		// has to leave a trace that is visible in an ordinary production log.
		log.Warn().
			Str("mail", o.Mail).
			Bool("created", o.Created).
			Bool("reactivated", o.Reactivated).
			Bool("granted", o.Granted).
			Msg("protected administrator restored from the configuration file")
	}
}

// reconcileDevelopmentUser gives auth.mode=dev's synthetic user a person row.
//
// The development door hands out an identity nobody logged in as, with an id derived from its
// address so that it is stable across restarts. That was enough while nothing referenced the
// actor: the roles are synthesised too, and the policy asks about roles.
//
// It stopped being enough the moment a write recorded who performed it. `created_by` is a
// foreign key to person, and an actor that exists in the request and not in the database fails
// it — so the first thing a developer tried after the catalogue landed was refused with a
// generic error, in a mode whose entire purpose is that the ordinary path works.
//
// The row alone, deliberately: no roles are granted. Which roles the development user has stays
// a property of the mode (every one of them, see auth.developmentActor) rather than of a row
// somebody could edit into a different answer — that would make the dev door quietly diverge
// from what it says it does.
//
// Not fatal. A development database that cannot take this row is broken in a way the next
// query will report more clearly than a startup abort would.
func reconcileDevelopmentUser(ctx context.Context, pool store.Pool, mode auth.Mode, mail string) {
	if mode != auth.ModeDev {
		return
	}

	address := auth.DevUserMail(mail)

	id, err := store.EnsureDevelopmentUser(ctx, pool, auth.DevUserID(mail), address,
		auth.DevUserName)
	if err != nil {
		log.Warn().Err(err).Msg("cannot give the development user a person row — writes that " +
			"record who performed them will fail")
		return
	}
	if id != auth.DevUserID(mail) {
		// Somebody created this address by hand with a different id. The synthetic actor still
		// carries the derived one, so the foreign key will still refuse — and saying so here is
		// better than the generic refusal it turns into three screens later.
		log.Warn().
			Str("mail", address).
			Msg("the development user's address belongs to a person row with a different id; " +
				"remove that row or run with a real identity")
	}
}

// logZPAConfiguration says once, at startup, whether the module import can run.
//
// The address goes into the line and the token never does — not truncated, not as a prefix.
// A Bool is the whole useful answer to "did the file reach the container", and a fragment of a
// credential in a log is a credential in a log.
//
// Info rather than Warn when it is absent: no ZPA is the ordinary state of every DevContainer
// and every CI run, and a warning that fires on every ordinary start is one people learn to
// scroll past — which is a bad habit to teach on the same screen where the protected-admin
// warning has to be noticed.
func logZPAConfiguration(cfg ZPAConfig) {
	if !cfg.Configured() {
		log.Info().Msg("zpa module import not configured")
		return
	}
	log.Info().Str("baseURL", cfg.BaseURL).Bool("token", true).Msg("zpa module import configured")
}

// reportConfiguration prints which file was read and which subsystems it configures.
//
// To stdout, for the same reason reportMigrationStatus is: somebody reads this in a terminal
// after `docker compose run --rm`, and a timestamp and a level in front of every line make it
// harder to read for no gain.
//
// It reports what is configured, never the values that are secret. The point is to answer
// "will the new image start with this file, and did it see the block I added" — both of which
// are answered by getting here at all plus these three lines.
func reportConfiguration(cfg Config, configFile string, reporting bool) {
	if configFile == "" {
		fmt.Println("configuration file: none found (flags and defaults only)")
	} else {
		fmt.Printf("configuration file: %s\n", configFile)
	}
	fmt.Printf("auth:               mode=%s, %d protected administrator(s)\n",
		cfg.Auth.Mode, len(cfg.Auth.ProtectedAdmins))
	if cfg.ZPA.Configured() {
		fmt.Printf("zpa module import:  configured (%s)\n", cfg.ZPA.BaseURL)
	} else {
		fmt.Println("zpa module import:  not configured")
	}
	// From the environment, not from the file this command is about — but somebody checking
	// what this image will do wants to know whether its errors will be seen by anyone. The
	// state comes from setupReporting rather than from the variable, because a DSN that is
	// set but unusable means reporting is off, and reporting "on" there would be a lie told
	// by the one command whose job is to answer this question.
	switch {
	case reporting:
		fmt.Printf("error reporting:    on (%s)\n", environmentOrDefault())
	case os.Getenv(EnvSentryDSN) != "":
		fmt.Printf("error reporting:    off (%s is set but was rejected)\n", EnvSentryDSN)
	default:
		fmt.Printf("error reporting:    off (%s is unset)\n", EnvSentryDSN)
	}
}

func environmentOrDefault() string {
	if e := os.Getenv(EnvSentryEnvironment); e != "" {
		return e
	}
	return "production"
}

// reportMigrationStatus prints what the database has and what this binary is carrying, then
// returns so that Serve can exit.
//
// Written to stdout rather than through zerolog: this is output somebody reads in a terminal
// after `docker compose exec`, and a log line with a timestamp, a level and a caller in front
// of it is harder to read for no gain. Failures still go through the logger, because a
// failure here is an operational event.
func reportMigrationStatus(ctx context.Context, dsn string) {
	status, err := store.StatusDSN(ctx, dsn)
	if err != nil {
		log.Fatal().Err(err).Msg("cannot read the migration status")
	}
	fmt.Println(status)
}

// Handler builds the same http.Handler that Serve mounts, without listening on a port.
//
// Exported for tests, and deliberately the *same* function Serve uses rather than a
// test-only reassembly of the routes. A harness that wires its own router proves that the
// harness is correct; this one proves that the server is. Every rule that lands in the
// middleware chain is therefore exercised by the integration tests automatically, including
// the ones nobody remembered to write a test for.
func Handler(opts Options) http.Handler {
	return router(opts)
}

func router(opts Options) http.Handler {
	r := chi.NewRouter()

	// Liveness for the container healthcheck and the deploy workflow. Deliberately outside
	// any auth: it must answer before the database or the auth proxy are reachable.
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{
			"status":  "ok",
			"version": opts.Build.Version,
		})
	})

	// ONE handler on both doors. /query gets the identity from the auth proxy's
	// X-Remote-User, /api/graphql from a bearer token — but they share a schema, a resolver
	// root and an AroundOperations chain, so a rule added for the browser cannot be missing
	// on the token path. What differs is the authenticating middleware, and only that.
	gql := graphqlHandler(opts)

	refusals := refusalRecorder(opts.Access)

	r.With(auth.Middleware(auth.NewProxyAuthenticator(opts.Auth), refusals)).Handle("/query", gql)

	if opts.Auth.Mode.TokenDoorEnabled() {
		r.With(auth.Middleware(auth.NewTokenAuthenticator(opts.Auth), refusals)).
			Handle("/api/graphql", gql)
	} else {
		// Not mounted at all rather than mounted and refusing. The emergency stop has to
		// leave no code path that could be wrong about whether it is engaged, and a 404 is
		// also the honest answer: on this instance, that door does not exist.
		log.Warn().Msg("auth.mode=off-token: /api/graphql is not served")
	}

	if opts.Playground {
		r.Handle("/", playground.Handler("Tallox", "/query"))
	}

	return r
}

// graphqlHandler builds the gqlgen server. Transports are listed explicitly rather than
// taken from NewDefaultServer: the default set includes websockets, and a subscription
// transport that nobody has thought about is an auth path that nobody has thought about.
func graphqlHandler(opts Options) http.Handler {
	srv := handler.New(generated.NewExecutableSchema(generated.Config{
		Resolvers: &graph.Resolver{
			Build:         opts.Build,
			Tokens:        opts.Tokens,
			People:        opts.People,
			Planning:      opts.Planning,
			Import:        opts.Import,
			Catalogue:     opts.Catalogue,
			Demand:        opts.Demand,
			SubjectGroups: opts.SubjectGroups,
			Wishes:        opts.Wishes,
			Staffing:      opts.Staffing,
			Marks:         opts.Marks,
			Access:        opts.Access,
		},
		// The generated code fails closed on a directive with no implementation — the field
		// errors with "directive interactiveOnly is not implemented" rather than passing
		// through. So forgetting this line breaks token management loudly instead of
		// serving it to scripts, which is the direction one wants to be wrong in.
		Directives: graph.Directives(),
	}))

	srv.AddTransport(transport.Options{})
	srv.AddTransport(transport.GET{})
	srv.AddTransport(transport.POST{})

	// The scope check, once per operation, on the one handler both doors share. Unlike
	// @interactiveOnly this is not a generated directive call — @scope is skip_runtime and read
	// out of the field definitions here — so forgetting this line would not break loudly. What
	// catches it instead is bootstrap/scope_test.go, which drives the assembled handler rather
	// than a hand-wired one, and TestEveryRootFieldDeclaresAScope, which keeps the annotations
	// this reads from going missing.
	// The access log, registered BEFORE the scope check and therefore wrapped around it.
	//
	// That order is the whole reason a refused operation appears in the log at all:
	// EnforceScopes answers with graphql.OneShot instead of calling through, and only a
	// middleware outside it sees that response. The other way round, the log would hold every
	// operation that was allowed and no record of the ones that were not — backwards for an
	// audit trail. bootstrap/access_test.go pins it.
	if opts.Access != nil {
		srv.AroundOperations(graph.RecordAccess(opts.Access))
	}

	srv.AroundOperations(graph.EnforceScopes)

	srv.SetQueryCache(lru.New[*ast.QueryDocument](1000))

	// Introspection stays on, in production too. The API is a product: it is what makes
	// editor completion, codegen and schema exploration work for colleagues writing their
	// own evaluations against a Personal Access Token. The switch exists because the
	// configuration file documents one, not because it is expected to be used.
	if !opts.DisableIntrospection {
		srv.Use(extension.Introspection{})
	}
	srv.Use(extension.AutomaticPersistedQuery{Cache: lru.New[string](100)})

	return srv
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Error().Err(err).Msg("cannot write response")
	}
}

// setupLogging applies the configured level.
//
// An unrecognised level falls back to info and says so, rather than refusing to start. Every
// other configuration mistake in this file is fatal — see LoadConfig — but this one is not, on
// purpose: the failure mode of a typo in the logging level is that the logs are the wrong
// verbosity, and refusing to serve the faculty's planning tool over that is a worse outcome
// than the problem. The warning is what makes it noticed, and it is legible precisely because
// the fallback keeps the log running.
func setupLogging(level string, reporter zerolog.LevelWriter) {
	zerolog.TimeFieldFormat = zerolog.TimeFormatUnix

	parsed, err := zerolog.ParseLevel(level)
	if err != nil || parsed == zerolog.NoLevel {
		parsed = zerolog.InfoLevel
		defer func() {
			log.Warn().Str("configured", level).
				Msg("unknown log level, using info")
		}()
	}
	zerolog.SetGlobalLevel(parsed)

	// The reporter has to be carried over from setupReporting: this replaces the writer, and
	// without it every error from here on would only be printed.
	console := zerolog.ConsoleWriter{Out: os.Stdout}
	out := zerolog.MultiLevelWriter(console)
	if reporter != nil {
		out = zerolog.MultiLevelWriter(console, reporter)
	}
	// .Caller() is not cosmetic: the reporter groups issues by that field.
	log.Logger = zerolog.New(out).With().Caller().Timestamp().Logger()
}

// setupReporting starts error reporting from the environment and returns the writer for
// setupLogging to keep, plus a flush to defer.
//
// Everything that decides what may leave this host lives in internal/obs — read
// internal/obs/scrub.go before adding anything that reports. Both are safe when reporting is off: the
// writer is nil and the flush does nothing.
//
// It runs before the configuration is read, and therefore attaches the reporter to zerolog's
// default logger straight away — otherwise the one log.Fatal() that can happen before
// setupLogging, a configuration file that will not load, would be the single startup failure
// that never reaches the collector.
//
// A collector that will not start is a reason to run unmonitored, not a reason to refuse to
// serve the faculty's planning tool — the same trade the log level makes above.
func setupReporting(build buildinfo.Info) (zerolog.LevelWriter, func()) {
	dsn := os.Getenv(EnvSentryDSN)
	if dsn == "" {
		return nil, func() {}
	}

	environment := os.Getenv(EnvSentryEnvironment)
	if environment == "" {
		environment = "production"
	}

	reporter, err := obs.Init(obs.Config{
		DSN:         dsn,
		Environment: environment,
		Release:     build.Version,
		// Deliberately not wired to the configuration file: this runs before
		// LoadConfig, which is the whole point of it. Filling the ignore list needs
		// a deploy here, and it needs a week of real traffic before it means
		// anything anyway.
		IgnoreErrors: nil,
	})
	if err != nil {
		log.Warn().Err(err).Msg("error reporting is off")
		return nil, func() {}
	}
	if reporter == nil {
		return nil, func() {}
	}

	log.Logger = log.Output(zerolog.MultiLevelWriter(os.Stderr, reporter)).
		With().Caller().Logger()

	return reporter, obs.Flush
}

// refusalRecorder adapts the access log to what internal/auth needs to record a refused
// sign-in.
//
// The adapter lives here because this is the only package that may know both vocabularies:
// internal/auth authenticates and is deliberately below the layer that knows what an access
// log entry is, and internal/domain does not know what a door is called in an HTTP middleware.
//
// A nil service yields a nil recorder rather than one that discards: auth checks for nil, and
// "there is no recorder" should be one state rather than two that behave alike.
func refusalRecorder(access *domain.AccessService) auth.AccessRecorder {
	if access == nil {
		return nil
	}
	return refusalAdapter{access: access}
}

type refusalAdapter struct {
	access *domain.AccessService
}

func (a refusalAdapter) RecordRefusal(ctx context.Context, r auth.Refusal) error {
	door := domain.AccessDoorInteractive
	if r.Door == principal.KindToken {
		door = domain.AccessDoorToken
	}
	// No actor id and no roles: there is no person row, which is the whole point of the entry.
	// The table's CHECK constraint says the same thing, so a future version of this function
	// that filled either in would fail loudly rather than quietly recording a fiction.
	return a.access.Record(ctx, domain.AccessRecord{
		ActorMail: r.Mail,
		Door:      door,
		TokenID:   r.TokenID,
		Outcome:   domain.AccessRefusedAuth,
		ErrorCode: r.Code,
		SourceIP:  r.Source,
	})
}
