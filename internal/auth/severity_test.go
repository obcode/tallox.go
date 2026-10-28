package auth

import (
	"errors"
	"fmt"
	"testing"

	"github.com/rs/zerolog"
)

// TestSeverityMakesTheRightThingsVisible pins the levels rather than the wording.
//
// It exists because of a concrete evening: the production container runs at Info, every
// refusal was logged at Debug, and a wave of 401s caused by an empty person table left no
// trace in the log at all. The person debugging it had to go through oauth2-proxy's log to
// find out who was being turned away by our own middleware.
//
// An internal test (package auth, not auth_test) so that severity can stay unexported: this
// is a decision about levels, not an API. It also avoids the alternative — capturing the
// global zerolog output — which would mean mutating a package-level variable while the rest
// of this suite runs in parallel, i.e. a data race the -race detector would rightly report.
func TestSeverityMakesTheRightThingsVisible(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		err  error
		want zerolog.Level
		why  string
	}{
		{
			err:  ErrUnavailable,
			want: zerolog.ErrorLevel,
			why:  "nobody's credential is broken, the database is — that is an operational event",
		},
		{
			err:  ErrAmbiguousIdentity,
			want: zerolog.WarnLevel,
			why:  "no client can cause it through Caddy; seeing it means the proxy or the network is not what deploy/ says",
		},
		{
			err:  ErrUnknownUser,
			want: zerolog.InfoLevel,
			why:  "a real person is being turned away; this is the line somebody greps for",
		},
		{
			err:  ErrInactiveUser,
			want: zerolog.InfoLevel,
			why:  "a real account, deliberately disabled — worth seeing when it surprises somebody",
		},
		{
			err:  ErrExpiredToken,
			want: zerolog.InfoLevel,
			why:  "a token that existed and ran out; its owner will ask why it stopped working",
		},
		{
			err:  ErrRevokedToken,
			want: zerolog.InfoLevel,
			why:  "same, and it also says somebody revoked it",
		},
		{
			err:  ErrInvalidToken,
			want: zerolog.DebugLevel,
			why:  "volume is chosen by the sender; a log that can be flooded from outside is ignored",
		},
		{
			err:  ErrMalformedToken,
			want: zerolog.DebugLevel,
			why:  "same — anybody reaching the token door can produce these at any rate",
		},
	} {
		t.Run(tc.err.Error(), func(t *testing.T) {
			t.Parallel()

			if got := severity(tc.err); got != tc.want {
				t.Errorf("severity(%v) = %s, want %s — %s", tc.err, got, tc.want, tc.why)
			}

			// Wrapped, which is how they actually arrive: the authenticators add the mail
			// address or the token id with %w.
			wrapped := fmt.Errorf("%w: some detail", tc.err)
			if got := severity(wrapped); got != tc.want {
				t.Errorf("severity of a wrapped %v = %s, want %s — errors.Is is what makes "+
					"the mapping survive the context the authenticators add", tc.err, got, tc.want)
			}
		})
	}
}

// TestUnknownRefusalsAreQuiet: an error nobody has classified must not become the loudest
// thing in the log. Debug is the safe default here — the visible cases are the enumerated
// ones, and a new refusal reason gets its level chosen deliberately in the switch above.
func TestUnknownRefusalsAreQuiet(t *testing.T) {
	t.Parallel()

	if got := severity(errors.New("something nobody classified")); got != zerolog.DebugLevel {
		t.Errorf("an unclassified refusal logs at %s", got)
	}
}
