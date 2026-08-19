package bootstrap_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/obcode/tallox.go/bootstrap"
)

// TestNoConfigFileIsNotAnError.
//
// The development loop runs entirely on flags, and `go run .` in a fresh checkout has no
// tallox.yaml anywhere. A server that refused to start without one would make the first run of
// every new clone a support question.
func TestNoConfigFileIsNotAnError(t *testing.T) {
	t.Parallel()

	cfg, file, err := bootstrap.LoadConfigFrom("", t.TempDir())
	if err != nil {
		t.Fatalf("loading without a file failed: %v", err)
	}
	if file != "" {
		t.Errorf("reported reading %q, want none", file)
	}
	if cfg.Auth.Mode != "proxy" {
		t.Errorf("auth mode defaulted to %q, want proxy — a server that falls back to "+
			"something more convenient when nobody configured it hands out an administrator "+
			"on the day somebody forgets a file", cfg.Auth.Mode)
	}
}

// TestTheServerBinaryIsNotMistakenForItsConfiguration.
//
// In production the working directory is /app, the configuration is mounted at
// /app/tallox.yaml — and the server binary is /app/tallox. viper considers an extensionless
// file matching the config name whenever a config type has been set, so setting one made the
// container find the binary first and try to parse 22 MB of ELF as YAML. It died on the first
// start that had a configuration file at all, with "control characters are not allowed".
//
// The same file sits in the repository root after `go build`, which is how this was found.
func TestTheServerBinaryIsNotMistakenForItsConfiguration(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	// Something binary-looking, named exactly like the configuration but without a suffix.
	if err := os.WriteFile(filepath.Join(dir, bootstrap.ConfigName),
		[]byte{0x7f, 'E', 'L', 'F', 0x02, 0x01, 0x01, 0x00}, 0o755); err != nil {
		t.Fatalf("cannot write the decoy: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, bootstrap.ConfigName+".yaml"),
		[]byte("log:\n  level: warn\n"), 0o600); err != nil {
		t.Fatalf("cannot write the configuration: %v", err)
	}

	cfg, file, err := bootstrap.LoadConfigFrom("", dir)
	if err != nil {
		t.Fatalf("loading failed: %v — the binary was parsed as YAML", err)
	}
	if filepath.Base(file) != bootstrap.ConfigName+".yaml" {
		t.Fatalf("read %q, want the .yaml file", file)
	}
	if cfg.Log.Level != "warn" {
		t.Errorf("log level is %q, want the value from the file", cfg.Log.Level)
	}
}

// TestAnExplicitPathThatDoesNotExistIsAnError: somebody asked for that file. Silently running
// on defaults would be a container that ignores its mount and looks healthy doing it.
func TestAnExplicitPathThatDoesNotExistIsAnError(t *testing.T) {
	t.Parallel()

	if _, _, err := bootstrap.LoadConfig(filepath.Join(t.TempDir(), "nope.yaml")); err == nil {
		t.Error("a missing explicit configuration file was accepted")
	}
}

// TestAnUnknownKeyIsAStartupFailure.
//
// The alternative is the mode this whole exercise exists to end: a file that documents a
// setting, a program that ignores it, and an operator who is wrong about what their server is
// doing. A typo in production has to be loud on the restart that introduces it.
func TestAnUnknownKeyIsAStartupFailure(t *testing.T) {
	t.Parallel()

	path := writeConfig(t, `
auth:
  protectedadmin:
    - mail: admin@example.org
`)

	_, _, err := bootstrap.LoadConfig(path)
	if err == nil {
		t.Fatal("a misspelled key was accepted — the protected administrators would silently " +
			"be empty, and nobody would find out until the day they are needed")
	}
	if !strings.Contains(err.Error(), "protectedadmin") {
		t.Errorf("the error does not name the offending key: %v", err)
	}
}

// TestAFileFillsInOnlyWhatItMentions: a file that sets one thing must not zero everything else.
func TestAFileFillsInOnlyWhatItMentions(t *testing.T) {
	t.Parallel()

	path := writeConfig(t, `
auth:
  protectedadmins:
    - mail: admin@example.org
      name: Admin
`)

	cfg, _, err := bootstrap.LoadConfig(path)
	if err != nil {
		t.Fatalf("loading failed: %v", err)
	}

	if len(cfg.Auth.ProtectedAdmins) != 1 || cfg.Auth.ProtectedAdmins[0].Mail != "admin@example.org" {
		t.Fatalf("protected admins are %+v", cfg.Auth.ProtectedAdmins)
	}
	if cfg.Server.Port != 8080 {
		t.Errorf("port is %d, want the default 8080 rather than a zero", cfg.Server.Port)
	}
	if cfg.Auth.Mode != "proxy" {
		t.Errorf("auth mode is %q, want the default", cfg.Auth.Mode)
	}
	if !cfg.Server.Introspection {
		t.Error("introspection defaulted to off — the API is a product here, and " +
			"introspection is what makes editor completion and codegen work for colleagues")
	}
}

// TestOnlyFlagsThatWereActuallySetOverrideTheFile is the whole basis of the precedence rule,
// and the usual way a configuration layer is quietly wrong.
//
// Without the "was it set" distinction there is no way to tell `-playground=false` from "the
// flag defaulted to false", so every flag default would override the file and the file would
// be decorative. It stays invisible until somebody sets a value that happens to equal a
// default.
func TestOnlyFlagsThatWereActuallySetOverrideTheFile(t *testing.T) {
	t.Parallel()

	fromFile := bootstrap.Config{
		Server: bootstrap.ServerConfig{Port: 9999, Playground: false, Introspection: true},
		Auth:   bootstrap.AuthConfig{Mode: "proxy", DevUser: "from-file@example.org"},
		Log:    bootstrap.LogConfig{Level: "warn"},
	}
	flags := bootstrap.FlagOverrides{
		Addr:       ":8080",
		Playground: true,
		AuthMode:   "dev",
		DevUser:    "from-flag@example.org",
		Verbose:    true,
	}

	t.Run("no flag set: the file wins entirely", func(t *testing.T) {
		got, err := bootstrap.ApplyFlagOverrides(fromFile, map[string]bool{}, flags)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.Server != fromFile.Server || got.Log != fromFile.Log ||
			got.Auth.Mode != fromFile.Auth.Mode || got.Auth.DevUser != fromFile.Auth.DevUser {
			t.Errorf("flag defaults leaked into the configuration:\n got %+v\nwant %+v",
				got, fromFile)
		}
	})

	t.Run("one flag set: only that one wins", func(t *testing.T) {
		got, err := bootstrap.ApplyFlagOverrides(fromFile, map[string]bool{"auth-mode": true}, flags)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.Auth.Mode != "dev" {
			t.Errorf("auth mode is %q, want the flag to win", got.Auth.Mode)
		}
		if got.Server.Port != 9999 {
			t.Errorf("port is %d, want the file's value untouched", got.Server.Port)
		}
		if got.Auth.DevUser != "from-file@example.org" {
			t.Errorf("dev user is %q, want the file's value untouched", got.Auth.DevUser)
		}
	})

	t.Run("-v raises the level but never lowers it", func(t *testing.T) {
		quiet := flags
		quiet.Verbose = false
		got, err := bootstrap.ApplyFlagOverrides(fromFile, map[string]bool{"v": true}, quiet)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.Log.Level != "warn" {
			t.Errorf("log level is %q — -v is a switch meaning \"show me everything\", and "+
				"there is no -v=false that could mean \"be quieter than the file says\"",
				got.Log.Level)
		}
	})
}

// TestAddrOverridesThePort keeps the flag working against a file that speaks in ports.
func TestAddrOverridesThePort(t *testing.T) {
	t.Parallel()

	base := bootstrap.DefaultConfig() // port 8080

	got, err := bootstrap.ApplyFlagOverrides(base, map[string]bool{"addr": true},
		bootstrap.FlagOverrides{Addr: ":9000"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Server.Addr() != ":9000" {
		t.Errorf("listen address is %q, want :9000", got.Server.Addr())
	}
}

// TestAddrKeepsAHostPart is a regression test with a name attached to it.
//
// An earlier version of this refused any -addr that named a host, reasoning that the server
// always listens on all interfaces and is reached through a reverse proxy. That is true of
// production and of nothing else: tallox.gui's end-to-end workflow starts a throwaway backend
// with `-addr 127.0.0.1:8080` to keep it off the runner's network, and the refusal broke a job
// that had worked for weeks — with a message explaining, confidently, why the thing it was
// doing was wrong.
//
// "Listens on all interfaces" is a property of one deployment, not of the flag.
func TestAddrKeepsAHostPart(t *testing.T) {
	t.Parallel()

	got, err := bootstrap.ApplyFlagOverrides(bootstrap.DefaultConfig(),
		map[string]bool{"addr": true}, bootstrap.FlagOverrides{Addr: "127.0.0.1:8080"})
	if err != nil {
		t.Fatalf("-addr with a host was refused: %v", err)
	}
	if got.Server.Addr() != "127.0.0.1:8080" {
		t.Errorf("listen address is %q, want the flag verbatim — dropping the host would make "+
			"the flag mean something other than what it says", got.Server.Addr())
	}
}

// TestAddrWithoutAUsablePortIsRefused: the check that is left is the one that catches a typo
// before the listener does, and says so at startup rather than in a stack trace.
func TestAddrWithoutAUsablePortIsRefused(t *testing.T) {
	t.Parallel()

	for _, addr := range []string{"8080", ":0", ":notaport", ":99999", ""} {
		if _, err := bootstrap.ApplyFlagOverrides(bootstrap.DefaultConfig(),
			map[string]bool{"addr": true}, bootstrap.FlagOverrides{Addr: addr}); err == nil {
			t.Errorf("-addr %q was accepted", addr)
		}
	}
}

// writeConfig puts a configuration file in a temporary directory and returns its path.
func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "tallox.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("cannot write the test configuration: %v", err)
	}
	return path
}

// TestTheZPABlockIsReadAsAWhole covers the three states of the block, which are deliberately
// three rather than two.
//
// Absent is the ordinary case — every DevContainer, every CI run, every fresh clone — and must
// not be a failure. Complete is production. Half-filled is neither: somebody expressed an
// intention that would not be honoured, and the file is the thing that says who talks to the
// examination office's system, so it has to be loud on the restart that introduces the mistake
// rather than on the first nightly import that quietly never ran.
//
// The same asymmetry auth.protectedadmins already has: an empty list warns, a malformed entry
// refuses.
func TestTheZPABlockIsReadAsAWhole(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		body       string
		wantErr    string
		configured bool
	}{
		{
			name: "absent is not a failure",
			body: "auth:\n  mode: proxy\n",
		},
		{
			name:       "complete",
			body:       "zpa:\n  baseurl: https://zpa.example.org\n  token: example-token\n",
			configured: true,
		},
		{
			name:    "token without an address",
			body:    "zpa:\n  token: example-token\n",
			wantErr: "zpa.baseurl",
		},
		{
			name:    "address without a token",
			body:    "zpa:\n  baseurl: https://zpa.example.org\n",
			wantErr: "zpa.token",
		},
		{
			name:    "an address that is not http",
			body:    "zpa:\n  baseurl: ftp://zpa.example.org\n  token: example-token\n",
			wantErr: "scheme",
		},
		{
			name:    "an address with a query",
			body:    "zpa:\n  baseurl: https://zpa.example.org/?v=2\n  token: example-token\n",
			wantErr: "without a query",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cfg, _, err := bootstrap.LoadConfig(writeConfig(t, tc.body))

			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("a half-configured or malformed zpa block was accepted — the "+
						"import would silently never run: %q", tc.body)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Errorf("the error does not say which key is at fault (want %q): %v",
						tc.wantErr, err)
				}
				return
			}

			if err != nil {
				t.Fatalf("loading failed: %v", err)
			}
			if got := cfg.ZPA.Configured(); got != tc.configured {
				t.Errorf("Configured() = %v, want %v", got, tc.configured)
			}
		})
	}
}

// TestTheZPAAddressLosesItsTrailingSlash.
//
// Joining a path onto a base that ends in a slash produces a doubled separator, which most
// reverse proxies answer with a redirect. The client does not follow redirects on purpose, so
// the symptom would be a refusal from a URL nobody typed — an expensive thing to diagnose for
// a character.
func TestTheZPAAddressLosesItsTrailingSlash(t *testing.T) {
	t.Parallel()

	cfg, _, err := bootstrap.LoadConfig(writeConfig(t,
		"zpa:\n  baseurl: \"https://zpa.example.org/\"\n  token: \"  example-token  \"\n"))
	if err != nil {
		t.Fatalf("loading failed: %v", err)
	}
	if cfg.ZPA.BaseURL != "https://zpa.example.org" {
		t.Errorf("base url is %q, want it without the trailing slash", cfg.ZPA.BaseURL)
	}
	if cfg.ZPA.Token != "example-token" {
		t.Errorf("token is %q, want it trimmed — a copied-in secret brings whitespace with it",
			cfg.ZPA.Token)
	}
}
