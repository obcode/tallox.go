package model

import (
	"time"

	"github.com/obcode/tallox.go/internal/domain"
)

// The catalogue types, hand-written rather than generated.
//
// # Why
//
// Two fields of Module take an argument — "is this compulsory *in this programme*" and "does it
// appear in *this programme's* catalogue" — and gqlgen binds a field with an argument to a
// struct field of the same name if it finds one, silently ignoring the argument. Generated, they
// would have answered the same thing whatever was asked, which is worse than not having them:
// the schema would document a parameter that does nothing.
//
// Hand-written, gqlgen has to call a resolver for them, and the resolver can reach the domain
// value below and use the fold that lives there. gqlgen still checks these structs against the
// schema on every generate, so a field added to one and not the other is a build failure rather
// than a drift.
//
// # Why the domain value travels with the model
//
// So that the two argument-taking fields are answered by the same function that answers them
// anywhere else. Rebuilding the fold from the model's own offerings would be a second
// implementation of a rule, in the layer that is supposed to have none.

// Programme is a study programme, on the wire.
type Programme struct {
	Code           string                 `json:"code"`
	Title          string                 `json:"title"`
	Active         bool                   `json:"active"`
	PlanningStatus domain.ProgrammeStatus `json:"planningStatus"`
	Spos           []*Spo                 `json:"spos"`
}

// Spo is one version of one programme's examination regulations, on the wire.
type Spo struct {
	ID        string     `json:"id"`
	Version   int        `json:"version"`
	ValidFrom *Date      `json:"validFrom,omitempty"`
	PrimussID *string    `json:"primussId,omitempty"`
	Programme *Programme `json:"programme"`
}

// Teacher is somebody who teaches, on the wire.
//
// `IsUser` is derived from the mail address when the row is read, not stored: somebody admitted
// this morning is connected to their own modules now rather than after the next import.
type Teacher struct {
	ID                   string  `json:"id"`
	Name                 string  `json:"name"`
	SortName             string  `json:"sortName"`
	Mail                 *string `json:"mail,omitempty"`
	IsProfessor          bool    `json:"isProfessor"`
	IsLecturerOnContract bool    `json:"isLecturerOnContract"`
	IsHonoraryProfessor  bool    `json:"isHonoraryProfessor"`
	IsStaff              bool    `json:"isStaff"`
	Active               bool    `json:"active"`
	Faculty              *string `json:"faculty,omitempty"`
	LastSemester         *string `json:"lastSemester,omitempty"`
	IsUser               bool    `json:"isUser"`
}

// Module is a catalogue entry, on the wire.
type Module struct {
	ID                  string              `json:"id"`
	Name                string              `json:"name"`
	HomeProgramme       *Programme          `json:"homeProgramme"`
	SubjectGroup        *SubjectGroupRef    `json:"subjectGroup,omitempty"`
	Responsible         *Teacher            `json:"responsible,omitempty"`
	CourseType          domain.CourseType   `json:"courseType"`
	Frequency           domain.Frequency    `json:"frequency"`
	ContactHoursPerWeek *int                `json:"contactHoursPerWeek,omitempty"`
	Credits             *int                `json:"credits,omitempty"`
	Active              bool                `json:"active"`
	Official            bool                `json:"official"`
	RetiredAt           *time.Time          `json:"retiredAt,omitempty"`
	ZpaID               *string             `json:"zpaId,omitempty"`
	Components          []*ModuleComponent  `json:"components"`
	ComponentHours      *float64            `json:"componentHours,omitempty"`
	Offerings           []*ModuleOffering   `json:"offerings"`
	Source              domain.ModuleSource `json:"source"`
	Kind                domain.ModuleKind   `json:"kind"`

	// Domain is the value this was built from, for the fields that take an argument.
	//
	// Named apart from the schema's own `source` field, which says where a catalogue row came
	// from — two different things, and one of them is not on the wire at all.
	//
	// Never marshalled — gqlgen renders only what the schema names, and the json tag says so for
	// anything else that might walk this struct.
	Domain domain.Module `json:"-"`
}

// ModuleComponent is one unit of a module's split, on the wire.
type ModuleComponent struct {
	ID            string                  `json:"id"`
	Kind          domain.InstancePartKind `json:"kind"`
	TeachingHours float64                 `json:"teachingHours"`
	Position      int                     `json:"position"`
}

// ModuleOffering is the statement that a module counts in one set of regulations, on the wire.
type ModuleOffering struct {
	Spo                  *Spo     `json:"spo"`
	IsDuty               bool     `json:"isDuty"`
	ModuleCodes          []string `json:"moduleCodes"`
	Focuses              []string `json:"focuses"`
	MinProgrammeSemester *int     `json:"minProgrammeSemester,omitempty"`
}
