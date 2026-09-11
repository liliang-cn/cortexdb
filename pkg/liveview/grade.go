package liveview

import cortexdb "github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"

// Colouring the scene by how well established a record is, rather than by what
// kind of thing it is.
//
// Type is what the graph has always been coloured by, and it answers "what is
// in here". The question a person actually brings to a picture of a brain is
// the other one — which of this did anybody check — and the knowledge contract
// already answers it per record, in the same `_grade` the panel at the bottom
// of the page tallies. Until now the two halves of the page disagreed about
// what they were showing: the tally counted grades over the whole store and the
// scene above it was coloured by node_type.
//
// This is a mode, not a replacement. Type stays the default, because a caller
// who upgrades and touches nothing must get the page they had.

// The contract's palette, lifted for a dark ground.
//
// The product's own contract colours are chosen for a light background:
// verified #047857, self_consistent #0e7490, asserted #8f9bb0, held #b45309,
// refused #b42318. On #04060d they are all but invisible — #047857 against
// this page's background is a contrast ratio under 2, and #b42318 reads as a
// dark smudge rather than as an alarm. Worse, the scene bloom-passes the
// rendered frame, so a dark fill does not merely read badly, it drops out of
// the glow entirely and a refused node becomes the one thing on the page you
// cannot see.
//
// So each is lifted to roughly the same hue at a much higher lightness — the
// Tailwind-600 originals taken to their 400 step, which is the same ramp the
// rest of this page's colours already come off — and the ordering between them
// is preserved: verified is still the green, refused is still the red, and
// asserted is still the one that says nothing much. A reader who knows the
// light palette recognises these; a reader who does not still reads green as
// settled and red as refused.
const (
	// GradeColorVerified lifts #047857 (emerald-700) to emerald-400.
	GradeColorVerified = "#34d399"
	// GradeColorSelfConsistent lifts #0e7490 (cyan-700) to sky-400.
	GradeColorSelfConsistent = "#38bdf8"
	// GradeColorAsserted lifts #8f9bb0 (a cool grey) to slate-400. Asserted is
	// the shoulder-shrug of the five — a source said so and nothing checked it
	// — and it must not look like an achievement.
	GradeColorAsserted = "#94a3b8"
	// GradeColorHeld lifts #b45309 (amber-700) to amber-400.
	GradeColorHeld = "#fbbf24"
	// GradeColorRefused lifts #b42318 (red-700) to red-400.
	GradeColorRefused = "#f87171"
	// GradeColorUntagged is the dim grey for a record carrying no _grade at
	// all. Deliberately darker than asserted and desaturated to nothing: it is
	// the shape of the shelf rather than a judgement, and on a real brain it
	// is most of what is drawn, so a bright colour here would drown the five.
	GradeColorUntagged = "#3f4d66"
	// GradeColorUnknown is a _grade the contract does not define — a producer
	// writing something wrong. Rose, and the only colour here that is not on
	// the contract's ladder at all, because it is somebody's bug and the page
	// must not let it hide among the values the contract does define. It is
	// the same rose the contract panel already marks an unknown row with.
	GradeColorUnknown = "#fb7185"
)

// gradePalette maps each of the contract's grades to its lifted colour.
//
// Keyed off the constants pkg/cortexdb's contract.go defines, so a grade
// renamed there stops compiling here rather than silently losing its colour.
// A grade *added* there cannot be caught by the compiler — Go will not tell a
// map it is missing a key — so that is a test, and the test walks the closed
// set the contract itself names rather than a list written out a second time.
var gradePalette = map[string]string{
	cortexdb.GradeVerified:       GradeColorVerified,
	cortexdb.GradeSelfConsistent: GradeColorSelfConsistent,
	cortexdb.GradeAsserted:       GradeColorAsserted,
	cortexdb.GradeHeld:           GradeColorHeld,
	cortexdb.GradeRefused:        GradeColorRefused,
}

// GradeLegendEntry is one line of the grade legend: a colour, the word the
// contract uses for it, and what that word means in a sentence short enough to
// sit in a panel.
//
// The sentence is here rather than in the page because it is the contract's
// meaning and not the page's styling, and because a legend that only names the
// five teaches nobody the difference between asserted and self_consistent —
// which is the one distinction a reader of this picture has to make.
type GradeLegendEntry struct {
	Grade string `json:"grade"`
	Color string `json:"color"`
	Means string `json:"means"`
}

// gradeMeaning is the one-line gloss per grade, paraphrasing contract.go's
// own doc comments. Untagged and unknown are not grades and are appended by
// GradeLegend, which is where their two sentences live.
var gradeMeaning = map[string]string{
	cortexdb.GradeVerified:       "something outside the producer established it",
	cortexdb.GradeSelfConsistent: "coherent with what was already stated; unchecked against the world",
	cortexdb.GradeAsserted:       "a source or a model said so and nothing has checked it",
	cortexdb.GradeHeld:           "nothing yet — a person has to look",
	cortexdb.GradeRefused:        "the producer declined, and said why",
}

// GradeLegend is the legend the page draws in grade mode: the contract's five
// in the order they stand, then the two rows that are not grades.
//
// Ordered here rather than in the page for the reason ContractReport.Rows is:
// the contract's ladder of standing is Go's to know, and a page that sorted
// the five itself would be a second place to keep it.
func GradeLegend() []GradeLegendEntry {
	out := make([]GradeLegendEntry, 0, len(contractGradeOrder)+2)
	for _, g := range contractGradeOrder {
		out = append(out, GradeLegendEntry{Grade: g, Color: gradePalette[g], Means: gradeMeaning[g]})
	}
	out = append(out,
		GradeLegendEntry{Grade: ContractUntagged, Color: GradeColorUntagged,
			Means: "no _grade at all — nothing here says how it is known"},
		GradeLegendEntry{Grade: ContractUnknown, Color: GradeColorUnknown,
			Means: "a _grade the contract does not define — a producer's bug"},
	)
	return out
}

// GradeColor is the colour for one record's grade, and the same decision the
// contract panel's gradeColor makes: a value the contract defines gets its
// place on the ladder, no value at all gets the dim grey, and anything else
// gets the rose that says somebody is writing a word nobody agreed on.
func GradeColor(grade string) string {
	if grade == "" {
		return GradeColorUntagged
	}
	if c, ok := gradePalette[grade]; ok {
		return c
	}
	return GradeColorUnknown
}
