package hubapp

// defaultAgentImage is the container image stamped into the seeded agent_type's
// orchestration block until an admin authors a real one. The hugen image is
// built in M3 (Dockerfile); HB4's DockerRuntime reads it back via
// agentmgr.ImageFromConfig(agent_type.config).
const defaultAgentImage = "hugen:latest"

// defaultAgentTypeSeedSQL seeds the default agent type into the Agent DB
// (hub.agent.db.agent_types — the hugen-owned canon). It is appended to the
// agent.db init DDL (InitDBSchemaTemplate) so hugr applies it straight to
// Postgres at schema-application time — NOT via a runtime GraphQL call, which
// would need hugr's HTTP endpoint up during _mount/init (it is not; see Init).
//
// Idempotent (ON CONFLICT DO NOTHING): an existing type — including an admin
// edit — is left untouched. The `config.orchestration` sub-block is hub-only
// spawn metadata (image/cpu/mem/mounts) that hugen's config loader ignores and
// HB4's DockerRuntime reads for the container spawn; the rest of `config` is the
// agent runtime config hugen reads via agent_info.
const defaultAgentTypeSeedSQL = `
INSERT INTO agent_types (id, name, description, config)
VALUES (
    'data-analyst',
    'Data Analyst',
    'Hugr data-exploration agent: discovery, query, and analysis skills over the mesh.',
    '{"orchestration":{"image":"` + defaultAgentImage + `"}}'::JSONB
)
ON CONFLICT (id) DO NOTHING;
`
