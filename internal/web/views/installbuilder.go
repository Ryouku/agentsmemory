package views

import (
	"strings"

	"github.com/atvirokodosprendimai/agentsmemory/internal/mcpprotocol"
)

// The install-command builder, shared by the public landing section and the
// dashboard's revealed-key block. Both ask the same question — where should the
// kit land, for which agent, with which flags — so they assemble the command from
// one set of expressions rather than each hardcoding a variant that drifts.
//
// The two mounts differ in exactly two ways, which is why installBuilder is a
// struct and not a package of constants:
//
//   - SIGNAL NAMESPACE. Datastar signals are global to a page, and the dashboard
//     renders one KeyBlock per project, several of which can be revealed at once
//     (KeyBlock's own doc comment warns about precisely this for its indicator).
//     Two builders sharing a bare "_sbname" would mean typing a name under one
//     project silently rewrote another project's command. Every signal therefore
//     carries a per-mount suffix.
//   - THE TOKEN. The dashboard renders beside the decrypted key, so it can embed
//     AGENTSMEMORY_TOKEN and hand over a command that never prompts. A public page
//     has no token to embed and says so in copy instead (landingTokenNote).

// installBuilder is one mounted instance of the picker.
type installBuilder struct {
	// Suffix is appended to every signal name so two mounts on one page cannot
	// share state. Empty on the landing page, where only one builder exists.
	Suffix string
	// Token, when set, is embedded in the command as AGENTSMEMORY_TOKEN so the
	// install runs non-interactively. It is only ever set where the secret is
	// already on screen, so embedding it exposes nothing new.
	Token string
}

// landingBuilder is the public page's mount: one per page, so it needs no suffix,
// and no token exists to embed.
func landingBuilder() installBuilder { return installBuilder{} }

// projectBuilder is the dashboard's mount for one workspace. The suffix is the
// team id with everything but letters and digits removed — a UUID's dashes are not
// valid in a JavaScript identifier, and the signal names are read as identifiers.
func projectBuilder(teamID, token string) installBuilder {
	return installBuilder{Suffix: "_" + identSuffix(teamID), Token: token}
}

// identSuffix reduces s to the characters that are safe inside a JS identifier.
func identSuffix(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		}
		return -1
	}, s)
}

// sig returns the page-unique name of one of this builder's signals.
func (b installBuilder) sig(name string) string { return name + b.Suffix }

// ref returns a signal reference for use inside a datastar expression.
func (b installBuilder) ref(name string) string { return "$" + b.sig(name) }

// Signals is the data-signals block declaring every signal this builder drives.
// All are front-end-only (the "_" prefix keeps them off the wire): the whole
// interaction is a text substitution, so nothing here is worth a round trip.
//
// _copiedKey is deliberately NOT suffixed. It is the shared keyed-flash signal
// (see clipboardExprKey): buttons distinguish themselves by the key they write
// into it, so one signal serves every command block on the page and only the
// clicked button confirms. Re-declaring it per mount is harmless — each just
// initialises it to the same empty string.
func (b installBuilder) Signals() string {
	return "{" + b.sig("_copiedInstall") + ": false, _copiedKey: '', " +
		b.sig("_plat") + ": '" + platUnix + "', " +
		b.sig("_copiedWin") + ": false, " +
		b.sig("_mode") + ": 'global', " +
		b.sig("_agent") + ": 'claude', " +
		b.sig("_sbname") + ": '', " +
		b.sig("_optcopy") + ": false, " +
		b.sig("_optshared") + ": false, " +
		b.sig("_optrec") + ": false}"
}

// The two values of the platform axis, the outer question the builder now asks.
// It sits above the mode tabs because it decides whether those tabs apply at all:
// an install with no CLI has nowhere to land but the user's own config, so there
// is no global-vs-sandbox choice left to make.
const (
	platUnix = "unix" // macOS, Linux, WSL — the bash installer runs
	platWin  = "win"  // Windows or any editor-only client — no installer at all
)

// PlatIs tests which platform tab is active. It gates the entire CLI half of the
// builder, so a reader on Windows is never shown a curl line that cannot run on
// their machine.
func (b installBuilder) PlatIs(p string) string {
	return b.ref("_plat") + " === '" + p + "'"
}

// SetPlat is the click expression a platform tab runs.
func (b installBuilder) SetPlat(p string) string {
	return b.ref("_plat") + " = '" + p + "'"
}

// ModeIs is the expression that tests which tab is active, used both to gate
// controls (data-show) and to light the tab itself.
func (b installBuilder) ModeIs(mode string) string {
	return b.ref("_mode") + " === '" + mode + "'"
}

// SetMode is the click expression a tab button runs.
func (b installBuilder) SetMode(mode string) string {
	return b.ref("_mode") + " = '" + mode + "'"
}

// NameExpr yields the sandbox name to splice into a command. It hyphenates
// whitespace and then strips everything outside [A-Za-z0-9._-] rather than
// trusting the field: the page hands this line to the visitor's own shell, so it
// must never render one that does something other than install. Typing
// "my project" is the case this is really for; "foo; rm -rf ~" is the case it must
// not get wrong.
func (b installBuilder) NameExpr() string {
	return "(" + b.ref("_sbname") + `.trim().replace(/\s+/g, '-').replace(/[^A-Za-z0-9._-]/g, '') || '` +
		landingNameFallback + "')"
}

// agentFlag is the --agent argument for the installer, which accepts the
// multi-agent values. Claude is the installer's default, so selecting it adds
// nothing — the shortest correct command is the one worth showing.
func (b installBuilder) agentFlag() string {
	return "(" + b.ref("_agent") + " === 'claude' ? '' : ' --agent ' + " + b.ref("_agent") + ")"
}

// launchAgentFlag is agentFlag for `run` and `init`, which resolve a single kit
// and reject "all" (resolveAgentKit in clients/claude-code/agentkit.go). An
// `--agent all` sandbox is one directory three CLIs can open, so the launch
// commands omit the flag and open it as Claude.
func (b installBuilder) launchAgentFlag() string {
	return "(" + b.ref("_agent") + " === 'claude' || " + b.ref("_agent") +
		" === 'all' ? '' : ' --agent ' + " + b.ref("_agent") + ")"
}

// base is the invariant head of the command: fetch the script and pipe it into a
// bash that carries the token when there is one to carry.
//
// The token is interpolated into a double-quoted shell word without escaping,
// which is safe only because tenant.GenerateToken returns hex — 64 characters of
// [0-9a-f], with no quote to break out of. That is an invariant of another
// package, so TestTokenAlphabetIsShellSafe asserts it rather than leaving this
// line to discover a format change the hard way. Escaping here instead would be
// defending against a state the system cannot currently reach.
func (b installBuilder) base() string {
	cmd := "curl -fsSL " + installScriptURL + " | "
	if b.Token != "" {
		cmd += mcpprotocol.TokenEnvVar + `="` + b.Token + `" `
	}
	return cmd + "bash"
}

// Default is the command as the untouched builder renders it, emitted on the
// server so the block reads correctly before datastar boots and data-text takes
// over — and so it still reads correctly if datastar never arrives.
func (b installBuilder) Default() string { return b.base() + " -s -- --global" }

// InstallExpr is the datastar expression behind the command block — the single
// source of truth for what the page displays AND what its Copy button writes, so
// the two cannot drift apart.
//
// Both branches always forward arguments, so `-s --` is unconditional. The global
// branch spells out --global rather than relying on a bare pipe: with neither
// --global nor --sandbox the installer stops and asks which mode you want, and a
// tab that says "Global" must not then ask the question the tab already answered.
func (b installBuilder) InstallExpr() string {
	sandboxFlags := " ' -s --' + " + b.agentFlag() +
		" + ' --sandbox ' + " + b.NameExpr() +
		" + (" + b.ref("_optcopy") + " ? ' --copy' : '')" +
		" + (" + b.ref("_optshared") + " ? ' --shared-auth' : '')" +
		" + (" + b.ref("_optrec") + " ? ' --recommended' : '')"
	globalFlags := " ' -s -- --global' + " + b.agentFlag() +
		" + (" + b.ref("_optrec") + " ? ' --recommended' : '')"
	return jsString(b.base()) + " + (" + b.ModeIs("project") + " ?" + sandboxFlags + " :" + globalFlags + ")"
}

// RunExpr is the command that opens the sandbox once it exists. The dashboard
// shows only this one follow-up: a signed-in user needs the launch line, not the
// init/load onboarding the landing page walks through.
func (b installBuilder) RunExpr() string {
	return jsString("aiagentmemory run") + " + " + b.launchAgentFlag() + " + ' ' + " + b.NameExpr()
}

// LaunchSteps is the "now use it" strip that follows a sandboxed install: open it,
// pin the repository to it, then the single command everyone on the team runs
// afterwards. Ordered by when a reader needs them, and built from the same signals
// as the install command, so the name typed above is already in them.
func (b installBuilder) LaunchSteps() []launchStep {
	return []launchStep{
		{
			Key:   "run",
			Label: "open it",
			Expr:  b.RunExpr(),
			Text:  "aiagentmemory run " + landingNameFallback,
			Desc:  "Starts the agent with this sandbox's config, commands, MCP and token. Your global install is untouched.",
		},
		{
			Key:   "init",
			Label: "pin this repo",
			Expr:  jsString("aiagentmemory init --sandbox ") + " + " + b.NameExpr() + " + " + b.launchAgentFlag(),
			Text:  "aiagentmemory init --sandbox " + landingNameFallback,
			Desc:  "Run it inside the project. It writes .aiagentmemory — commit that file. Append -- --model opus to record agent flags too.",
		},
		{
			Key:   "load",
			Label: "every day after",
			Expr:  jsString("aiagentmemory load"),
			Text:  "aiagentmemory load",
			Desc:  "Opens the pinned project from any subdirectory. Teammates run this same line against their own sandbox.",
		},
	}
}
