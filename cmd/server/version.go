// Package main builds the agentsmemory server binary and its read-only CLI.
package main

// version is stamped at build time via -ldflags "-X main.version=<tag>". The
// dev default never matches a release tag, so a build made without the stamp is
// honest about being a dev build — the same convention as the aiagentmemory CLI
// (clients/claude-code/main.go), whose release binary carries the real tag.
var version = "dev"
