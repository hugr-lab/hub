package hubapp

import _ "embed"

//go:embed schema/init.sql
var hubDBSchema string

//go:embed schema/hub.graphql
var hubGraphQLSchema string

//go:embed schema/migrations/001_agents_streaming.sql
var migration001 string

//go:embed schema/migrations/002_channel_protocol.sql
var migration002 string

// migrations maps fromVersion → SQL to run. The key is the version that
// the database currently reports; the SQL brings it to appVersion.
var migrations = map[string]string{
	"0.1.0": migration001 + "\n" + migration002, // fresh → current via both migrations
	"0.2.0": migration002,                        // A+ → current via channel protocol only
}
