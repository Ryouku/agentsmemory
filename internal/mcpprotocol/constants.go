// Package mcpprotocol owns wire constants shared by every agentsmemory MCP
// producer and consumer. It deliberately has no server or client dependencies.
package mcpprotocol

const (
	// ToolPrefix namespaces agentsmemory tools when several MCPs share a client.
	ToolPrefix = "am_"
	// WingHeader binds an MCP registration to its project wing.
	WingHeader = "X-Agentsmemory-Wing"
)
