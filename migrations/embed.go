// Package migrations exists so the SQL files can be embedded in the binary.
//
// The go:embed directive cannot reach outside the directory of the file that
// declares it, and the migrations live at the repository root because that is
// where Atlas writes them. One file here is cheaper than moving the directory
// somewhere that suits the compiler more than it suits the reader.
//
// Atlas only ever writes .sql and atlas.sum in this directory, so this file is
// safe from `atlas migrate diff`.
package migrations

import "embed"

// FS holds every versioned migration, applied in filename order at startup
// (01-structure.md §1.8).
//
//go:embed *.sql
var FS embed.FS
