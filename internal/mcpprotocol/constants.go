// Package mcpprotocol owns wire constants shared by every agentsmemory MCP
// producer and consumer. It deliberately has no server or client dependencies.
package mcpprotocol

const (
	// ToolPrefix namespaces agentsmemory tools when several MCPs share a client.
	ToolPrefix = "am_"
	// WingHeader binds an MCP registration to its project wing.
	WingHeader = "X-Agentsmemory-Wing"
	// TokenEnvVar is the workspace bearer every MCP client presents. The server
	// CLI, the stdio proxy, and the installer all read this one name.
	TokenEnvVar = "AGENTSMEMORY_TOKEN"
	// LocalTokenEnvVar is --local's shared bearer. It is deliberately not
	// TokenEnvVar: a developer with a hosted workspace key exported would
	// otherwise find their local server silently demanding it. The installer
	// reads this same variable, so exporting it once configures both halves.
	LocalTokenEnvVar = "AGENTSMEMORY_LOCAL_TOKEN"
	// WingEnvVar is the process-level default wing a registration or stdio
	// proxy forwards when the caller did not pass one on the tool call.
	WingEnvVar = "AGENTSMEMORY_WING"
	// StarScopeSchemaExtension marks an optional wing property whose "*" value
	// deliberately widens a registration-scoped read to every visible wing.
	// Contract-axis adapters discover the class from tools/list through this
	// extension instead of maintaining a second list of handlers.
	StarScopeSchemaExtension = "x-agentsmemory-star-scope"
)

// StarScopeProperty adds the machine-readable star-scope contract to an MCP
// string property's JSON Schema. Its plain function signature is assignable to
// mcp.PropertyOption without making this wire-constant package depend on the
// server or client implementation.
func StarScopeProperty(schema map[string]any) {
	schema[StarScopeSchemaExtension] = true
}
