package obs

import (
	"testing"

	sentry "github.com/getsentry/sentry-go"
)

// scrub is the zero-value scrubber, i.e. the production configuration.
var scrub = scrubber{}

func event() *sentry.Event {
	e := sentry.NewEvent()
	e.Logger = zerologLogger
	return e
}

// The test that carries the whole design: a tag nobody anticipated must not get
// out. It uses a key that exists nowhere in this code base on purpose — if the
// filter were a deny list, this would sail straight through.
func TestATagNobodyAnticipatedIsDropped(t *testing.T) {
	e := event()
	e.Tags["caller"] = "domain/wishes.go:41"
	e.Tags["semester"] = "2026-WS"
	e.Tags["voellig_neues_feld"] = "irgendwas Personenbezogenes"

	got := scrub.scrub(e, nil)

	if _, ok := got.Tags["voellig_neues_feld"]; ok {
		t.Error("an unknown tag survived the scrubber")
	}
	if got.Tags["semester"] != "2026-WS" || got.Tags["caller"] != "domain/wishes.go:41" {
		t.Errorf("an allowed tag was dropped: %v", got.Tags)
	}
}

// The names that must never leave, spelled out, so that adding one to
// allowedTags breaks a test rather than a person's privacy.
func TestTheIdentifyingTagsAreNotAllowed(t *testing.T) {
	for _, key := range []string{
		"email", "name", "person", "personID", "user", "lecturer",
		"token", "tokenID", "wish",
	} {
		if allowedTags[key] {
			t.Errorf("%q is on the allow list and must not be", key)
		}
	}
}

func TestMailAddressesAreRedactedInFreeText(t *testing.T) {
	e := event()
	e.Message = "cannot notify oliver.braun@hm.edu about the wish"
	e.Exception = []sentry.Exception{{
		Type:  "lookup failed",
		Value: "no person for Vorname.Nachname@hm.edu",
	}}

	got := scrub.scrub(e, nil)

	if got.Message != "cannot notify [email] about the wish" {
		t.Errorf("message = %q", got.Message)
	}
	if got.Exception[0].Value != "no person for [email]" {
		t.Errorf("exception value = %q", got.Exception[0].Value)
	}
}

// A digit run must survive: unlike plexams, tallox has no Matrikelnummern, and
// eating numbers would only make messages unreadable.
func TestDigitsSurvive(t *testing.T) {
	e := event()
	e.Message = "the import took 1787419247279 ns for 1234567 rows"

	if got := scrub.scrub(e, nil); got.Message != e.Message {
		t.Errorf("digits were redacted: %q", got.Message)
	}
}

func TestTheRequestIsRebuiltAndTheQueryThrownAway(t *testing.T) {
	e := event()
	e.Request = &sentry.Request{
		Method:      "POST",
		URL:         "https://tallox.cs.hm.edu/query?person=Nachname&token=geheim",
		QueryString: "person=Nachname&token=geheim",
		Cookies:     "session=abc",
		Data:        `{"query":"mutation { setWish }"}`,
		Headers: map[string]string{
			"X-Remote-User": "oliver.braun@hm.edu",
			"Cookie":        "session=abc",
			"User-Agent":    "Mozilla/5.0",
		},
		Env: map[string]string{"REMOTE_ADDR": "10.0.0.1"},
	}

	r := scrub.scrub(e, nil).Request

	if r.QueryString != "" || r.Cookies != "" || r.Data != "" || len(r.Env) != 0 {
		t.Errorf("the rebuilt request kept something: %+v", r)
	}
	if r.URL != "https://tallox.cs.hm.edu/query" {
		t.Errorf("url = %q", r.URL)
	}
	if _, ok := r.Headers["X-Remote-User"]; ok {
		t.Error("X-Remote-User survived — that is the logged-in person's address")
	}
	if _, ok := r.Headers["Cookie"]; ok {
		t.Error("Cookie survived")
	}
	if r.Headers["User-Agent"] != "Mozilla/5.0" {
		t.Errorf("an allowed header was dropped: %v", r.Headers)
	}
}

func TestAnUnknownContextIsDropped(t *testing.T) {
	e := event()
	e.Contexts["runtime"] = sentry.Context{"name": "go"}
	e.Contexts["etwas_neues"] = sentry.Context{"wer": "jemand"}

	got := scrub.scrub(e, nil)

	if _, ok := got.Contexts["etwas_neues"]; ok {
		t.Error("an unknown context survived")
	}
	if _, ok := got.Contexts["runtime"]; !ok {
		t.Error("the runtime context was dropped")
	}
}

func TestSkipFieldDropsTheWholeEvent(t *testing.T) {
	e := event()
	e.Tags[SkipField] = "true"

	if got := scrub.scrub(e, nil); got != nil {
		t.Error("an event marked with SkipField was sent anyway")
	}
}

// The fingerprint is what keeps every log site from folding into one issue —
// and it must NOT be applied to events that carry a real stack trace.
func TestOnlyLogLinesAreFingerprintedOnTheCaller(t *testing.T) {
	logLine := event()
	logLine.Tags["caller"] = "domain/zpasync.go:343"

	if got := scrub.scrub(logLine, nil); len(got.Fingerprint) != 1 ||
		got.Fingerprint[0] != "domain/zpasync.go:343" {
		t.Errorf("fingerprint = %v", got.Fingerprint)
	}

	captured := sentry.NewEvent()
	captured.Tags["caller"] = "domain/zpasync.go:343"

	if got := scrub.scrub(captured, nil); len(got.Fingerprint) != 0 {
		t.Errorf("a captured event was given a caller fingerprint: %v", got.Fingerprint)
	}
}

func TestNoUserIsEverAttached(t *testing.T) {
	e := event()
	e.User = sentry.User{Email: "oliver.braun@hm.edu", Name: "Oliver Braun"}

	if got := scrub.scrub(e, nil); got.User.Email != "" || got.User.Name != "" {
		t.Errorf("a user survived: %+v", got.User)
	}
}
