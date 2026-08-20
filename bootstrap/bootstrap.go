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
	"github.com/obcode/tallox.go/internal/store"
	"github.com/obcode/tallox.go/internal/zpa"
)

// EnvDatabaseURL is where the connection string comes from.
//
// The environment rather than the configuration file, because it is the one value that
// differs between the DevContainer, CI and the host, and because it is the value a deploy
// script sets. Secrets that are not per-environment belong in tallox.yaml.
const EnvDatabaseURL = "TALLOX_DB_URL"

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
		// Where the configuration file is. Empty means "look for tallox.yaml in . and $HOME",
		// which is what the container and the development loop both want.
		configPath = flag.String("config", "",
			"path to the configuration file (default: tallox.yaml in . or $HOME)")
	)
	flag.Parse()

	cfg, configFile, err := LoadConfig(*configPath)
	if err != nil {
		// Before setupLogging, so this uses the zerolog default writer rather than the
		// configured one. That is the right way round: the thing that failed is the source of
		// the logging configuration.
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

	setupLogging(cfg.Log.Level)
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
		reportConfiguration(cfg, configFile)
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

	defer pool.Close()

	directory := store.NewDirectory(pool)
	tokens := domain.NewTokenService(store.NewTokens(pool), nil)
	people := domain.NewPeopleService(store.NewPeople(pool), nil)
	planning := domain.NewSemesterService(store.NewSemesters(pool), nil)

	zpaCache := store.NewZPA(pool)
	imports := domain.NewZPASyncService(zpaCache, zpaSource, store.NewZPALock(pool))

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
			Tokens:   tokens,
			People:   people,
			Planning: planning,
			Import:   imports,
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
func reportConfiguration(cfg Config, configFile string) {
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

	r.With(auth.Middleware(auth.NewProxyAuthenticator(opts.Auth))).Handle("/query", gql)

	if opts.Auth.Mode.TokenDoorEnabled() {
		r.With(auth.Middleware(auth.NewTokenAuthenticator(opts.Auth))).Handle("/api/graphql", gql)
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
			Build:    opts.Build,
			Tokens:   opts.Tokens,
			People:   opts.People,
			Planning: opts.Planning,
			Import:   opts.Import,
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
func setupLogging(level string) {
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
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stdout}).
		With().Caller().Timestamp().Logger()
}
