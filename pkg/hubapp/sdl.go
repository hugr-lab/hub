package hubapp

// hubGraphQLSchema is the GraphQL SDL for hub.* tables.
// Registered via DataSourceInfo.HugrSchema — Hugr compiles this into the GraphQL API.
// Template parameters:
//   - {{.EmbedderName}} — configured embedding model name
const hubGraphQLSchema = `
type users @table(name: "users") {
  id: String! @pk
  display_name: String!
  email: String
  hugr_role: String!
  profile: String
  last_login_at: Timestamp
  metadata: JSON
}

type agent_types @table(name: "agent_types") {
  id: String! @pk
  display_name: String!
  description: String
  image: String!
  capabilities: [String]
  skills: [String]
  tool_policy: JSON
  max_instances_per_user: Int
  idle_timeout_seconds: Int
  metadata: JSON
}

type agent_instances @table(name: "agent_instances") {
  id: ID! @pk
  user_id: String!
  agent_type_id: String!
  container_id: String
  status: String
  started_at: Timestamp
  last_activity_at: Timestamp
  metadata: JSON
  user: users @relation(fields: ["user_id"], references: ["id"])
  agent_type: agent_types @relation(fields: ["agent_type_id"], references: ["id"])
}

type agent_sessions @table(name: "agent_sessions") {
  id: ID! @pk
  user_id: String!
  instance_id: ID
  started_at: Timestamp
  ended_at: Timestamp
  metadata: JSON
  user: users @relation(fields: ["user_id"], references: ["id"])
  instance: agent_instances @relation(fields: ["instance_id"], references: ["id"])
  messages: [agent_messages] @relation(fields: ["id"], references: ["session_id"])
}

type agent_messages @table(name: "agent_messages") {
  id: ID! @pk
  session_id: ID!
  role: String!
  content: String!
  tool_calls: JSON
  tokens_used: Int
  model: String
  created_at: Timestamp
}

type agent_memory @table(name: "agent_memory") @embeddings(
  model: "{{.EmbedderName}}"
  vector: "embedding"
  distance: cosine
) {
  id: ID! @pk
  user_id: String!
  content: String!
  category: String
  source: String
  created_at: Timestamp
  user: users @relation(fields: ["user_id"], references: ["id"])
}

type query_registry @table(name: "query_registry") {
  id: ID! @pk
  user_id: String!
  name: String!
  query: String!
  description: String
  tags: [String]
  usage_count: Int
  created_at: Timestamp
  updated_at: Timestamp
  user: users @relation(fields: ["user_id"], references: ["id"])
}

type tool_calls @table(name: "tool_calls") {
  id: ID! @pk
  session_id: ID
  user_id: String!
  tool_name: String!
  arguments: JSON
  result_summary: String
  duration_ms: Int
  tokens_in: Int
  tokens_out: Int
  created_at: Timestamp
  session: agent_sessions @relation(fields: ["session_id"], references: ["id"])
}

type llm_providers @table(name: "llm_providers") {
  id: String! @pk
  provider: String!
  model: String!
  base_url: String
  api_key_ref: String
  max_tokens_per_request: Int
  enabled: Boolean
  metadata: JSON
}

type llm_budgets @table(name: "llm_budgets") {
  id: ID! @pk
  scope: String!
  provider_id: String
  period: String!
  max_tokens_in: BigInt
  max_tokens_out: BigInt
  max_requests: Int
  created_at: Timestamp
  provider: llm_providers @relation(fields: ["provider_id"], references: ["id"])
}

type llm_usage @table(name: "llm_usage") {
  id: ID! @pk
  user_id: String!
  provider_id: String!
  session_id: ID
  tokens_in: Int!
  tokens_out: Int!
  duration_ms: Int
  period_key: String!
  created_at: Timestamp
  provider: llm_providers @relation(fields: ["provider_id"], references: ["id"])
  session: agent_sessions @relation(fields: ["session_id"], references: ["id"])
}
`
