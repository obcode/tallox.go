package domain

import (
	"context"
	"net/netip"
	"time"

	"github.com/google/uuid"
)

// AccessLogRetention is how long an entry is kept before the nightly run deletes it.
//
// Deliberately a constant here rather than a configuration key, and the reason is not
// simplicity. This is a record of when colleagues worked and what they looked at; a retention
// period that is set per installation is a retention period that nobody can state in a sentence
// when the staff council asks. Ninety days is long enough for "what happened last month" — the
// question one asks after noticing something — and short enough that no annual movement profile
// exists to be asked for.
//
// The other half of that argument is that a key would be forward-only (UnmarshalExact), so the
// value would in practice be whatever the file on the host says, which is exactly the state
// this constant prevents.
const AccessLogRetention = 90 * 24 * time.Hour

// AccessDoor is which mount an entry came through — the Kind of the invariant, recorded.
type AccessDoor string

const (
	// AccessDoorInteractive is a person in a browser, behind the auth proxy.
	AccessDoorInteractive AccessDoor = "INTERACTIVE"
	// AccessDoorToken is a Personal Access Token: a script, a notebook, a cron job.
	AccessDoorToken AccessDoor = "TOKEN"
)

// AccessOutcome is how a request ended.
//
// The three refusals are separate values rather than one because they are three different
// events with three different responses. A refused sign-in is somebody who cannot get in at
// all — the thing an administrator wants to see tonight. A scope refusal is a colleague's
// script asking for something its token was not minted for, and the fix is a new token. An
// interactive-only refusal is a script reaching for personnel data, and the fix is not a new
// token — it is the person, in a browser.
type AccessOutcome string

const (
	// AccessOK is an operation that ran and returned no error.
	AccessOK AccessOutcome = "OK"
	// AccessError is an operation that ran and failed — a resolver error, a validation
	// refusal, a database that was not there.
	AccessError AccessOutcome = "ERROR"
	// AccessRefusedAuth is a request that never reached the schema: an unknown identity, a
	// deactivated person, an expired or revoked token. The only outcome without a person row.
	AccessRefusedAuth AccessOutcome = "REFUSED_AUTH"
	// AccessRefusedScope is a token whose scopes did not cover what the operation asked for.
	AccessRefusedScope AccessOutcome = "REFUSED_SCOPE"
	// AccessRefusedInteractive is a token reaching for an @interactiveOnly field.
	AccessRefusedInteractive AccessOutcome = "REFUSED_INTERACTIVE"
)

// AccessRecord is one entry as it is written.
//
// Note what is not in this struct, and see the migration for why: no variables, no query
// document, no response. Fields carries the root field names and that is the most specific this
// log ever gets about what somebody asked for. Adding a field here is not a small change.
type AccessRecord struct {
	// ActorID is the person row, when the request had one. Nil for a refused sign-in.
	ActorID *uuid.UUID
	// ActorMail is the identity as asserted, whether or not it resolved to a person.
	ActorMail string
	Door      AccessDoor
	// TokenID is the public half of the Personal Access Token used, empty otherwise.
	TokenID string
	// Roles are the effective roles the request was judged by.
	Roles []string
	// NarrowedFrom are the roles as held, when and only when the caller asked to be narrowed.
	NarrowedFrom []string
	// Operation is the operation name from the document. Client-supplied: a label, never a key.
	Operation string
	// Fields are the root field names.
	Fields   []string
	Mutation bool
	Outcome  AccessOutcome
	// ErrorCode is the extensions.code of the first error, empty when there was none. The code
	// and never the message — the German sentences get reworded.
	ErrorCode string
	Duration  time.Duration
	// SourceIP is where the request came from. Zero value when it could not be determined.
	SourceIP netip.Addr
}

// AccessEntry is one entry as it is read back.
type AccessEntry struct {
	ID        uuid.UUID
	At        time.Time
	ActorID   *uuid.UUID
	ActorMail string
	// ActorName is today's name of the person, empty when there is no person row. Resolved
	// through the join rather than stored: a name is a rendering detail, and the one thing the
	// entry must carry itself — who this was — is the mail address.
	ActorName    string
	Door         AccessDoor
	TokenID      string
	Roles        []string
	NarrowedFrom []string
	Operation    string
	Fields       []string
	Mutation     bool
	Outcome      AccessOutcome
	ErrorCode    string
	Duration     time.Duration
	SourceIP     netip.Addr
}

// AccessFilter narrows a page of the log. The zero value is "everything, newest first".
type AccessFilter struct {
	ActorID *uuid.UUID
	// Mail is a substring match, for the support question that starts with half an address.
	Mail          string
	Door          AccessDoor
	OnlyRefused   bool
	OnlyMutations bool
	From          *time.Time
	Until         *time.Time
	// Before is the keyset cursor: the entry the previous page ended on.
	Before *AccessCursor
	Limit  int
}

// AccessCursor is where a page continues, as (at, id).
//
// Both halves. Two entries can share a microsecond — a browser fires several operations at
// once on a page load — and a cursor on the timestamp alone would drop whichever landed second.
type AccessCursor struct {
	At time.Time
	ID uuid.UUID
}

// DefaultAccessLimit and MaxAccessLimit bound a page.
//
// A ceiling rather than an unbounded read, unlike glabs' owner-wide dump. There, a dump is one
// person's own history; here it is everybody's, and "select the lot" is how a support question
// turns into a term of colleagues' movements in one response body.
const (
	DefaultAccessLimit = 100
	MaxAccessLimit     = 1000
)

// Normalised returns the filter with its limit brought into range.
func (f AccessFilter) Normalised() AccessFilter {
	switch {
	case f.Limit <= 0:
		f.Limit = DefaultAccessLimit
	case f.Limit > MaxAccessLimit:
		f.Limit = MaxAccessLimit
	}
	return f
}

// AccessCounts are the headline figures of one window.
type AccessCounts struct {
	Total              int64
	Interactive        int64
	Token              int64
	Mutations          int64
	Errors             int64
	RefusedAuth        int64
	RefusedScope       int64
	RefusedInteractive int64
	// People is how many distinct person rows appear. Refused sign-ins have none and are not
	// counted here — they are counted by RefusedAuth, and by RefusedSignIn below by name.
	People int64
}

// AccessRoleCount is how much happened under one effective role.
type AccessRoleCount struct {
	Role       string
	Operations int64
}

// RefusedSignIn is one identity that was turned away, grouped.
type RefusedSignIn struct {
	Mail string
	// Reason is the refusal's error code, empty when the door did not name one.
	Reason   string
	Door     AccessDoor
	Attempts int64
	LastAt   time.Time
}

// MutationCount is one root field somebody changed something with.
type MutationCount struct {
	Mail   string
	Field  string
	Calls  int64
	LastAt time.Time
}

// AccessSummary is what the nightly report and the administration page both read.
//
// One type for both, so the two cannot drift into disagreeing about what happened last night —
// which is the failure mode of a report that computes its own figures beside a page that
// computes its own.
type AccessSummary struct {
	From   time.Time
	Until  time.Time
	Counts AccessCounts
	Roles  []AccessRoleCount
	// Refused are the sign-ins that never got in. This is the part that names people, and it
	// names them because being turned away is itself the event worth reporting.
	Refused []RefusedSignIn
	// Mutations is everything that was changed, by whom and with which root field.
	Mutations []MutationCount
}

// AccessStore is the persistence behind the access log.
//
// A seam like every other in internal/domain: the package may not import pgx, and a summary
// should be assemblable in a test without a database. The write side is deliberately on the
// same interface — internal/auth holds a one-method view of it (auth.AccessRecorder) so that
// the refusal path can record without importing the store.
type AccessStore interface {
	Record(ctx context.Context, rec AccessRecord) error
	Entries(ctx context.Context, filter AccessFilter) ([]AccessEntry, error)
	Summary(ctx context.Context, from, until time.Time) (AccessSummary, error)
	Prune(ctx context.Context, cutoff time.Time) (int64, error)
}
