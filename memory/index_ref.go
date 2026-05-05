package memory

import "strings"

type IndexRef struct {
	Kind      IndexEntryKind `json:"kind"`
	Namespace Namespace      `json:"namespace"`
	Role      Role           `json:"role,omitzero"`
	Name      string         `json:"name,omitzero"`
	ID        string         `json:"id,omitzero"`
}

func (r IndexRef) Path() string {
	scope := sanitizeIndexSegment(string(r.Namespace.Scope))
	scopeID := sanitizeIndexSegment(r.Namespace.ID)
	switch r.Kind {
	case IndexBlock:
		return strings.Join([]string{
			scope,
			scopeID,
			string(RoleCore),
			sanitizeIndexSegment(r.Name),
		}, "/")
	case IndexMemory:
		leaf := sanitizeIndexSegment(r.Name)
		if leaf == "" {
			leaf = "memory-" + shortIndexID(r.ID)
		} else if r.ID != "" {
			leaf += "--" + shortIndexID(r.ID)
		}
		return strings.Join([]string{
			scope,
			scopeID,
			sanitizeIndexSegment(string(r.Role)),
			leaf,
		}, "/")
	default:
		return strings.Join([]string{scope, scopeID}, "/")
	}
}
