package views

// OpenPanelClientID is the OpenPanel client id the tracking snippet initialises
// with. The web package sets it at startup from the OPENPANEL_CLIENT_ID
// environment variable; it is exported so web can write it without a views→web
// import cycle, exactly like AssetVersion.
//
// Empty is the meaningful default, not a missing value: an unconfigured
// deployment renders no tracker at all. That is what keeps a self-hosted or
// local-mode install (and every test and dev run) free of a third-party beacon
// nobody asked for — analytics is opt-in by supplying the id.
//
// Only the client id lives here. OpenPanel's web SDK authenticates browser
// events with the id alone; the account's client SECRET belongs to the
// server-side/export API and is deliberately never read into the views package,
// because anything rendered into a document head is published to every visitor.
var OpenPanelClientID string

// openPanelScriptURL is the OpenPanel web SDK, loaded from the vendor's CDN.
// Pinning is not offered upstream — op1.js is the versionless entry point they
// document — so the tag matches their published snippet.
const openPanelScriptURL = "https://openpanel.dev/op1.js"

// openPanelScript returns the complete OpenPanel snippet — the SDK tag plus the
// init call — as a string, ready to be emitted with templ.Raw.
//
// It is built as a string rather than written as literal markup because templ
// treats the *contents* of a <script> tag as opaque text and never evaluates Go
// expressions inside it, so the client id could not be interpolated there. This
// is the same constraint (and the same workaround) as landingJSONLDScript.
//
// The id goes through jsString, so json.Marshal HTML-escapes it — "<" becomes
// the < escape, which means a value containing "</script>" cannot end the tag
// early and inject markup. The id comes from the operator's own environment
// rather than from a request, so this is defence in depth, not the boundary.
//
// The tracking options mirror OpenPanel's documented defaults for a
// server-rendered site: screen views are what a multi-page app needs (there is
// no client-side router to hook), outgoing links attribute referrals off the
// landing page, and attribute tracking lets a future data-track attribute work
// without touching this snippet again.
func openPanelScript(clientID string) string {
	return `<script src="` + openPanelScriptURL + `" defer async></script>` +
		`<script>window.op=window.op||function(...args){(window.op.q=window.op.q||[]).push(args)};` +
		`window.op('init',{clientId:` + jsString(clientID) +
		`,trackScreenViews:true,trackOutgoingLinks:true,trackAttributes:true});</script>`
}
