package rpcserver

import (
	"context"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/liliang-cn/cortexdb/v2/pkg/authz"
)

// nestedScanDepth bounds the walk for nested copies of a scope field. Nothing
// in cortexdb.v1 nests scope fields deeper than RetrievalPlan.filters, two
// levels down; the bound is here so a future recursive message cannot turn an
// authorisation check into an unbounded walk of an attacker-shaped payload.
const nestedScanDepth = 4

// authInterceptor enforces the key policy on every RPC: the bearer secret
// identifies a key, the key's clearance decides whether it may invoke the
// method, and the key's scope decides whether it may touch the rows the request
// names. A nil or empty key set disables authentication, which is what an unset
// token has always meant.
func authInterceptor(keys *authz.KeySet) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if !keys.Enabled() {
			return handler(ctx, req)
		}
		secret, err := bearerSecret(ctx)
		if err != nil {
			return nil, err
		}
		key, ok := keys.Lookup(secret)
		if !ok {
			// Deliberately the same message whether the secret was never
			// valid or has since been revoked out of the key file: telling
			// them apart tells a prober which guesses were once right.
			return nil, status.Error(codes.Unauthenticated, "invalid token")
		}
		if err := key.AuthorizeMethod(info.FullMethod); err != nil {
			return nil, status.Error(codes.PermissionDenied, err.Error())
		}
		// authorizeRequestRows is key.AuthorizeRows plus the one case it cannot
		// answer: an RPC that names a row by id and carries no scope field. See
		// ownership.go — those are waived here and enforced against the stored
		// row by the handler, which is why the key travels on in the context.
		if err := authorizeRequestRows(key, info.FullMethod, req); err != nil {
			return nil, status.Error(codes.PermissionDenied, err.Error())
		}
		return handler(withCallerKey(ctx, key), req)
	}
}

// bearerSecret pulls the token out of the authorization header.
func bearerSecret(ctx context.Context) (string, error) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return "", status.Error(codes.Unauthenticated, "missing authorization metadata")
	}
	values := md.Get("authorization")
	if len(values) == 0 {
		return "", status.Error(codes.Unauthenticated, "missing authorization metadata")
	}
	secret, found := strings.CutPrefix(values[0], "Bearer ")
	if !found {
		return "", status.Error(codes.Unauthenticated, "invalid token")
	}
	return secret, nil
}

// requestFieldLookup adapts a request message to authz.FieldLookup using
// protoreflect, so the scope check needs no per-RPC code and no proto change:
// the field names a scope confines are already on the wire.
//
// A request that is not a proto message yields nil, which authz.AuthorizeRows
// treats as a denial for any confined key. That is the fail-closed case — an
// authorisation decision that cannot see the request is not a decision.
func requestFieldLookup(req any) authz.FieldLookup {
	msg, ok := req.(proto.Message)
	if !ok || msg == nil {
		return nil
	}
	m := msg.ProtoReflect()
	if !m.IsValid() {
		return nil
	}
	return func(field string) (string, []string, bool) {
		fd := m.Descriptor().Fields().ByName(protoreflect.Name(field))
		declared := fd != nil && fd.Kind() == protoreflect.StringKind && !fd.IsList() && !fd.IsMap()
		var top string
		if declared {
			top = m.Get(fd).String()
		}
		return top, collectNested(m, protoreflect.Name(field), nestedScanDepth), declared
	}
}

// collectNested gathers non-empty values of a string field with the given name
// from the populated sub-messages of m, skipping m's own top-level field.
//
// Nested copies are collected because a value carried inside, say, a
// RetrievalPlan's filters may be the one a handler ends up using. Rather than
// reason about which wins for each RPC, the policy rejects any nested value
// that disagrees with the key's confinement, which is correct whichever way the
// precedence goes.
func collectNested(m protoreflect.Message, field protoreflect.Name, depth int) []string {
	if depth <= 0 {
		return nil
	}
	var out []string
	m.Range(func(fd protoreflect.FieldDescriptor, v protoreflect.Value) bool {
		if fd.Kind() != protoreflect.MessageKind && fd.Kind() != protoreflect.GroupKind {
			return true
		}
		switch {
		case fd.IsMap():
			// Map values are caller-supplied metadata, not request structure;
			// nothing in cortexdb.v1 carries a scope field inside one.
			return true
		case fd.IsList():
			list := v.List()
			for i := range list.Len() {
				out = append(out, scopeValuesIn(list.Get(i).Message(), field, depth)...)
			}
		default:
			out = append(out, scopeValuesIn(v.Message(), field, depth)...)
		}
		return true
	})
	return out
}

// scopeValuesIn reads the named field off one sub-message and keeps descending.
func scopeValuesIn(m protoreflect.Message, field protoreflect.Name, depth int) []string {
	if !m.IsValid() {
		return nil
	}
	var out []string
	if fd := m.Descriptor().Fields().ByName(field); fd != nil &&
		fd.Kind() == protoreflect.StringKind && !fd.IsList() && !fd.IsMap() {
		if s := m.Get(fd).String(); s != "" {
			out = append(out, s)
		}
	}
	return append(out, collectNested(m, field, depth-1)...)
}
