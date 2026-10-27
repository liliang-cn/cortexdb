package cortexdb

import (
	"context"
	"fmt"
	"strings"
)

// How a memory is known.
//
// A lesson is worth sharing only when a reader can tell what was checked from
// what was a guess. "The resize bug is in CtrlVlmDfnModifyApiCallHandler,
// verified by reverting the fix and watching the test go red" and "probably
// the satellite does not get the update" are both memories, and an agent that
// recalls the second as if it were the first acts on a guess. Knowledge and
// graph records already say how they are known (the knowledge contract,
// contract.go); a memory now carries the same two keys, _grade and _why, with
// the same five values and the same rule that a grade claiming more than "said
// so" names its evidence.
//
// A memory saved without a grade is untagged: nobody said how it is known.
// That is kept apart from asserted, as the contract keeps "untagged" apart on
// every other record, so a store written before grades existed reads as it
// did and does not suddenly call every memory a guess. The capture paths grade
// what they write.

// memoryGradeRank orders the grades for min_grade: verified above
// self_consistent above asserted, which untagged equals, above held above
// refused. A value the contract does not define ranks with refused.
func memoryGradeRank(grade string) int {
	switch grade {
	case GradeVerified:
		return 4
	case GradeSelfConsistent:
		return 3
	case GradeAsserted, "":
		return 2
	case GradeHeld:
		return 1
	}
	return 0
}

// IsGrade reports whether grade is one of the contract's five.
func IsGrade(grade string) bool { return contractGrades[normalizeGrade(grade)] }

// GradeAtLeast reports whether a record graded grade ("" is untagged) is
// known at least as well as min.
func GradeAtLeast(grade, min string) bool {
	return memoryGradeRank(normalizeGrade(grade)) >= memoryGradeRank(normalizeGrade(min))
}

// normalizeGrade trims and lowercases a grade a caller sent.
func normalizeGrade(grade string) string {
	return strings.ToLower(strings.TrimSpace(grade))
}

// validateMemoryGrade checks a grade and its reason: the grade is one of the
// contract's five, and verified, held and refused name why. Verified needs
// one because a claim that something was checked is only as good as the
// check it names; held and refused because the contract already requires it.
func validateMemoryGrade(grade, why string) error {
	if grade == "" {
		if strings.TrimSpace(why) != "" {
			return fmt.Errorf("grade_why given without a grade")
		}
		return nil
	}
	if !contractGrades[grade] {
		return fmt.Errorf("grade %q is not one of %s", grade, contractValues(contractGrades))
	}
	if strings.TrimSpace(why) == "" && (grade == GradeVerified || grade == GradeHeld || grade == GradeRefused) {
		return fmt.Errorf("grade %s needs grade_why: the test, measurement, command or person that established it", grade)
	}
	return nil
}

// stampMemoryGrade writes grade and why into metadata under the contract
// keys, or removes them when grade is empty.
func stampMemoryGrade(metadata map[string]any, grade, why string) {
	if grade == "" {
		delete(metadata, KeyGrade)
		delete(metadata, KeyWhy)
		return
	}
	metadata[KeyGrade] = grade
	if why = strings.TrimSpace(why); why != "" {
		metadata[KeyWhy] = why
	} else {
		delete(metadata, KeyWhy)
	}
}

// gradeOfSave is the grade a save writes: the request's fields, or the
// contract keys in its metadata when the fields are empty.
func gradeOfSave(req MemorySaveRequest) (string, string) {
	grade, why := normalizeGrade(req.Grade), req.GradeWhy
	if grade == "" {
		g, _ := stringFromAny(req.Metadata[KeyGrade])
		grade = normalizeGrade(g)
	}
	if strings.TrimSpace(why) == "" {
		why, _ = stringFromAny(req.Metadata[KeyWhy])
	}
	return grade, why
}

type minGradeKey struct{}

// withMinMemoryGrade holds the searches under ctx to grade; "" holds them to
// nothing.
func withMinMemoryGrade(ctx context.Context, grade string) (context.Context, error) {
	grade = normalizeGrade(grade)
	if grade == "" {
		return ctx, nil
	}
	if !contractGrades[grade] {
		return ctx, fmt.Errorf("min_grade %q is not one of %s", grade, contractValues(contractGrades))
	}
	return context.WithValue(ctx, minGradeKey{}, grade), nil
}

// belowMinGrade reports whether grade ranks under the minimum in ctx.
func belowMinGrade(ctx context.Context, grade string) bool {
	minGrade, _ := ctx.Value(minGradeKey{}).(string)
	return minGrade != "" && !GradeAtLeast(grade, minGrade)
}

// isUnverifiedGrade reports whether a memory says it was not checked: graded
// asserted or held. Untagged says nothing either way.
func isUnverifiedGrade(grade string) bool {
	return grade == GradeAsserted || grade == GradeHeld
}
