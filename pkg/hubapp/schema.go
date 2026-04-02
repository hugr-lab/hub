package hubapp

import _ "embed"

//go:embed schema/init.sql
var hubDBSchema string

//go:embed schema/hub.graphql
var hubGraphQLSchema string
