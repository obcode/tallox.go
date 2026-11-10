package bootstrap_test

import (
	"testing"

	"github.com/obcode/tallox.go/internal/graphqltest"
	"github.com/obcode/tallox.go/internal/store/storetest"
	"github.com/obcode/tallox.go/internal/testdata"
)

// Taking a local course out of the lists, through the assembled handler.
//
// changeLocalModule is not @fullReach: a course is neither confidential nor personnel data, and
// neither are the instances the new refusal is about. So the same answer has to come through all
// three doors — the browser, a plain token and a full-reach one.

const changeLocalMutation = `mutation($id: ID!, $in: LocalModuleInput!) {
	changeLocalModule(id: $id, input: $in) { id name active }
}`

type changedLocalModule struct {
	ChangeLocalModule struct {
		ID     string
		Name   string
		Active bool
	}
}

// deactivated is the input that takes a course created by localInput out of the lists.
func deactivated(id, programme, name string) map[string]any {
	in := localInput(programme, name, "FWP_PLACEHOLDER")
	vars := map[string]any{"id": id, "in": in["in"]}
	vars["in"].(map[string]any)["active"] = false
	return vars
}

func TestALocalModuleInDemandIsDeactivatedOnlyAfterItIsWithdrawnThroughEveryDoor(t *testing.T) {
	t.Parallel()

	f := demandHandler(t,
		map[string][]string{
			testdata.Vier.Mail: {storetest.FixtureProgrammeA},
			testdata.Zwei.Mail: {storetest.FixtureProgrammeB},
		},
		grants{testdata.Vier, []string{"PROGRAMME_LEAD"}},
		grants{testdata.Zwei, []string{"PROGRAMME_LEAD"}})

	browser := graphqltest.New(f.handler).AsUser(testdata.Vier.Mail).On(graphqltest.Browser)
	doors := []struct {
		name string
		c    *graphqltest.Client
	}{
		{"browser", browser},
		{"token", graphqltest.New(f.handler).WithToken(testdata.Vier.Token).On(graphqltest.Token)},
		{"full reach", graphqltest.New(f.handler).
			WithToken(fullReachToken(t, f.schema, testdata.Vier)).On(graphqltest.Token)},
	}
	other := graphqltest.New(f.handler).AsUser(testdata.Zwei.Mail).On(graphqltest.Browser)

	for _, door := range doors {
		t.Run(door.name, func(t *testing.T) {
			// A name per door: the runs share a database, and the name is a local row's identity.
			name := "FWP-Platzhalter (" + door.name + ")"

			// Created and declared in the browser: declaring is @fullReach, and the claim here
			// is about deactivating, which every door may do.
			var created localModuleResult
			browser.MustQuery(t, createLocalMutation,
				localInput(storetest.FixtureProgrammeA, name, "FWP_PLACEHOLDER"), &created)
			id := created.CreateLocalModule.ID

			var declared struct{ DeclareCourseInstance struct{ ID string } }
			browser.MustQuery(t, declareMutation, map[string]any{"in": map[string]any{
				"semester":  "2027-SS",
				"programme": storetest.FixtureProgrammeA,
				"moduleId":  id,
				"track":     "A",
			}}, &declared)

			// Refused while in demand, with the code the GUI branches on.
			assertRefusal(t, door.c.Do(t, changeLocalMutation,
				deactivated(id, storetest.FixtureProgrammeA, name)), "MODULE_IN_DEMAND")

			// The lead of another programme learns nothing new: the permission is asked first.
			assertRefusal(t, other.Do(t, changeLocalMutation,
				deactivated(id, storetest.FixtureProgrammeA, name)), "NOT_YOUR_PROGRAMME")

			browser.MustQuery(t, `mutation($id: ID!) { withdrawCourseInstance(id: $id) }`,
				map[string]any{"id": declared.DeclareCourseInstance.ID}, nil)

			var out changedLocalModule
			door.c.MustQuery(t, changeLocalMutation,
				deactivated(id, storetest.FixtureProgrammeA, name), &out)
			if out.ChangeLocalModule.Active {
				t.Error("the placeholder is still active after it was deactivated")
			}
		})
	}
}

// The other direction: a course taken out of the lists cannot be declared again. Declaring is
// @fullReach, so a plain token stops at that gate, and the two doors that may plan get the same
// refusal.
func TestADeactivatedLocalModuleCannotBeDeclaredThroughEveryDoor(t *testing.T) {
	t.Parallel()

	f := demandHandler(t,
		map[string][]string{testdata.Vier.Mail: {storetest.FixtureProgrammeA}},
		grants{testdata.Vier, []string{"PROGRAMME_LEAD"}})

	browser := graphqltest.New(f.handler).AsUser(testdata.Vier.Mail).On(graphqltest.Browser)
	doors := []struct {
		name string
		c    *graphqltest.Client
		code string
	}{
		{"browser", browser, "MODULE_INACTIVE"},
		{"token", graphqltest.New(f.handler).WithToken(testdata.Vier.Token).On(graphqltest.Token),
			"FULL_REACH_REQUIRED"},
		{"full reach", graphqltest.New(f.handler).
			WithToken(fullReachToken(t, f.schema, testdata.Vier)).On(graphqltest.Token),
			"MODULE_INACTIVE"},
	}

	for _, door := range doors {
		t.Run(door.name, func(t *testing.T) {
			name := "FWP-Platzhalter stillgelegt (" + door.name + ")"

			var created localModuleResult
			browser.MustQuery(t, createLocalMutation,
				localInput(storetest.FixtureProgrammeA, name, "FWP_PLACEHOLDER"), &created)
			id := created.CreateLocalModule.ID
			browser.MustQuery(t, changeLocalMutation,
				deactivated(id, storetest.FixtureProgrammeA, name), nil)

			assertRefusal(t, door.c.Do(t, declareMutation, map[string]any{"in": map[string]any{
				"semester":  "2027-SS",
				"programme": storetest.FixtureProgrammeA,
				"moduleId":  id,
				"track":     "A",
			}}), door.code)
		})
	}
}
