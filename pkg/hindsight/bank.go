package hindsight

// Disposition traits influence how opinions are weighted during reflection.
// These traits only affect context formatting, not recall operations.
type Disposition struct {
	// Skepticism: 1=Trusting, 5=Skeptical
	// Affects how strongly contradictory evidence is considered
	Skepticism int

	// Literalism: 1=Flexible interpretation, 5=Literal interpretation
	// Affects how strictly facts are treated vs interpreted
	Literalism int

	// Empathy: 1=Detached, 5=Empathetic
	// Affects how user-centric vs objective the context is
	Empathy int
}

// DefaultDisposition returns a disposition with balanced traits.
func DefaultDisposition() *Disposition {
	return &Disposition{
		Skepticism: 3,
		Literalism: 3,
		Empathy:    3,
	}
}

// Validate checks if disposition values are in valid range.
func (d *Disposition) Validate() bool {
	if d.Skepticism < 1 || d.Skepticism > 5 {
		return false
	}
	if d.Literalism < 1 || d.Literalism > 5 {
		return false
	}
	if d.Empathy < 1 || d.Empathy > 5 {
		return false
	}
	return true
}

// Bank represents a memory bank - an isolated memory space for an agent.
// Multiple agents can have separate memory banks with different dispositions.
type Bank struct {
	// ID is the unique identifier for this bank
	ID string

	// Name is a human-readable name
	Name string

	// Disposition configures how this bank forms opinions
	*Disposition

	// Description provides context about this bank's purpose
	Description string

	// Background provides additional context for the bank
	Background string

	// CreatedAt is the Unix timestamp when this bank was created
	CreatedAt int64
}

// NewBank creates a new memory bank with default disposition.
func NewBank(id, name string) *Bank {
	return &Bank{
		ID:          id,
		Name:        name,
		Disposition: DefaultDisposition(),
		CreatedAt:   0, // Will be set by storage
	}
}

// NewBankWithDisposition creates a new memory bank with custom disposition.
func NewBankWithDisposition(id, name string, disp *Disposition) *Bank {
	return &Bank{
		ID:          id,
		Name:        name,
		Disposition: disp,
		CreatedAt:   0,
	}
}
