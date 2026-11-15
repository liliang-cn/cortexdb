package authz

import "fmt"

// ExtensionTool is the policy line of a tool a server offers beside the
// toolbox: a tool of a workflow package the facade cannot import, which the
// process that builds the server registers with it.
//
// The toolbox's own tools are classified from their definitions (toolAccess)
// and, for a confined key, by the allowlist. An extension tool is in neither,
// so the server hands its line over with the call. The line can say less than
// an allowlist line on purpose: there is no way to state which scope fields an
// extension honours, so a confined key is refused every one of them.
type ExtensionTool struct {
	Name string
	// Access is what calling the tool does. Unclassified, the zero value, is
	// refused to every key.
	Access Access
	// Refusal is why a confined key may not call it, in the form refusedTools
	// uses ("writes ... which no scope field confines"). Empty gives the
	// default.
	Refusal string
}

// AuthorizeExtensionTool is AuthorizeCall and AuthorizeToolArgs for an
// extension tool: a write needs a read-write key, and a confined key is
// refused with the tool's reason.
func (k Key) AuthorizeExtensionTool(t ExtensionTool) error {
	if t.Name == "" || (t.Access != Read && t.Access != Write) {
		return fmt.Errorf("%w: tool %q is not classified", ErrDenied, t.Name)
	}
	if _, core := LookupTool(t.Name); core {
		return fmt.Errorf("%w: %s is a toolbox tool and cannot be authorized as an extension", ErrDenied, t.Name)
	}
	if t.Access == Write && k.Clearance != ReadWrite {
		return fmt.Errorf("%w: key %q is %s and tool %s writes", ErrDenied, k.ID, k.Clearance, t.Name)
	}
	if !k.Scope.IsZero() {
		reason := t.Refusal
		if reason == "" {
			reason = defaultRefusal
		}
		return fmt.Errorf("%w: key %q is confined and %s is not a tool a confined key may call: it %s",
			ErrDenied, k.ID, t.Name, reason)
	}
	return nil
}
