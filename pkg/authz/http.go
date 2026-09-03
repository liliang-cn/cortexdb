package authz

import "fmt"

// This file is the door pkg/httpapi comes in through.
//
// AuthorizeMethod and AuthorizeRows both begin by looking a gRPC full method
// name up in the table in methods.go, which is the right shape for a surface
// whose operations are named "/cortexdb.v1.MemoryService/SaveMemory". HTTP's
// operations are named "POST /v1/memory", and the two do not map one to one —
// one REST route reaches the whole toolbox, and a path plus a verb is not a
// method name under any renaming. So pkg/httpapi keeps its own explicit route
// table and hands the classification in, and the two functions below apply the
// same rule to it.
//
// The rule and its wording live here rather than in pkg/httpapi on purpose. A
// key confined to user_id="hermes" must be refused in the same words whichever
// port refused it: an operator debugging a scope should not have to learn two
// vocabularies, and two refusal rules maintained separately are two rules that
// will eventually disagree about what a key may do. When these change,
// AuthorizeMethod and AuthorizeRows change with them.

// AuthorizeOperation reports whether the key's clearance permits an operation
// the caller has already classified. name is what the denial should call the
// operation — for HTTP, the method and path.
//
// An Unclassified operation is denied, exactly as an unclassified gRPC method
// is. Failing open here would mean a newly added route — the case most likely
// to be a write — is reachable by every read-only key until somebody notices.
func (k Key) AuthorizeOperation(name string, m Method) error {
	if m.Access != Read && m.Access != Write {
		return fmt.Errorf("%w: %s is not classified as a read or a write", ErrDenied, name)
	}
	if m.Access == Write && k.Clearance != ReadWrite {
		return fmt.Errorf("%w: key %q is %s and %s is a write", ErrDenied, k.ID, k.Clearance, name)
	}
	return nil
}

// AuthorizeOperationRows reports whether the key's scope permits the rows an
// already-classified operation asks for. It is a no-op for an unconfined key.
//
// The rule is the one AuthorizeRows documents at length, and the reasoning
// there — reject, never narrow, and an unset confined field is a rejection too
// — applies here unchanged. It has to: a request that names no user_id means
// "every user" over JSON for the same reason it does over protobuf, and a
// surface that quietly narrowed it instead would hand back a subset of the
// answer with nothing to say so.
func (k Key) AuthorizeOperationRows(name string, m Method, lookup FieldLookup) error {
	if k.Scope.IsZero() {
		return nil
	}
	if m.Access != Read && m.Access != Write {
		return fmt.Errorf("%w: %s is not classified as a read or a write", ErrDenied, name)
	}
	if m.Rowless {
		return nil
	}
	if lookup == nil {
		return fmt.Errorf("%w: key %q is confined and the request for %s could not be inspected",
			ErrDenied, k.ID, name)
	}
	for _, c := range k.Scope.constraints() {
		top, nested, declared := lookup(c.field)
		if !declared {
			return fmt.Errorf("%w: key %q is confined to %s=%q and %s carries no %s field",
				ErrDenied, k.ID, c.field, c.value, name, c.field)
		}
		if top == "" {
			return fmt.Errorf("%w: key %q is confined to %s=%q and the request leaves %s unset",
				ErrDenied, k.ID, c.field, c.value, c.field)
		}
		if top != c.value {
			return fmt.Errorf("%w: key %q is confined to %s=%q and the request asks for %q",
				ErrDenied, k.ID, c.field, c.value, top)
		}
		for _, v := range nested {
			if v != c.value {
				return fmt.Errorf("%w: key %q is confined to %s=%q and the request carries a nested %s=%q",
					ErrDenied, k.ID, c.field, c.value, c.field, v)
			}
		}
	}
	return nil
}
