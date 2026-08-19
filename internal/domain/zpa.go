package domain

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
)

// ZPAKind is one of the four object kinds the examination office's interface publishes.
//
// The internal vocabulary, deliberately not the endpoint paths. Which URL a kind is fetched
// from is a fact about somebody else's HTTP surface and lives in internal/zpa; these constants
// are what the database CHECK constraint and every later query speak, and a released migration
// cannot be edited when they rename a path.
//
// The list has three homes that cannot import one another — this one, the constraint, and the
// GraphQL enum — so internal/store carries a test comparing the first two.
type ZPAKind string

const (
	// ZPAKindModule is the module catalogue: one row per module, with its home programme,
	// course type, credits and the prose blocks of the module description.
	ZPAKindModule ZPAKind = "MODULE"
	// ZPAKindBasket is a catalogue slot inside a programme's examination regulations —
	// compulsory or elective, optionally belonging to a specialisation.
	ZPAKindBasket ZPAKind = "BASKET"
	// ZPAKindMSBA is the association: one row per (module, regulations, basket). This is where
	// the module code, the earliest programme semester and the examination types live, because
	// all three differ between the programmes a module appears in.
	ZPAKindMSBA ZPAKind = "MSBA"
	// ZPAKindSPO is one version of one programme's examination regulations.
	ZPAKindSPO ZPAKind = "SPO"
)

// AllZPAKinds returns every kind, in the order a sync fetches them.
//
// Smallest first, and that ordering is a decision rather than tidiness: the two large fetches
// are the ones that can time out, and failing after the cheap ones have already been written
// leaves a run that is partial in a useful way rather than empty.
func AllZPAKinds() []ZPAKind {
	return []ZPAKind{ZPAKindSPO, ZPAKindBasket, ZPAKindModule, ZPAKindMSBA}
}

// Valid reports whether k is a kind this program knows.
func (k ZPAKind) Valid() bool {
	for _, known := range AllZPAKinds() {
		if k == known {
			return true
		}
	}
	return false
}

// ZPAObject is one object as it arrived, with the two things read out of it.
//
// Payload stays opaque on purpose. The interface's shape has already changed once, and a
// typed mirror would have to be migrated every time it changes again — for a cache of a
// database we neither own nor influence. Worse, a typed mirror silently discards what it does
// not model, so the first question about a field nobody thought to map is answered with "we
// never knew". Keeping the payload whole is what makes the change log able to say what
// changed, including in fields Tallox has no opinion about.
type ZPAObject struct {
	// ZpaID is the object's primary key in the examination office's database. An import key
	// here and never an identity: it belongs in a nullable reference column beside a Tallox row
	// with its own uuid, and nothing joins on it at request time. The sibling project made a
	// foreign identifier its primary key and every table downstream inherited its quirks.
	ZpaID int64
	// Payload is the object exactly as it arrived.
	Payload json.RawMessage
	// Label is a best-effort human name, empty when the payload carries none. It is only ever
	// shown next to an id, never matched on.
	Label string
}

// ZPASource is where the objects come from.
//
// Declared here, by the consumer, and satisfied by internal/zpa — the same direction
// auth.UserLookup runs in, and for the same reason: this package then never imports net/http,
// and the sync can be driven in a test by a hand-written source.
//
// That a fake is acceptable here is not in tension with "do not mock the database". The rule
// exists because the rules of this system live in SQL, so a fake store passes while the
// shipped query leaks. Nothing of the kind is true of HTTP: what the real interface does is
// tested in internal/zpa against an httptest server, and what the sync does with the result is
// tested here.
type ZPASource interface {
	Fetch(ctx context.Context, kind ZPAKind) ([]ZPAObject, error)
}

// ChangedKeys reports the top-level keys in which two payloads differ.
//
// Top-level only. A structural diff is a bigger feature than the question being asked, and the
// question is "what changed last night" — answerable from key names, and renderable as a
// sentence rather than as two blobs of JSON side by side. The payloads are flat enough for
// this to be informative: measured against the real interface, the module objects are entirely
// scalar and the others nest one level.
//
// A payload that does not parse as an object yields the single key "" rather than an error.
// Change detection must never be the thing that fails a sync; the hash has already established
// that something is different.
func ChangedKeys(before, after json.RawMessage) []string {
	a, aOK := topLevelKeys(before)
	b, bOK := topLevelKeys(after)
	if !aOK || !bOK {
		return []string{""}
	}

	// Values are compared after canonicalising, never as bytes, and this is not theoretical
	// tidiness — it was wrong first and the first real run showed it.
	//
	// One side has been through jsonb and the other is exactly what arrived. Their interface
	// escapes non-ASCII (`nach Ankündigung`) and jsonb stores the decoded character, so a
	// byte comparison calls every field containing an umlaut different. Changing one field of
	// one module reported eight, and since every German module description has umlauts in it,
	// the report would have named nearly every field of nearly every change — which is the
	// amount of information a hash already carried.
	changed := make([]string, 0, len(a)+len(b))
	for key, beforeValue := range a {
		afterValue, present := b[key]
		if !present || !sameJSON(beforeValue, afterValue) {
			changed = append(changed, key)
		}
	}
	for key := range b {
		if _, present := a[key]; !present {
			changed = append(changed, key)
		}
	}

	sort.Strings(changed)
	return changed
}

// sameJSON compares two payloads by value rather than by bytes.
//
// The stored side has been through jsonb and the fetched side has not, so their key order and
// whitespace differ for reasons that are not changes. Comparing bytes would report the entire
// catalogue as changed on the first run after any serialiser change on either side.
func sameJSON(a, b json.RawMessage) bool {
	var av, bv any
	if err := json.Unmarshal(a, &av); err != nil {
		return false
	}
	if err := json.Unmarshal(b, &bv); err != nil {
		return false
	}
	ac, err := json.Marshal(av)
	if err != nil {
		return false
	}
	bc, err := json.Marshal(bv)
	if err != nil {
		return false
	}
	return string(ac) == string(bc)
}

func topLevelKeys(raw json.RawMessage) (map[string]json.RawMessage, bool) {
	if len(raw) == 0 {
		return nil, false
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, false
	}
	return obj, true
}

// ParseZPAKind turns a stored string back into a kind.
func ParseZPAKind(s string) (ZPAKind, error) {
	k := ZPAKind(s)
	if !k.Valid() {
		return "", fmt.Errorf("unknown zpa object kind %q", s)
	}
	return k, nil
}
