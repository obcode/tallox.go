package bootstrap

import (
	"errors"
	"flag"
	"fmt"
	"net/url"
	"strings"

	"github.com/spf13/viper"
)

// ConfigName is the base name of the configuration file, without extension. Searched in the
// working directory and in $HOME, which is where a container mount and a developer's home
// respectively put it.
const ConfigName = "tallox"

// Config is everything tallox.yaml can say.
//
// Every field here is read by something. That is a rule rather than an observation: a key
// that a file documents and the program ignores is worse than no key at all, because somebody
// eventually sets it, restarts, and believes the restart did what the comment promised. The
// blocks that are planned but not wired — SMTP — are commented out in
// deploy/tallox.yaml.example instead of being declared here, so the shape stays documented
// without the file being able to lie.
//
// The corollary is UnmarshalExact below: a key this struct does not know is a startup
// failure, not a shrug. A typo in a production configuration file has to be loud on the
// restart that introduces it, and the alternative — silently keeping the default — is a
// mode where the server runs and the operator is wrong about what it is doing.
//
// That exactness makes a configuration key forward-only, exactly like a migration, and it is
// worth spelling out because the recovery for everything else here is a rollback. Deploy the
// image that reads a new key before the key appears in the file; roll back past it and the
// file the operator has already edited stops the older image from starting. The failure is
// also deferred — it lands on the next restart, not on the edit — so the person who caused it
// is not necessarily the person who sees it.
type Config struct {
	Server ServerConfig
	Auth   AuthConfig
	ZPA    ZPAConfig
	Log    LogConfig
}

// ServerConfig is the HTTP surface.
type ServerConfig struct {
	// Port is what the server listens on. A number rather than an address, because in the
	// configuration file the interface is not something worth varying: the container
	// publishes nothing and Caddy reaches it by service name.
	//
	// The -addr flag still takes a full address and wins over this — see listen.
	Port int
	// listen is the full address -addr asked for, empty when nobody asked.
	//
	// Unexported, so it exists only as a flag override and never as a configuration key: two
	// ways to say where the server listens is one too many, and the file is the one that
	// should read well. viper cannot set it, so `server.listen` in a YAML file is an unknown
	// key and fails the start, which is the right answer.
	listen string
	// Playground serves the built-in GraphQL playground at "/". Off in production — the
	// explorer colleagues use is /api-doku in tallox.gui, and exactly one explorer is the
	// point.
	Playground bool
	// Introspection stays on, in production too. It is not a user interface: it is what makes
	// editor completion, codegen and schema exploration work for the colleagues writing their
	// own evaluations. See CLAUDE.md — the API is a product here.
	Introspection bool
}

// AuthConfig configures both doors, and the way back in when the database says otherwise.
type AuthConfig struct {
	// Mode is dev, proxy or off-token. Validated by auth.ParseMode.
	Mode string
	// DevUser is the mail address of the injected development user. Only read in dev mode.
	DevUser string
	// ProtectedAdmins is the list of people who must be able to administer this installation,
	// whatever the database currently says.
	//
	// Reconciled at every start, additively: a listed person is created if missing,
	// reactivated if deactivated, and granted ADMIN if they lack it. Nobody is ever demoted
	// by this list — editing the file is not a way to revoke anything, because a list that
	// could revoke would turn a careless edit into a silent mass demotion.
	//
	// It is the answer to the failure mode this project actually has: one administrator
	// removing another by accident, on an installation that is only reachable through a VPN
	// and whose only other repair is psql on the host.
	ProtectedAdmins []ProtectedAdmin
}

// ProtectedAdmin is one entry of that list.
type ProtectedAdmin struct {
	// Mail is the identity the proxy asserts in X-Remote-User — the same string that would be
	// typed into the user administration, not a local login name.
	Mail string
	// Name is what the interface shows. Optional: an empty name is filled in with the mail
	// address, exactly as it is for a person created through the GUI before the ZPA import
	// has run.
	Name string
}

// ZPAConfig points at the central examination office's REST interface, the source of the
// module master data. Read-only, in one direction: SPOs are maintained there and stay there.
//
// Both values or neither. There is deliberately no `enabled` switch, and the argument is the
// one auth.mode=off-token already makes about not mounting a route: an emergency stop that
// removes the thing itself leaves no code path that could be wrong about whether it is
// engaged. `enabled: false` next to a filled-in token is exactly such a code path — the
// credential is still there and a single condition stands between it and the other system.
// Emptying Token removes the credential, and then there is nothing to authenticate with.
type ZPAConfig struct {
	// BaseURL is the root of the interface, without a trailing slash and without the /rest
	// path. Deliberately without a default: this repository is public, and the address of an
	// internal system is operational detail. It lives in deploy/tallox.yaml.example, which is
	// not.
	BaseURL string
	// Token authenticates every request. The scheme is `Authorization: Token <value>`, not
	// Bearer — the interface is Django REST Framework, which answers a missing credential with
	// `WWW-Authenticate: Token`. Getting this wrong costs half an hour, because the wrong
	// scheme and no scheme at all produce the same refusal.
	//
	// A secret, and therefore in this file rather than in the environment: TALLOX_DB_URL is in
	// the environment because it differs between the DevContainer, CI and the host and because
	// a deploy script sets it. This one does not vary that way.
	//
	// Never logged, not even truncated. Startup reports whether one is configured, not what it
	// is.
	Token string
}

// Configured reports whether the ZPA import can run at all.
//
// The three states are deliberately not two. Nothing configured is the ordinary case for every
// DevContainer, every CI run and every fresh checkout, so it is a fact to log and not a
// failure. Both values configured is the production case. Exactly one of them is neither: it
// is somebody who expressed an intention the program will not honour, and Validate refuses the
// start for it — the same asymmetry auth.protectedadmins already has, where an empty list is a
// warning and a malformed entry is an error.
func (c ZPAConfig) Configured() bool {
	return c.BaseURL != "" && c.Token != ""
}

// Validate checks the block for the mistakes that would otherwise surface as a puzzling
// refusal from the other system rather than as a configuration error here.
func (c ZPAConfig) Validate() error {
	baseURL := strings.TrimSpace(c.BaseURL)
	token := strings.TrimSpace(c.Token)

	switch {
	case baseURL == "" && token == "":
		return nil
	case baseURL == "":
		return errors.New("zpa.token is set but zpa.baseurl is not")
	case token == "":
		return errors.New("zpa.baseurl is set but zpa.token is not")
	}

	u, err := url.Parse(baseURL)
	if err != nil {
		return fmt.Errorf("zpa.baseurl %q is not a URL: %w", baseURL, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("zpa.baseurl %q needs an http or https scheme", baseURL)
	}
	if u.Host == "" {
		return fmt.Errorf("zpa.baseurl %q has no host", baseURL)
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("zpa.baseurl %q must be a bare address, without a query or fragment", baseURL)
	}
	return nil
}

// Normalised returns the block with its whitespace trimmed and its base address free of a
// trailing slash.
//
// The slash matters more than it looks. Joining a path onto a base that ends in one produces
// a doubled separator, which most reverse proxies answer with a redirect — and the client
// deliberately does not follow redirects, so the symptom would be a refusal from a URL nobody
// typed. Trimming once here is cheaper than diagnosing that.
func (c ZPAConfig) Normalised() ZPAConfig {
	return ZPAConfig{
		BaseURL: strings.TrimRight(strings.TrimSpace(c.BaseURL), "/"),
		Token:   strings.TrimSpace(c.Token),
	}
}

// LogConfig is the logging level, as a word rather than a boolean pair.
type LogConfig struct {
	// Level is debug, info, warn or error.
	Level string
}

// DefaultConfig is what a server with no configuration file runs as.
//
// It is production, deliberately, and it is the same set of values the flags default to. A
// server that falls back to something more convenient when nobody configured it is a server
// that hands out an administrator on the day somebody forgets a file.
func DefaultConfig() Config {
	return Config{
		Server: ServerConfig{
			Port:          8080,
			Playground:    true,
			Introspection: true,
		},
		Auth: AuthConfig{
			Mode:    "proxy",
			DevUser: "",
		},
		Log: LogConfig{
			Level: "info",
		},
	}
}

// LoadConfig reads tallox.yaml, falling back to DefaultConfig where it is silent.
//
// An explicit path that does not exist is an error: somebody asked for that file. A missing
// file at the searched locations is not — the development loop runs entirely on flags, and a
// server that refuses to start without a configuration file would make the first `go run .`
// of every new checkout a support question.
//
// Returns the path that was read, or the empty string when none was, so that the startup log
// can say which file the running configuration came from. "Which tallox.yaml is this
// container actually using" is a question asked while standing in front of production.
func LoadConfig(explicitPath string) (Config, string, error) {
	return LoadConfigFrom(explicitPath, ".", "$HOME")
}

// LoadConfigFrom is LoadConfig with the search path spelled out.
//
// Exported for the tests, which need to point the search somewhere they control. The
// alternative — chdir into a temporary directory — reaches outside the test that does it: the
// working directory is process-wide, every test in this package runs in parallel, and
// TestTheDevLoopAsksForDevMode reads ../gowatch.yml relative to it. That test went red for a
// reason that had nothing to do with it, which is the worst kind of flake to be handed.
func LoadConfigFrom(explicitPath string, searchPaths ...string) (Config, string, error) {
	v := viper.New()

	if explicitPath != "" {
		// An explicit path is taken at its word. The type comes from the extension; a file
		// named without one is refused rather than guessed at.
		v.SetConfigFile(explicitPath)
	} else {
		// Deliberately no SetConfigType here, and it is not a detail.
		//
		// viper only considers an *extensionless* file matching the config name when a type
		// has been set. In production the working directory is /app, the configuration is
		// mounted at /app/tallox.yaml — and the server binary is /app/tallox. With a type
		// set, viper finds the binary first and tries to parse 22 MB of ELF as YAML, so the
		// container dies on the first start that has a configuration file at all.
		//
		// The same trap is in the repository root, where `go build` leaves ./tallox next to
		// the source.
		v.SetConfigName(ConfigName)
		for _, path := range searchPaths {
			v.AddConfigPath(path)
		}
	}

	cfg := DefaultConfig()

	if err := v.ReadInConfig(); err != nil {
		var notFound viper.ConfigFileNotFoundError
		if errors.As(err, &notFound) && explicitPath == "" {
			return cfg, "", nil
		}
		return Config{}, "", fmt.Errorf("cannot read the configuration file: %w", err)
	}

	// UnmarshalExact, not Unmarshal: an unknown key fails the start. See the Config doc.
	//
	// It unmarshals into the defaults rather than into a zero value, so a file that mentions
	// only auth.protectedadmins leaves everything else at the documented default instead of
	// silently setting the port to 0 and the mode to the empty string.
	if err := v.UnmarshalExact(&cfg); err != nil {
		return Config{}, "", fmt.Errorf("cannot read %s: %w", v.ConfigFileUsed(), err)
	}

	// Validated here rather than at the point of use, so that a malformed address fails the
	// start instead of the first nightly import — which would be discovered a day later, by
	// nobody, because a job that never ran sends no mail.
	if err := cfg.ZPA.Validate(); err != nil {
		return Config{}, "", fmt.Errorf("cannot read %s: %w", v.ConfigFileUsed(), err)
	}
	cfg.ZPA = cfg.ZPA.Normalised()

	return cfg, v.ConfigFileUsed(), nil
}

// FlagOverrides are the flag values that can override the file, and the names under which
// they were declared.
//
// A struct rather than a long parameter list, for the reason Options is one: this grows with
// every knob, and two adjacent strings that mean different things are a bug waiting for a
// hurried afternoon.
type FlagOverrides struct {
	Addr       string
	Playground bool
	AuthMode   string
	DevUser    string
	Verbose    bool
}

// ApplyFlagOverrides lets explicitly-set flags win over the configuration file.
//
// The distinction that makes this correct is `set` — the names of the flags the caller
// actually typed, from flag.Visit. Without it there is no way to tell `-playground=false`
// from "the flag defaulted to false", so a flag default would silently override every value
// in the file and the file would be decorative. This is the usual way a configuration layer
// is wrong, and it is invisible until somebody sets a value that happens to equal a default.
//
// Pure, and exported for the test that asserts precedence in both directions.
func ApplyFlagOverrides(cfg Config, set map[string]bool, f FlagOverrides) (Config, error) {
	if set["addr"] {
		if err := validateAddr(f.Addr); err != nil {
			return Config{}, err
		}
		cfg.Server.listen = f.Addr
	}
	if set["playground"] {
		cfg.Server.Playground = f.Playground
	}
	if set["auth-mode"] {
		cfg.Auth.Mode = f.AuthMode
	}
	if set["auth-dev-user"] {
		cfg.Auth.DevUser = f.DevUser
	}
	if set["v"] && f.Verbose {
		// -v is a switch, not a level: it means "show me everything" and there is no
		// `-v=false` that could mean "be quieter than the file says".
		cfg.Log.Level = "debug"
	}
	return cfg, nil
}

// validateAddr checks that -addr is something net/http can listen on.
//
// The host part is kept, not refused. An earlier version rejected it on the grounds that this
// server always listens on all interfaces and is reached through a reverse proxy — which is
// true of production and of nothing else. `-addr 127.0.0.1:8080` is what the end-to-end
// workflow uses to keep its throwaway backend off the runner's network, and that is a
// perfectly good reason to name a host. Refusing it broke a job that had worked for weeks.
//
// The check that remains is the one that catches a typo before the listener does: an address
// with no port at all, or with one nothing can bind.
func validateAddr(addr string) error {
	_, port, found := strings.Cut(addr, ":")
	if !found {
		return fmt.Errorf("-addr %q has no port", addr)
	}
	var n int
	if _, err := fmt.Sscanf(port, "%d", &n); err != nil || n <= 0 || n > 65535 {
		return fmt.Errorf("-addr %q has no usable port", addr)
	}
	return nil
}

// FlagsSet reports which flags were given on the command line.
//
// flag.Visit walks only the flags that were actually set, which is the whole basis of the
// precedence rule above.
func FlagsSet() map[string]bool {
	set := make(map[string]bool)
	flag.Visit(func(f *flag.Flag) { set[f.Name] = true })
	return set
}

// Addr renders the listen address the HTTP server binds.
//
// The -addr flag wins whole, rather than being reduced to its port: it is the only way to say
// "listen on loopback only", and that is a thing tests and one-off runs legitimately want.
func (c ServerConfig) Addr() string {
	if c.listen != "" {
		return c.listen
	}
	return fmt.Sprintf(":%d", c.Port)
}
