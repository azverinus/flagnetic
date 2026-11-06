// Package scim is the SCIM API, served under /scim/v2: the server generated
// from api/scim.yaml, the authentication of SCIM tokens in front of every
// operation, and the handlers translating requests into the identity
// context's use cases.
package scim

//go:generate go tool oapi-codegen -config oapi-codegen.yaml ../../../../../api/scim.yaml
