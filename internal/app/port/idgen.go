package port

// IDGen is randomness as a dependency. Ids come from the client (UUID v7), but
// the server generates one when the client sends none. A deterministic
// generator in tests makes responses comparable against golden files
// (01-structure.md §1.6).
type IDGen interface {
	New() string
}
