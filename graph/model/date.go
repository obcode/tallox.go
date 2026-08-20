package model

import (
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/99designs/gqlgen/graphql"
)

// Date is a calendar day, without a time of day and without a timezone.
//
// # Why not the Time scalar
//
// The first date in this schema is when a set of examination regulations starts applying, and
// the ones coming after it are the milestones of the planning process. None of them is an
// instant: regulations that apply from the first of October do not apply from midnight, and a
// deadline rendered as `2026-10-01T00:00:00+02:00` invites somebody to ask what happens at
// 00:30. Sending a day as an instant also makes it a lie in one direction — a client in another
// timezone reading it back gets the day before.
//
// The wire form is `2026-10-01`, which is what the source publishes and what a person writing
// an evaluation script expects to compare as a string.
type Date time.Time

// DateLayout is the wire form: four digits, a hyphen, two, a hyphen, two.
const DateLayout = "2006-01-02"

// NewDate builds a Date from a time, keeping only the calendar day.
func NewDate(t time.Time) Date { return Date(t) }

// Time returns the underlying time, at midnight in whatever location it carries.
func (d Date) Time() time.Time { return time.Time(d) }

// MarshalGQL writes the day as a quoted string.
func (d Date) MarshalGQL(w io.Writer) {
	_, _ = io.WriteString(w, strconv.Quote(time.Time(d).Format(DateLayout)))
}

// UnmarshalGQL reads `2026-10-01` and refuses anything else.
//
// Refuses rather than repairs, deliberately, and for the same reason the semester code refuses
// `WS 2026` instead of rearranging it: a date this program guessed at is a date somebody will
// later have to disprove. `2026-10-01T00:00:00Z` is rejected too — accepting an instant here
// would let a client's timezone decide which day was meant.
func (d *Date) UnmarshalGQL(v any) error {
	s, ok := v.(string)
	if !ok {
		return fmt.Errorf("ein Datum muss als Zeichenkette im Format JJJJ-MM-TT angegeben werden")
	}
	parsed, err := time.ParseInLocation(DateLayout, s, time.Local)
	if err != nil {
		return fmt.Errorf("%q ist kein Datum im Format JJJJ-MM-TT", s)
	}
	*d = Date(parsed)
	return nil
}

var (
	_ graphql.Marshaler   = Date{}
	_ graphql.Unmarshaler = (*Date)(nil)
)
