// Package graph is the GraphQL layer: schema, generated execution code and resolvers.
//
// Resolvers stay thin. They translate between the wire format and internal/domain and do
// nothing else — no database access (the architecture test in internal/arch enforces that),
// no authorization decisions (those belong in internal/policy, so that the token path
// cannot differ from the browser path).
package graph

import (
	"github.com/obcode/tallox.go/internal/buildinfo"
	"github.com/obcode/tallox.go/internal/domain"
)

//go:generate go tool gqlgen generate

// Resolver is the dependency root of the GraphQL layer. Everything a resolver needs is a
// field here, injected once in bootstrap — there is no package-level state and no service
// locator, so a test can construct a Resolver with exactly the seams it wants to observe.
type Resolver struct {
	Build buildinfo.Info
	// Tokens is token management. Nil in the handful of tests that only ask for buildInfo —
	// a resolver that panics on a field nobody exercised is louder than a fake that answers.
	Tokens *domain.TokenService
	// People is user administration, on the same terms as Tokens.
	People *domain.PeopleService
	// Wishes is the wish phase: who has registered interest in which instance part.
	//
	// Named for the area rather than for the type like its neighbours, and here the shadowing
	// its neighbours only warn about is real: `Wishes` is also the generated queryResolver method
	// for the `wishes` field, so inside any *query* resolver `r.Wishes` is that method and the
	// service has to be reached as `r.Resolver.Wishes`. The mutation resolvers have no such
	// method and say `r.Wishes` — which is why the two halves of wish.resolvers.go do not match,
	// and why that is not a slip.
	Wishes *domain.WishService
	// Marks is what opens and closes the planning: which study programmes have announced their
	// demand as settled, and which subject groups are taking wishes.
	//
	// Named for what the two are rather than for either of them, because they are one service and
	// neither name covers the other — and because DemandCompletions and WishWindows are both
	// generated queryResolver methods, so a field called after either would be shadowed.
	Marks *domain.PlanningMarkService
	// Staffing is the assignment phase: who holds which part of which instance.
	//
	// Named for what it does rather than for its type, for the reason Planning and Catalogue are —
	// and here the collision it avoids is certain rather than likely: `Assignments` is also the
	// generated queryResolver method for the `assignments` field, so a field of that name would
	// have to be reached as `r.Resolver.Assignments` inside every query resolver and as
	// `r.Assignments` inside every mutation resolver. Wishes lives with exactly that and the
	// comment above it exists to explain why its two halves do not match; once was enough.
	Staffing *domain.AssignmentService
	// SubjectGroups is the faculty's own grouping of modules and people: who works on what,
	// who leads which group, and which modules belong to it.
	//
	// Named for the area like the fields below it, and here the shadowing it avoids is real: a
	// field called Groups would read as anything at all, and SubjectGroup would collide with the
	// generated queryResolver method of that name.
	SubjectGroups *domain.SubjectGroupService
	// Planning is the semester workflow: which semesters exist and where each one stands.
	//
	// Named for the scope area rather than for the type, because `Semesters` would be shadowed
	// by the generated queryResolver method of the same name — `r.Semesters` inside a resolver
	// resolves to the method, and the resulting error is about a func having no field `List`,
	// which takes a minute to read.
	Planning *domain.SemesterService
	// Import is the module master data import: which runs there were and what they changed.
	//
	// Named for what it does rather than for its type, for the reason Planning is: a field
	// called ZpaSyncRuns would be shadowed by the generated queryResolver method of the same
	// name, and the resulting error takes a minute to read.
	Import *domain.ZPASyncService
	// Catalogue is the module catalogue: programmes, regulations, modules and how a module's
	// hours divide.
	//
	// Named for the area rather than for the type, for the reason Planning and Import are: a
	// field called Modules would be shadowed by the generated queryResolver method of the same
	// name, and the error that produces takes a minute to read.
	Catalogue *domain.CatalogueService
	// Access is the access log: who reached this installation and what they asked for.
	//
	// Named for the area rather than for the type, for the reason the five below are: a field
	// called AccessLog would be shadowed by the generated queryResolver method of the same
	// name.
	Access *domain.AccessService
	// Demand is the demand planning: which instances a study programme needs in a semester.
	//
	// Named for the area rather than for the type, for the reason the four above are: a field
	// called CourseInstances would be shadowed by the generated queryResolver method of the same
	// name, and the error that produces takes a minute to read.
	Demand *domain.DemandService
}
