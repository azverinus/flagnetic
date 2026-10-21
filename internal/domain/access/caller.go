package access

import (
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
)

// Method is how a caller proved who they are.
type Method string

// The methods.
const (
	MethodToken   Method = "token"
	MethodSession Method = "session"
)

// Caller is who makes a request to the management API: the credential in use
// and the member it acts for.
type Caller struct {
	Method Method
	// Token is the API token presented, for MethodToken.
	Token *APIToken
	// Member is the member the credential acts for; nil for a project token.
	Member *Member
}

// TokenCaller returns the caller presenting tok, held by holder (nil for a
// project token).
func TokenCaller(tok APIToken, holder *Member) Caller {
	return Caller{Method: MethodToken, Token: &tok, Member: holder}
}

// hasScope reports whether the caller's credential carries s. A session is
// narrowed by no scope.
func (c Caller) hasScope(s Scope) bool {
	return s == "" || c.Token == nil || c.Token.HasScope(s)
}

func (c Caller) isProjectToken() bool { return c.Token != nil && c.Token.Kind == TokenProject }

// Operation is what one operation of the management API requires, as its
// OpenAPI document declares it.
type Operation struct {
	// Name is the operation's identifier in the document, for counting its
	// refusals.
	Name string
	// Requirement is a catalog permission or a reserved value.
	Requirement Requirement
	// SessionRequired operations refuse every API token, so a leaked token
	// can neither mint another credential nor lock its member out.
	SessionRequired bool
	// Read operations change nothing.
	Read bool
	// Scope is the scope a token needs, as ScopeOf derives it; "" for none.
	Scope Scope
	// OpenToProjectTokens marks the one kind of operation outside a project
	// a project token reaches: reading which token it is. Everywhere else a
	// project token reaches its own project only.
	OpenToProjectTokens bool
}

// Authorize judges an authenticated caller against op, in the order every
// refusal is explained: a project token outside its reach, then a write that
// needs a session, then a scope the token lacks, then the requirement
// itself. A public operation admits anyone.
//
// An operation requiring a catalog permission is judged in its project,
// which this does not look up: a project token is let through to that
// judgement, and the operation itself is refused here, so none is ever open
// before its project's grants are judged.
func Authorize(c Caller, op Operation) error {
	if op.Requirement == RequirePublic {
		return nil
	}
	if c.isProjectToken() && op.Requirement.reserved() && !op.OpenToProjectTokens {
		return apperrors.Forbidden("forbidden", "a project token reaches only its project")
	}
	if op.SessionRequired && c.Method != MethodSession {
		return apperrors.Forbidden("session_required", "this operation needs a signed-in session; an API token cannot perform it")
	}
	if !c.hasScope(op.Scope) {
		return apperrors.Forbidden("insufficient_scope", "the API token does not carry the scope "+string(op.Scope))
	}
	switch op.Requirement {
	case RequireMember:
		return nil
	case RequireInstanceAdmin:
		if c.Member == nil || !c.Member.InstanceAdmin {
			return apperrors.Forbidden("forbidden", "this operation needs an instance admin").
				WithExtension("permission", string(RequireInstanceAdmin))
		}
		return nil
	}
	return apperrors.Forbidden("forbidden", "this operation needs the permission "+string(op.Requirement)+" in its project").
		WithExtension("permission", string(op.Requirement))
}
