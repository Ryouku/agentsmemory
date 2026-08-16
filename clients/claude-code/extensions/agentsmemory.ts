/**
 * agentsmemory bridge for the pi coding agent.
 *
 * pi ships no MCP client on purpose — "intentionally does not include built-in
 * MCP" (pi docs/usage.md) — and it retired hooks in favour of extensions. This
 * one extension therefore does both jobs the Claude/codex installs get for free:
 *
 *   1. It speaks the remote agentsmemory MCP (Streamable HTTP + bearer) directly
 *      and re-registers every server-side tool as a native pi tool, so `am_*`
 *      calls in the memory protocol work verbatim.
 *   2. It fires the end-of-turn memory checkpoint that the Stop hook fires on the
 *      other two agents.
 *
 * The installer writes this file into <PI_CODING_AGENT_DIR>/extensions/, where pi
 * auto-discovers it, and persists the endpoint + token in agentsmemory.env beside
 * it; `aiagentmemory run --agent pi <sandbox>` exports both before exec'ing pi.
 *
 * Configuration (all optional — a missing token disables the bridge quietly,
 * unless AGENTSMEMORY_LOCAL says the server wants none):
 *   AGENTSMEMORY_MCP_URL    remote MCP endpoint (default https://aiagentmemory.dev/mcp)
 *   AGENTSMEMORY_TOKEN      workspace bearer token
 *   AGENTSMEMORY_LOCAL      1 = self-hosted `agentsmemory --local`: connect unauthenticated
 *   AGENTSMEMORY_STOP_HOOK  on (default) | once | off — end-of-turn checkpoint
 */

// Type-only import: jiti erases it, so the extension still loads when pi's own
// package is not resolvable from the sandbox config dir.
import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";

const DEFAULT_MCP_URL = "https://aiagentmemory.dev/mcp";
const PROTOCOL_VERSION = "2025-06-18";
const CLIENT_INFO = { name: "agentsmemory-pi-bridge", version: "1" };

/** Request timeout. Long enough for a cold serverless start, short enough that a
 *  hung endpoint never wedges pi's startup or a tool call. */
const REQUEST_TIMEOUT_MS = 60_000;

/** The checkpoint the agent sees when a turn settles — same three writes the
 *  Stop hook asks for on Claude and codex, so the protocol reads identically. */
const CHECKPOINT = [
  "agentsmemory checkpoint — persist this session into team memory before stopping:",
  "  1. am_diary_write — an AAAK session summary (what changed, why, open threads).",
  "  2. am_kg_add      — new durable facts as subject -> predicate -> object triples.",
  "  3. am_add_drawer  — notable decisions / code, verbatim, into the right wing + room.",
  "Use the agentsmemory tools (am_ prefix). Skip only if nothing was worth",
  "remembering — and say so. Disable with AGENTSMEMORY_STOP_HOOK=off (or =once).",
].join("\n");

type JsonRpcResponse = {
  result?: Record<string, unknown>;
  error?: { code: number; message: string };
};

type McpTool = {
  name: string;
  description?: string;
  inputSchema?: Record<string, unknown>;
};

/**
 * McpClient is a minimal Streamable-HTTP MCP client: one POST per JSON-RPC
 * message, no persistent stream. That is all this bridge needs — we list tools
 * once at startup and call them on demand — and it keeps the extension free of
 * any dependency pi does not already ship.
 */
class McpClient {
  private sessionId?: string;
  private readonly url: string;
  private readonly token: string;

  // Plain assignments rather than TypeScript parameter properties: pi loads this
  // file through jiti, but a strip-only TS loader (node --experimental-strip-types,
  // and anything else that erases types without transforming) rejects that sugar.
  constructor(url: string, token: string) {
    this.url = url;
    this.token = token;
  }

  /** initialize performs the MCP handshake and returns the server's tool list. */
  async initialize(): Promise<McpTool[]> {
    await this.request("initialize", {
      protocolVersion: PROTOCOL_VERSION,
      capabilities: {},
      clientInfo: CLIENT_INFO,
    });
    // The spec requires this notification before normal operation; it carries no
    // id and the server answers 202 with no body, so nothing is parsed.
    await this.notify("notifications/initialized");

    // tools/list is paginated. Follow every cursor: a workspace with many tools
    // would otherwise silently expose only the first page.
    const tools: McpTool[] = [];
    let cursor: string | undefined;
    do {
      const page = await this.request("tools/list", cursor ? { cursor } : {});
      tools.push(...((page.tools as McpTool[] | undefined) ?? []));
      cursor = page.nextCursor as string | undefined;
    } while (cursor);
    return tools;
  }

  /** call invokes one remote tool and returns its raw MCP result. */
  async call(name: string, args: unknown, signal?: AbortSignal): Promise<Record<string, unknown>> {
    return this.request("tools/call", { name, arguments: args ?? {} }, signal);
  }

  /** request sends one JSON-RPC call and unwraps its result. */
  private async request(
    method: string,
    params: unknown,
    signal?: AbortSignal,
  ): Promise<Record<string, unknown>> {
    const res = await this.post({ jsonrpc: "2.0", id: nextId(), method, params }, signal);
    const body = await readMessage(res);
    if (body?.error) {
      throw new Error(`${method}: ${body.error.message} (code ${body.error.code})`);
    }
    return body?.result ?? {};
  }

  /** notify sends a JSON-RPC notification, which has no id and no response. */
  private async notify(method: string): Promise<void> {
    const res = await this.post({ jsonrpc: "2.0", method }, undefined);
    // Drain the body so the connection can be reused; a 202 has nothing in it.
    await res.text().catch(() => "");
  }

  private async post(payload: unknown, signal?: AbortSignal): Promise<Response> {
    const headers: Record<string, string> = {
      "content-type": "application/json",
      // Streamable HTTP servers may answer either way; accept both so a server
      // that streams its reply as SSE is handled by readMessage below.
      accept: "application/json, text/event-stream",
      "mcp-protocol-version": PROTOCOL_VERSION,
    };
    // A self-hosted `agentsmemory --local` server takes no credential, so no
    // header is sent at all rather than an empty bearer that reads like auth.
    if (this.token) {
      headers.authorization = `Bearer ${this.token}`;
    }
    if (this.sessionId) {
      headers["mcp-session-id"] = this.sessionId;
    }

    const res = await fetch(this.url, {
      method: "POST",
      headers,
      body: JSON.stringify(payload),
      signal: signal ?? AbortSignal.timeout(REQUEST_TIMEOUT_MS),
    });
    if (!res.ok) {
      throw new Error(`agentsmemory MCP ${res.status} ${res.statusText}`);
    }
    // A stateful server hands out its session id on initialize and expects it
    // back on every later request; a stateless one never sets the header.
    const sid = res.headers.get("mcp-session-id");
    if (sid) {
      this.sessionId = sid;
    }
    return res;
  }
}

let requestId = 0;
function nextId(): number {
  return ++requestId;
}

/**
 * readMessage parses one JSON-RPC message out of a response that may be plain
 * JSON or a short SSE stream. Reading the body to completion (rather than
 * consuming the stream frame by frame) is safe here because every call is a
 * single request/response pair the server closes after answering.
 */
async function readMessage(res: Response): Promise<JsonRpcResponse | undefined> {
  const text = await res.text();
  if (!text.trim()) {
    return undefined;
  }
  if ((res.headers.get("content-type") ?? "").includes("text/event-stream")) {
    // Take the last data: payload — an SSE reply may be preceded by comments or
    // keep-alive frames, and the response we want is the final one.
    const payloads = text
      .split("\n")
      .filter((line) => line.startsWith("data:"))
      .map((line) => line.slice(5).trim())
      .filter((line) => line && line !== "[DONE]");
    const last = payloads.at(-1);
    return last ? (JSON.parse(last) as JsonRpcResponse) : undefined;
  }
  return JSON.parse(text) as JsonRpcResponse;
}

/**
 * toPiResult maps an MCP tool result onto pi's tool-result shape. Text content
 * passes through; anything else (images, embedded resources) is rendered as JSON
 * so no part of the answer is silently dropped. An MCP-level error becomes a
 * thrown error, which is how pi reports a failed tool call.
 */
function toPiResult(result: Record<string, unknown>) {
  const content = (result.content as Array<Record<string, unknown>> | undefined) ?? [];
  const parts = content.map((item) =>
    item.type === "text" && typeof item.text === "string"
      ? { type: "text" as const, text: item.text }
      : { type: "text" as const, text: JSON.stringify(item) },
  );
  if (result.isError) {
    throw new Error(parts.map((p) => p.text).join("\n") || "agentsmemory tool call failed");
  }
  return {
    content: parts.length > 0 ? parts : [{ type: "text" as const, text: "" }],
    details: result,
  };
}

export default async function (pi: ExtensionAPI) {
  registerCheckpoint(pi);

  const token = process.env.AGENTSMEMORY_TOKEN?.trim();
  const url = process.env.AGENTSMEMORY_MCP_URL?.trim() || DEFAULT_MCP_URL;
  // A self-hosted server (installed with --local) authenticates nobody, so a
  // missing token there means "none is wanted" — not "the user skipped it".
  // Without this distinction we would either stay silent against a local server
  // that would have worked, or 401 repeatedly against the hosted one.
  const local = process.env.AGENTSMEMORY_LOCAL?.trim() === "1";
  if (!token && !local) {
    // No token is a normal state (the user skipped it at install), not a failure:
    // say so once per session and leave pi otherwise untouched.
    announce(pi, "agentsmemory: no AGENTSMEMORY_TOKEN — memory tools are off", "info");
    return;
  }

  const client = new McpClient(url, token ?? "");
  let tools: McpTool[];
  try {
    tools = await client.initialize();
  } catch (err) {
    // Startup must never fail on a network hiccup: pi awaits this factory, so a
    // throw here would cost the user their whole session over an unreachable API.
    announce(pi, `agentsmemory: MCP unavailable (${errorText(err)})`, "error");
    return;
  }

  for (const tool of tools) {
    pi.registerTool({
      name: tool.name,
      label: tool.name,
      description: tool.description ?? `agentsmemory tool ${tool.name}`,
      // The remote schema is plain JSON Schema rather than a TypeBox type; pi
      // validates non-TypeBox schemas through its JSON-Schema path, so it is
      // handed over untouched instead of being lossily re-declared here.
      parameters: (tool.inputSchema as never) ?? ({ type: "object", properties: {} } as never),
      async execute(_toolCallId: string, params: unknown, signal?: AbortSignal) {
        return toPiResult(await client.call(tool.name, params, signal));
      },
    } as never);
  }

  announce(pi, `agentsmemory: ${tools.length} memory tools ready`, "info");
}

/**
 * registerCheckpoint reproduces the Stop hook on pi. It nudges once per user
 * turn: the flag is raised when the nudge is sent and lowered only when a fresh
 * user message arrives, so the turn our own message triggers cannot re-trigger
 * it — the same loop prevention `stop_hook_active` gives the hook script.
 */
function registerCheckpoint(pi: ExtensionAPI) {
  const mode = (process.env.AGENTSMEMORY_STOP_HOOK ?? "on").trim().toLowerCase();
  if (mode === "off") {
    return;
  }

  // nudged gates the current turn; everNudged latches "once" mode for the whole
  // session. Two flags rather than one because they answer different questions.
  let nudged = false;
  let everNudged = false;

  pi.on("message_start", async (event: { message?: { role?: string } }) => {
    if (event.message?.role === "user") {
      nudged = false;
    }
  });

  pi.on("agent_settled", async () => {
    if (nudged || (mode === "once" && everNudged)) {
      return;
    }
    nudged = true;
    everNudged = true;
    pi.sendMessage(
      { customType: "agentsmemory-checkpoint", content: CHECKPOINT, display: true },
      { deliverAs: "followUp", triggerTurn: true },
    );
  });
}

/** announce shows a one-line status at session start, where a UI context exists.
 *  The extension factory itself has no ctx, so the message waits for the event. */
function announce(pi: ExtensionAPI, message: string, level: "info" | "error") {
  pi.on("session_start", async (_event: unknown, ctx: { ui: { notify: (m: string, l: string) => void } }) => {
    ctx.ui.notify(message, level);
  });
}

/** errorText renders an unknown thrown value as a single readable line. */
function errorText(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}
