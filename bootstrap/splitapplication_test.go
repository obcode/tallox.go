package bootstrap_test

import (
	"testing"

	"github.com/obcode/tallox.go/internal/graphqltest"
	"github.com/obcode/tallox.go/internal/store/storetest"
	"github.com/obcode/tallox.go/internal/testdata"
)

// applyModuleSplitToInstances through the API.
//
// The rows are tested in internal/store; here it is who may, at which door, and the one rule that
// is a decision rather than mechanics: a caller who cannot read an instance's assignments gets an
// answer that does not depend on them.

const applySplitMutation = `mutation($m: ID!, $dry: Boolean!) {
	applyModuleSplitToInstances(moduleId: $m, dryRun: $dry) {
		dryRun unchanged
		changed { instance { id programme { code } } teachingHoursBefore teachingHoursAfter }
		refused { instance { programme { code } } kind code message }
	}
}`

type splitApplicationOut struct {
	ApplyModuleSplitToInstances struct {
		DryRun    bool
		Unchanged int
		Changed   []struct {
			Instance struct {
				ID        string
				Programme struct{ Code string }
			}
			TeachingHoursBefore float64
			TeachingHoursAfter  float64
		}
		Refused []struct {
			Instance struct{ Programme struct{ Code string } }
			Kind     string
			Code     string
			Message  string
		}
	}
}

const partsOfModuleQuery = `query($s: String!) {
	courseInstances(semester: $s) { programme { code } parts { kind teachingHours } }
}`

// partKinds reads the kinds of the fixture semester's instances, by programme.
func partKinds(t *testing.T, c *graphqltest.Client) map[string][]string {
	t.Helper()
	var out struct {
		CourseInstances []struct {
			Programme struct{ Code string }
			Parts     []struct {
				Kind          string
				TeachingHours float64
			}
		}
	}
	c.MustQuery(t, partsOfModuleQuery, map[string]any{"s": "2027-SS"}, &out)
	kinds := map[string][]string{}
	for _, i := range out.CourseInstances {
		for _, p := range i.Parts {
			kinds[i.Programme.Code] = append(kinds[i.Programme.Code], p.Kind)
		}
	}
	return kinds
}

func TestApplyingASplitThroughTheAPI(t *testing.T) {
	t.Parallel()

	f := demandHandler(t,
		map[string][]string{testdata.Vier.Mail: {storetest.FixtureProgrammeA}},
		grants{testdata.Vier, []string{"LECTURER", "PROGRAMME_LEAD"}},
		grants{testdata.Eins, []string{"LECTURER"}},
		grants{testdata.Fuenf, []string{"LECTURER", "DEANS_OFFICE"}})

	browser := func(who testdata.Persona) *graphqltest.Client {
		return graphqltest.New(f.handler).AsUser(who.Mail).On(graphqltest.Browser)
	}
	lead, dean := browser(testdata.Vier), browser(testdata.Fuenf)

	// The module is at home in A; B plans it too, separately — declared beside A it would be
	// held by A and have no parts of its own.
	var declared struct{ DeclareCourseInstance struct{ ID string } }
	lead.MustQuery(t, declareMutation,
		declareInput(f, storetest.FixtureProgrammeA, "2027-SS", ""), &declared)
	dean.MustQuery(t, declareMutation,
		declareInput(f, storetest.FixtureProgrammeB, "2027-SS", ""), &declared)
	var released struct{ ReleaseInstanceCoverage struct{ ID string } }
	dean.MustQuery(t, `mutation($id: ID!) { releaseInstanceCoverage(id: $id) { id } }`,
		map[string]any{"id": declared.DeclareCourseInstance.ID}, &released)

	// The split is corrected: a longer lecture, and an exercise instead of the laboratory.
	var set struct{ SetModuleComponents struct{ ID string } }
	lead.MustQuery(t, `mutation($m: ID!) {
		setModuleComponents(moduleId: $m, components: [
			{kind: LECTURE, teachingHours: 3}, {kind: EXERCISE, teachingHours: 2}]) { id }
	}`, map[string]any{"m": f.module.String()}, &set)

	args := func(dry bool) map[string]any { return map[string]any{"m": f.module.String(), "dry": dry} }

	t.Run("a lecturer may not", func(t *testing.T) {
		if code := errorCode(t, browser(testdata.Eins).Do(t, applySplitMutation, args(true))); code != "NOT_YOUR_MODULE" {
			t.Errorf("a lecturer was told %s, want NOT_YOUR_MODULE", code)
		}
	})

	t.Run("not through a token, because it can answer PART_ASSIGNED", func(t *testing.T) {
		resp := graphqltest.New(f.handler).WithToken(testdata.Vier.Token).Do(t, applySplitMutation, args(true))
		if code := errorCode(t, resp); code != "INTERACTIVE_ONLY" {
			t.Errorf("through a token it was answered with %s, want INTERACTIVE_ONLY", code)
		}
	})

	t.Run("the dry run writes nothing", func(t *testing.T) {
		var out splitApplicationOut
		lead.MustQuery(t, applySplitMutation, args(true), &out)
		if got := out.ApplyModuleSplitToInstances; !got.DryRun || len(got.Changed) != 2 {
			t.Fatalf("the preview is %+v, want both programmes' instances changed", got)
		}
		if got := partKinds(t, lead); len(got[storetest.FixtureProgrammeA]) != 2 ||
			got[storetest.FixtureProgrammeA][1] != "LAB" {
			t.Errorf("after the preview the parts are %v, want them untouched", got)
		}
	})

	// The home programme's lead reaches every instance, but cannot read B's assignments: B's
	// laboratory stays, reported the same way whether anybody holds it or not.
	t.Run("the home programme's lead carries it over, and leaves what it cannot see", func(t *testing.T) {
		var out splitApplicationOut
		lead.MustQuery(t, applySplitMutation, args(false), &out)
		got := out.ApplyModuleSplitToInstances
		if len(got.Changed) != 2 {
			t.Errorf("%d instances changed, want both", len(got.Changed))
		}
		if len(got.Refused) != 1 || got.Refused[0].Code != "SPLIT_LEFT_TO_PROGRAMME" ||
			got.Refused[0].Instance.Programme.Code != storetest.FixtureProgrammeB || got.Refused[0].Kind != "LAB" {
			t.Fatalf("the refusals are %+v, want B's laboratory left to its programme", got.Refused)
		}
		kinds := partKinds(t, lead)
		if a := kinds[storetest.FixtureProgrammeA]; len(a) != 2 || a[0] != "LECTURE" || a[1] != "EXERCISE" {
			t.Errorf("A's parts are %v, want LECTURE, EXERCISE", a)
		}
		if b := kinds[storetest.FixtureProgrammeB]; len(b) != 2 || b[1] != "LAB" {
			t.Errorf("B's parts are %v, want the laboratory kept and no exercise beside it", b)
		}
	})

	t.Run("the dean's office finishes it", func(t *testing.T) {
		var out splitApplicationOut
		dean.MustQuery(t, applySplitMutation, args(false), &out)
		if got := out.ApplyModuleSplitToInstances; len(got.Refused) != 0 || got.Unchanged != 1 {
			t.Errorf("the dean's office got %+v, want A unchanged and nothing refused", got)
		}
		if b := partKinds(t, dean)[storetest.FixtureProgrammeB]; len(b) != 2 || b[1] != "EXERCISE" {
			t.Errorf("B's parts are %v, want LECTURE, EXERCISE", b)
		}
	})
}
