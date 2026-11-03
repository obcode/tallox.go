package bootstrap_test

import (
	"slices"
	"testing"
	"time"

	"github.com/obcode/tallox.go/internal/graphqltest"
	"github.com/obcode/tallox.go/internal/policy"
	"github.com/obcode/tallox.go/internal/testdata"
)

// OBSERVER through the assembled handler: what the policy tests say, said again where a forgotten
// branch in a domain service or a resolver would show.

// An observer reads the demand and the controlling of every programme, the rosters, and the
// published assignments — and no wish, not even a published one. Through every door, because the
// narrowing is a rule about the account, not about the session.
func TestAnObserverReadsThePlanningButNoWishes(t *testing.T) {
	t.Parallel()

	f := wishHandler(t,
		grants{testdata.Eins, []string{"LECTURER"}},
		grants{testdata.Zwei, []string{"OBSERVER"}},
	)
	f.register(t, testdata.Eins)
	f.publish(t)

	full := graphqltest.New(f.handler).WithToken(fullReachToken(t, f.schema, testdata.Zwei))
	reads := func(t *testing.T, c *graphqltest.Client) {
		var out partnerView
		c.MustQuery(t, partnerReads, map[string]any{"s": f.semester}, &out)
		if len(out.CourseInstances) == 0 {
			t.Error("an observer reads no demand")
		}
		if len(out.TeachingLoad) < 2 {
			t.Errorf("an observer reads %d controlling rows; it is not narrowed to programmes",
				len(out.TeachingLoad))
		}
		if len(out.Wishes) != 0 {
			t.Errorf("an observer reads %d published wishes, want none", len(out.Wishes))
		}

		var roster rosterView
		c.MustQuery(t, rosterReads, nil, &roster)
		if len(roster.Teachers) == 0 {
			t.Error("an observer reads no teachers; unlike a partner it sees the rosters")
		}
	}
	graphqltest.EachDoor(t, f.handler, testdata.Zwei.Mail, testdata.Zwei.Token, reads)
	t.Run("full reach", func(t *testing.T) { reads(t, full) })

	// The list exists for its figures, and those an observer does not read.
	overview := `query($s: String!) { desiredHoursOverview(semester: $s) { person { mail } } }`
	for name, c := range map[string]*graphqltest.Client{
		"browser":    graphqltest.New(f.handler).AsUser(testdata.Zwei.Mail).On(graphqltest.Browser),
		"full reach": full,
	} {
		var out struct {
			DesiredHoursOverview []struct{ Person struct{ Mail string } }
		}
		c.MustQuery(t, overview, map[string]any{"s": f.semester}, &out)
		if len(out.DesiredHoursOverview) != 0 {
			t.Errorf("via %s: an observer reads the desired hours overview: %d people",
				name, len(out.DesiredHoursOverview))
		}
	}
}

// Every write an account without a role could reach answers an observer with its own code.
func TestAnObserverWritesNothing(t *testing.T) {
	t.Parallel()

	f := wishHandler(t, grants{testdata.Zwei, []string{"OBSERVER"}})
	for name, c := range map[string]*graphqltest.Client{
		"browser":    graphqltest.New(f.handler).AsUser(testdata.Zwei.Mail).On(graphqltest.Browser),
		"full reach": graphqltest.New(f.handler).WithToken(fullReachToken(t, f.schema, testdata.Zwei)),
	} {
		t.Run(name, func(t *testing.T) {
			mustRefuseWith(t, c, setWishMutation,
				map[string]any{"p": f.instance, "prio": "HAPPY_TO", "note": nil}, "OBSERVER_READS_ONLY")
			mustRefuseWith(t, c, setDesired, map[string]any{"s": f.semester, "h": 10}, "OBSERVER_READS_ONLY")
			mustRefuseWith(t, c, `mutation($g: [ID!]!) { setMySubjectGroups(subjectGroupIds: $g) { id } }`,
				map[string]any{"g": []string{f.group.String()}}, "OBSERVER_READS_ONLY")
		})
	}
}

// An observer reads the import and cannot start it.
func TestAnObserverReadsTheImportButDoesNotRunIt(t *testing.T) {
	t.Parallel()

	h := importHandler(t, true, grants{testdata.Zwei, []string{string(policy.RoleObserver)}})
	c := graphqltest.New(h).AsUser(testdata.Zwei.Mail).On(graphqltest.Browser)
	if resp := c.Do(t, runsQuery, nil); resp.Failed() {
		t.Fatalf("an observer cannot read the import runs: %s", resp.Body)
	}
	assertRefusal(t, c.Do(t, `mutation { syncZpaNow { id } }`, nil), "FORBIDDEN")
	assertRefusal(t, c.Do(t, `mutation { projectZpaCatalogue { id } }`, nil), "FORBIDDEN")
}

// OBSERVER is granted with a date or not at all, up to MaxObserverGrantDays when it is the only
// role being added and up to the ordinary ceiling otherwise.
func TestAnObserverIsGrantedOnlyWithAnExpiry(t *testing.T) {
	t.Parallel()

	c := graphqltest.New(adminHandler(t)).AsUser(testdata.Sechs.Mail)
	const withExpiry = `mutation ($id: ID!, $roles: [Role!]!, $until: Time) {
		setPersonRoles(id: $id, roles: $roles, expiresAt: $until) { id roles }
	}`
	zwei := testdata.Zwei.ID().String()
	in := func(days int) string { return time.Now().AddDate(0, 0, days).Format(time.RFC3339) }

	mustRefuseWith(t, c, setRolesMutation,
		map[string]any{"id": zwei, "roles": []string{"OBSERVER"}}, "OBSERVER_GRANT_MUST_EXPIRE")
	mustRefuseWith(t, c, withExpiry,
		map[string]any{"id": zwei, "roles": []string{"OBSERVER"}, "until": in(181)}, "GRANT_EXPIRY_OUT_OF_RANGE")
	mustRefuseWith(t, c, withExpiry,
		map[string]any{"id": zwei, "roles": []string{"OBSERVER", "DEANS_OFFICE"}, "until": in(100)},
		"GRANT_EXPIRY_OUT_OF_RANGE")

	var got struct{ SetPersonRoles struct{ Roles []string } }
	c.MustQuery(t, withExpiry,
		map[string]any{"id": zwei, "roles": []string{"OBSERVER"}, "until": in(100)}, &got)
	if !slices.Contains(got.SetPersonRoles.Roles, "OBSERVER") {
		t.Errorf("an observer grant for 100 days was not applied: %v", got.SetPersonRoles.Roles)
	}

	// Held, it is not re-granted, so re-sending the set needs no date.
	c.MustQuery(t, setRolesMutation, map[string]any{"id": zwei, "roles": []string{"OBSERVER"}}, &got)
}
