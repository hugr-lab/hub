package hubapp

// defaultAgentImage is the container image stamped into the seeded agent_type's
// orchestration block until an admin authors a real one. The hugen image is
// built in M3 (Dockerfile); HB4's DockerRuntime reads it back via
// agentmgr.ImageFromConfig(agent_type.config).
const defaultAgentImage = "hugen:latest"

// defaultAgentTypeConfig is a PLACEHOLDER template for the seeded type: it shows
// the shape an admin fills in, but is intentionally not runnable as-is —
// `models.model` is empty (hugen boots only once it is set), so the console's
// config editor flags it. `orchestration` is hub-only spawn metadata (image +
// resource caps) that hugen ignores; `models`/`skills`/… are the runtime config
// hugen reads. `skills: {}` leaves install unset → hugen installs every bundled
// skill. `models.routes.cheap` is stubbed because the ModelRouter requires a
// `cheap` intent at boot. A curated, ready-to-run example (e.g. `hugen-analyst`)
// is added to a deployment by hand, not seeded here.
const defaultAgentTypeConfig = `{
  "orchestration": { "image": "` + defaultAgentImage + `", "memory_bytes": 0, "nano_cpus": 0, "pids_limit": 0 },
  "models": { "mode": "remote", "model": "", "routes": { "cheap": { "mode": "remote", "model": "" } } },
  "skills": {}
}`

// defaultAgentTypeSeedSQL seeds the placeholder agent type into the Agent DB
// (hub.agent.db.agent_types — the hugen-owned canon). It is appended to the
// agent.db init DDL (InitDBSchemaTemplate) so hugr applies it straight to
// Postgres at schema-application time — NOT via a runtime GraphQL call, which
// would need hugr's HTTP endpoint up during _mount/init (it is not; see Init).
//
// Idempotent (ON CONFLICT DO NOTHING): an existing type — including an admin
// edit or a hand-added example — is left untouched.
const defaultAgentTypeSeedSQL = `
INSERT INTO agent_types (id, name, description, config)
VALUES (
    'data-analyst',
    'Data Analyst',
    'Placeholder agent type — set models.model (and review skills/tools) before provisioning.',
    '` + defaultAgentTypeConfig + `'::JSONB
)
ON CONFLICT (id) DO NOTHING;
`
