// Atlas is the authoring tool for migrations and lives in development and CI
// only. The production image never sees it: migrations/*.sql is embedded with
// go:embed and applied by the binary itself at startup (01-structure.md §1.8).

env "local" {
  // The schema as it should be. `atlas migrate diff` works out the SQL that
  // gets an existing database there.
  src = "file://schema.sql"

  // A scratch database Atlas uses to compute the diff. It is created and
  // dropped on every run and holds nothing of value.
  dev = "sqlite://dev?mode=memory&_fk=1"

  migration {
    dir = "file://migrations"
  }

  format {
    migrate {
      diff = "{{ sql . \"  \" }}"
    }
  }
}
