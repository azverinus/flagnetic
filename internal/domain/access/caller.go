package access

import (
	"slices"

	"github.com/azverinus/flagnetic/internal/domain/workspace"
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
	// Session is the session signed in to, for MethodSession.
	Session *Session
	// Member is the member the credential acts for; nil for a project token.
	Member *Member
}

// SessionCaller returns the caller signed in to s as holder.
func SessionCaller(s Session, holder Member) Caller {
	return Caller{Method: MethodSession, Session: &s, Member: &holder}
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

// InProject reports whether op requires a catalog permission, which is
// judged in the project the operation addresses.
func (op Operation) InProject() bool { return !op.Requirement.reserved() }

// ProjectAccess is what the caller holds in the project an operation
// addresses, as the seam found it before judging the operation.
type ProjectAccess struct {
	// Project is the project addressed; zero when no project has the key the
	// request names.
	Project workspace.ProjectID
	// Permissions are the caller's effective permissions there.
	Permissions []Permission
}

// Authorize judges an authenticated caller against op, in the order every
// refusal is explained: a project token outside its reach, then a write that
// needs a session, then a scope the token lacks, then an unknown project,
// then the requirement itself. A public operation admits anyone.
//
// in is what the caller holds in the project an operation requiring a
// catalog permission addresses; without it such an operation is refused, so
// none is ever open before its project's grants are judged.
func Authorize(c Caller, op Operation, in *ProjectAccess) error {
	if op.Requirement == RequirePublic {
		return nil
	}
	if c.isProjectToken() && !c.reaches(op, in) {
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
	if in != nil && in.Project.IsZero() {
		return ErrProjectNotFound
	}
	if in == nil || !slices.Contains(in.Permissions, Permission(op.Requirement)) {
		return apperrors.Forbidden("forbidden", "this operation needs the permission "+string(op.Requirement)+" in its project").
			WithExtension("permission", string(op.Requirement))
	}
	return nil
}

// ErrProjectNotFound answers an operation addressing a project no project's
// key names. It is answered before any permission is judged, so a caller
// tells an unknown project from one they may not touch only by its key.
var ErrProjectNotFound = apperrors.NotFound("not_found", "no project has this key")

// reaches reports whether a project token reaches op: reading which token
// it is, or an operation in its own project. An unknown project is answered
// like another project's, so the token learns nothing of which keys exist.
func (c Caller) reaches(op Operation, in *ProjectAccess) bool {
	if op.Requirement.reserved() {
		return op.OpenToProjectTokens
	}
	return in != nil && !in.Project.IsZero() && in.Project == c.Token.Project
}

// EffectivePermissions returns what c holds in project, in catalog order,
// given the roles of the grants c's member holds there. An instance admin
// holds every permission without a grant; a member, the union of their
// grants' roles; a project token, its role in its own project, without the
// permissions that change who has access. A token's permissions are further
// narrowed to those whose scope it carries.
func EffectivePermissions(c Caller, project workspace.ProjectID, grants []RoleName) []Permission {
	var held func(Permission) bool
	switch {
	case c.isProjectToken():
		if c.Token.Project != project {
			return nil
		}
		role, _ := RoleNamed(c.Token.Role)
		held = func(p Permission) bool { return slices.Contains(role.Permissions, p) }
	case c.Member == nil:
		return nil
	case c.Member.InstanceAdmin:
		held = func(Permission) bool { return true }
	default:
		held = func(p Permission) bool {
			for _, g := range grants {
				if role, ok := RoleNamed(g); ok && slices.Contains(role.Permissions, p) {
					return true
				}
			}
			return false
		}
	}
	var out []Permission
	for _, p := range catalog {
		if !held(p.Name) || !c.hasScope(p.Scope) || (p.MembersOnly && c.isProjectToken()) {
			continue
		}
		out = append(out, p.Name)
	}
	return out
}
