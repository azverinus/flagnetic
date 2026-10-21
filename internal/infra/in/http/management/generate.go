package management

// The server and client of api/management.yaml, then the operation table
// the permission seam reads, derived from the same document.
//go:generate go tool oapi-codegen -config oapi-codegen.yaml ../../../../../api/management.yaml
//go:generate go test ./operations -run ^TestTheOperationTableIsTheDocument$ -update
