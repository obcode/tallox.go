package bootstrap_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/obcode/tallox.go/internal/graphqltest"
	"github.com/obcode/tallox.go/internal/store/storetest"
	"github.com/obcode/tallox.go/internal/testdata"
)

// TestAStoreFailureSaysNothing is the fall-through of every area's error mapping, driven with
// a real PostgreSQL error rather than an expected refusal.
//
// The refusal tests of each area (TestAssignmentWritesDoNotLeak, TestARefusalNamesNoInternals,
// …) exercise the arms somebody wrote. This one exercises the arm nobody did: the table the
// write goes to is renamed from under the handler, the store wraps the driver's "relation does
// not exist (SQLSTATE 42P01)" with %w, and what the caller gets must be INTERNAL and the generic
// sentence — on both doors, because the token path is the one a rule gets forgotten on.
//
// A rename rather than a dropped connection, because the authenticators read the database too:
// a closed pool would refuse at the door, before any area's mapping was reached.
func TestAStoreFailureSaysNothing(t *testing.T) {
	t.Parallel()

	type probe struct {
		area        string
		handler     http.Handler
		schema      *storetest.Schema
		who         testdata.Persona
		table       string
		query       string
		vars        map[string]any
		browserOnly bool
	}

	probes := []probe{}

	{
		f := wishHandler(t, grants{testdata.Eins, []string{"LECTURER"}})
		probes = append(probes, probe{
			area: "wish", handler: f.handler, schema: f.schema, who: testdata.Eins, table: "wish",
			query: setWishMutation,
			vars:  map[string]any{"p": f.instance, "prio": "HAPPY_TO", "note": nil},
		})
	}
	{
		f := competenceHandler(t, grants{testdata.Eins, []string{"LECTURER"}})
		f.join(t, testdata.Eins)
		probes = append(probes, probe{
			area: "competence", handler: f.handler, schema: f.schema, who: testdata.Eins,
			table: "competence",
			query: setMyCompetenceMutation,
			vars:  map[string]any{"m": f.module, "l": "CAN_TEACH"},
		})
	}
	{
		f := assignmentHandler(t,
			grants{testdata.Eins, []string{"LECTURER"}},
			grants{testdata.Drei, []string{"LECTURER", "SUBJECT_GROUP_LEAD"}},
		)
		f.leadGroup(t, testdata.Drei, f.group)
		probes = append(probes, probe{
			area: "assignment", handler: f.handler, schema: f.schema, who: testdata.Drei,
			table: "assignment",
			query: setAssignmentMutation,
			vars: map[string]any{"p": f.lecture, "who": testdata.Eins.ID().String(),
				"note": nil, "replacing": nil},
			// Every assignment mutation is @interactiveOnly; the token door never reaches the store.
			browserOnly: true,
		})
	}
	{
		f := wishHandler(t, grants{testdata.Drei, []string{"LECTURER", "SUBJECT_GROUP_LEAD"}})
		f.leadGroup(t, testdata.Drei, f.group)
		probes = append(probes, probe{
			area: "planning", handler: f.handler, schema: f.schema, who: testdata.Drei,
			table: "wish_window",
			query: setWishWindowMutation,
			vars:  map[string]any{"s": f.semester, "g": f.group.String(), "open": false},
		})
	}
	{
		f := subjectGroupHandler(t,
			grants{testdata.Sechs, []string{"ADMIN"}},
			grants{testdata.Eins, []string{"LECTURER"}},
		)
		group := f.create(t, "MATHE", "Mathematik")
		probes = append(probes, probe{
			area: "subjectgroup", handler: f.handler, schema: f.schema, who: testdata.Eins,
			table: "person_subject_group",
			query: `mutation($g: [ID!]!) { setMySubjectGroups(subjectGroupIds: $g) { code } }`,
			vars:  map[string]any{"g": []string{group}},
		})
	}

	for _, p := range probes {
		t.Run(p.area, func(t *testing.T) {
			if _, err := p.schema.Pool.Exec(t.Context(),
				fmt.Sprintf(`ALTER TABLE %s RENAME TO %s_gone`, p.table, p.table)); err != nil {
				t.Fatalf("cannot pull the table away: %v", err)
			}

			check := func(t *testing.T, c *graphqltest.Client) {
				resp := c.Do(t, p.query, p.vars)
				if code := errorCode(t, resp); code != "INTERNAL" {
					t.Errorf("a store failure answered %q, want INTERNAL", code)
				}
				for _, message := range resp.Messages() {
					graphqltest.AssertNoLeak(t, message,
						append(graphqltest.DatabaseNoise(),
							"does not exist", "relation", p.table, "42P01")...)
				}
			}

			if p.browserOnly {
				inTheBrowser(t, p.handler, p.who.Mail, check)
				return
			}
			graphqltest.EachDoor(t, p.handler, p.who.Mail, p.who.Token, check)
		})
	}
}
