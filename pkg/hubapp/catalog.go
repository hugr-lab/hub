package hubapp

// registerCatalog registers all airport-go scalar/table/mutating functions in the CatalogMux.
// Read functions land under `function { hub { ... } }` (or, for table functions, `hub { ... }`),
// mutating ones under `mutation { function { hub { ... } } }`.
//
// HB6 store-prune (2026-07-06): the ADK-era transcript/memory surface was
// removed — memory_search / registry_search / agent_runtime (catalog),
// agent_capabilities (handlers_catalog), and the conversation mutations
// (handlers_conversation) are gone. Their tables die with the platform-DB
// prune; the agent's real store lives in the Agent DB and its live view is
// served by hugen's own protocol.
func (a *HubApp) registerCatalog() error {
	if err := a.registerReadFunctions(); err != nil {
		return err
	}
	if err := a.registerAgentMutations(); err != nil {
		return err
	}
	if err := a.registerAgentInfo(); err != nil {
		return err
	}
	if err := a.registerAgentBootstrap(); err != nil {
		return err
	}
	return nil
}
