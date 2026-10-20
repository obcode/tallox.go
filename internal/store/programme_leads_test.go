package store_test

import (
	"testing"

	"github.com/google/uuid"

	"github.com/obcode/tallox.go/internal/store"
	"github.com/obcode/tallox.go/internal/store/storetest"
	"github.com/obcode/tallox.go/internal/testdata"
)

// scopeToProgramme gives somebody's grant of role one programme.
func scopeToProgramme(t *testing.T, s *storetest.Schema, person uuid.UUID, role string,
	programme uuid.UUID) {
	t.Helper()

	if _, err := s.Pool.Exec(t.Context(),
		`INSERT INTO person_programme_scope (person_id, role, programme_id) VALUES ($1, $2, $3)`,
		person, role, programme); err != nil {
		t.Fatalf("cannot scope %s to the programme: %v", role, err)
	}
}

// The list of a programme's leads is what everybody in the faculty reads to know whom to ask, so
// it has to name exactly the people the rules treat as its lead. Three ways to be in the scope
// table and not be one: the grant ran out, the grant is a partner faculty's reader, or the
// programme is a different one.
func TestProgrammeLeadsAreTheGrantsThatLead(t *testing.T) {
	t.Parallel()

	s := storetest.New(t)
	ctx := t.Context()

	if_ := seedProgramme(t, s, "IF")
	ib := seedProgramme(t, s, "IB")

	storetest.SeedPerson(t, s, testdata.Vier, "LECTURER", "PROGRAMME_LEAD")
	storetest.SeedPerson(t, s, testdata.Drei, "LECTURER", "PROGRAMME_LEAD")
	storetest.SeedPerson(t, s, testdata.Zwei, "PARTNER_READER")
	storetest.SeedPerson(t, s, testdata.Eins, "LECTURER", "PROGRAMME_LEAD")

	scopeToProgramme(t, s, testdata.Vier.ID(), "PROGRAMME_LEAD", if_)
	scopeToProgramme(t, s, testdata.Drei.ID(), "PROGRAMME_LEAD", if_)
	scopeToProgramme(t, s, testdata.Zwei.ID(), "PARTNER_READER", if_)
	scopeToProgramme(t, s, testdata.Eins.ID(), "PROGRAMME_LEAD", ib)

	if _, err := s.Pool.Exec(ctx,
		`UPDATE person_role
		 SET granted_at = now() - interval '2 days', expires_at = now() - interval '1 day'
		 WHERE person_id = $1 AND role = 'PROGRAMME_LEAD'`, testdata.Drei.ID()); err != nil {
		t.Fatalf("cannot expire the grant: %v", err)
	}

	leads, err := store.NewModules(s.Pool).ProgrammeLeads(ctx, []string{"IF", "IB", "XX"})
	if err != nil {
		t.Fatalf("cannot read the leads: %v", err)
	}

	mails := func(code string) []string {
		var out []string
		for _, p := range leads[code] {
			out = append(out, p.Mail)
		}
		return out
	}
	if got := mails("IF"); len(got) != 1 || got[0] != testdata.Vier.Mail {
		t.Errorf("IF is led by %v, want only %s: an expired grant and a partner reader "+
			"are not leads", got, testdata.Vier.Mail)
	}
	if got := mails("IB"); len(got) != 1 || got[0] != testdata.Eins.Mail {
		t.Errorf("IB is led by %v, want only %s", got, testdata.Eins.Mail)
	}
	if got := mails("XX"); len(got) != 0 {
		t.Errorf("a programme that does not exist is led by %v", got)
	}
}
