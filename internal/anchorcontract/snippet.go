// Package anchorcontract owns the cross-process representation rules for code
// anchors. Both the server that fingerprints snippets and the client that finds
// them in a working tree must use these exact rules.
package anchorcontract

import "strings"

// NormalizeSnippet collapses whitespace so formatting-only changes do not make
// a code anchor appear stale.
func NormalizeSnippet(text string) string {
	return strings.Join(strings.Fields(text), " ")
}
