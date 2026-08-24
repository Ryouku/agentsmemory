// Package mcpprotocol owns wire constants shared by every agentsmemory MCP
// producer and consumer. It deliberately has no server or client dependencies.
package mcpprotocol

const (
	// ToolPrefix namespaces agentsmemory tools when several MCPs share a client.
	ToolPrefix = "am_"
	// WingHeader binds an MCP registration to its project wing.
	WingHeader = "X-Agentsmemory-Wing"
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
