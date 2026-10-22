// Package sdk is the HTTP adapter of the SDK API, /sdk/v1: the models, the
// server interface and the client generated from api/sdk.yaml, the one
// contract SDKs and the server share. The client drives the server in
// tests through the same document the SDKs are written against.
package sdk

// The server interface and client of api/sdk.yaml.
//go:generate go tool oapi-codegen -config oapi-codegen.yaml ../../../../../api/sdk.yaml
