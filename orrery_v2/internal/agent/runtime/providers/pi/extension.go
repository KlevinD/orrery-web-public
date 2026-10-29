package pi

func piBridgeExtensionSource() string {
	return `import { readFileSync } from "node:fs";
import { complete } from "@earendil-works/pi-ai";
import { convertToLlm, serializeConversation, type ExtensionAPI } from "@earendil-works/pi-coding-agent";

type BridgeTool = {
  name: string;
  label?: string;
  description?: string;
  promptSnippet?: string;
  parameters: Record<string, unknown>;
};

type BridgeConfig = {
  base_url: string;
  auth_token: string;
  tools: BridgeTool[];
  tool_call_timeout_ms?: number;
  approval?: {
    enabled?: boolean;
    human_approval_tools?: string[];
  };
  diary?: {
    enabled?: boolean;
  };
};

function maybeLoadConfig(): BridgeConfig | undefined {
  const path = process.env.ORRERY_PI_BRIDGE_CONFIG;
  if (!path) return undefined;
  return JSON.parse(readFileSync(path, "utf8")) as BridgeConfig;
}

function bridgeHeaders(config: BridgeConfig): Record<string, string> {
  return {
    "content-type": "application/json",
    "authorization": "Bearer " + config.auth_token,
  };
}

function toolCallSignal(config: BridgeConfig, fallback: AbortSignal): AbortSignal {
  const timeout = Number(config.tool_call_timeout_ms || 0);
  if (Number.isFinite(timeout) && timeout > 0) {
    return AbortSignal.any([fallback, AbortSignal.timeout(Math.floor(timeout))]);
  }
  return fallback;
}

async function bridgeJSON(config: BridgeConfig, path: string, body?: Record<string, unknown>): Promise<any> {
  const response = await fetch(config.base_url + path, {
    method: body ? "POST" : "GET",
    headers: bridgeHeaders(config),
    body: body ? JSON.stringify(body) : undefined,
  });
  const payload = await response.json().catch(() => ({}));
  if (!response.ok) throw new Error(payload.error || ("Orrery diary bridge failed with " + response.status));
  return payload;
}

function installDiaryFlush(pi: ExtensionAPI, config: BridgeConfig) {
  if (!config.diary?.enabled || !config.base_url) return;
  let compactionSequence = 0;
  pi.on("session_before_compact", async (event: any, ctx: any) => {
    const requestedSequence = ++compactionSequence;
    const preparation = event?.preparation || {};
    const branchEntries = Array.isArray(event?.branchEntries) ? event.branchEntries : [];
    const lastEntry = branchEntries.length > 0 ? branchEntries[branchEntries.length - 1] : undefined;
    const revision = String(lastEntry?.id || preparation.firstKeptEntryId || "compaction") + ":" + String(preparation.tokensBefore || 0) + ":" + String(branchEntries.length);
    const sessionId = String(ctx.sessionManager.getSessionId() || "");
    if (!sessionId) return undefined;
    const started = await bridgeJSON(config, "/diary-flush-start", {
      session_id: sessionId,
      context_revision: revision,
      compaction_sequence: requestedSequence,
    }).catch(() => undefined);
    const sequence = Number(started?.compaction_sequence || 0);
    if (!sequence || !ctx.model) return undefined;
    const diary = await bridgeJSON(config, "/diary-config").catch(() => undefined);
    if (!diary?.enabled || !diary?.path || !diary?.date) return undefined;
    const auth = await ctx.modelRegistry.getApiKeyAndHeaders(ctx.model);
    if (!auth?.ok || !auth.apiKey) return undefined;

    const sourceMessages = [...(preparation.messagesToSummarize || []), ...(preparation.turnPrefixMessages || [])];
    const conversation = serializeConversation(convertToLlm(sourceMessages));

    const systemPrompt = "Agent diary memory flush turn.\nCapture durable memories from the current Agent context.\nStore diary content only in " + diary.path + ".\nIf that diary already exists, append new content only and never overwrite it.\nTreat long-term memory, Agent instructions, and other reference/bootstrap memory as read-only.\nDo not create timestamped or slugged diary variants.\nUse only the diary read and append tools exposed for this turn.\nIf there is nothing new and durable to store, reply NO_REPLY.\nDo not include hidden reasoning, private chain-of-thought, credentials, or unsupported claims.\n\nOriginal Agent instructions are read-only reference:\n" + ctx.getSystemPrompt();
    const prompt = "Pre-compaction diary maintenance for local date " + diary.date + ". The host-selected target is exactly " + diary.path + ". Record only new durable outcomes, decisions and stated rationale, deliverables and evidence, blockers and unresolved items, and next actions. If nothing new is durable, reply NO_REPLY.\n\n<conversation>\n" + conversation + "\n</conversation>";
    const messages: any[] = [{ role: "user", content: [{ type: "text", text: prompt }], timestamp: Date.now() }];
    const tools: any[] = [
      { name: "memory_diary_read", description: "Read only the host-selected canonical diary.", parameters: { type: "object", additionalProperties: false, properties: { path: { type: "string" } }, required: ["path"] } },
      { name: "memory_diary_append", description: "Append new durable Markdown only to the host-selected canonical diary.", parameters: { type: "object", additionalProperties: false, properties: { path: { type: "string" }, content: { type: "string" } }, required: ["path", "content"] } },
    ];

    const maxAttempts = Math.max(1, Math.min(3, Number(diary.max_attempts || 3)));
    const maxToolRounds = 4;
    const transcriptBytes = new TextEncoder().encode(conversation).byteLength;
    for (let attempt = 0; attempt < maxAttempts; attempt++) {
      const lease = await bridgeJSON(config, "/diary-flush-attempt", {
        session_id: sessionId,
        context_revision: revision,
        context_window_tokens: Number(diary.context_window_tokens || 0),
        reserve_tokens: Number(diary.reserve_tokens || 0),
        projected_tokens: Number(preparation.tokensBefore || 0),
        transcript_bytes: transcriptBytes,
        compaction_sequence: sequence,
      }).catch(() => undefined);
      if (!lease?.allowed) return undefined;
      for (let round = 0; round < maxToolRounds; round++) {
        try {
          const response = await complete(ctx.model, { systemPrompt, messages, tools }, {
            apiKey: auth.apiKey,
            headers: auth.headers,
            maxTokens: 2048,
            signal: event.signal,
            sessionId,
            maxRetries: 0,
          });
          messages.push(response);
          const text = response.content.filter((part: any) => part.type === "text").map((part: any) => String(part.text || "")).join("\n").trim();
          const calls = response.content.filter((part: any) => part.type === "toolCall");
          if (calls.length === 0 && text.toUpperCase() === "NO_REPLY") {
            await bridgeJSON(config, "/diary-flush-outcome", { status: "no_content", session_id: sessionId, context_revision: revision, date: diary.date, compaction_sequence: sequence });
            return undefined;
          }
          if (calls.length === 0) break;
          let readCompleted = false;
          for (const call of calls) {
            if (call.name !== "memory_diary_read" && call.name !== "memory_diary_append") continue;
            const payload = await bridgeJSON(config, "/diary-tool-call", {
              tool_call_id: call.id,
              tool: call.name,
              input: call.arguments || {},
              session_id: sessionId,
              context_revision: revision,
              compaction_sequence: sequence,
            });
            const output = payload.output || {};
            messages.push({ role: "toolResult", toolCallId: call.id, toolName: call.name, content: [{ type: "text", text: JSON.stringify(output) }], details: output, isError: false, timestamp: Date.now() });
            if (call.name === "memory_diary_append" && (output.status === "written" || output.status === "replayed")) return undefined;
            if (call.name === "memory_diary_read") readCompleted = true;
          }
          if (readCompleted) continue;
          break;
        } catch (error) {
          if (event.signal?.aborted) return undefined;
          break;
        }
      }
      await bridgeJSON(config, "/diary-flush-outcome", { status: "failed", session_id: sessionId, context_revision: revision, date: diary.date, compaction_sequence: sequence }).catch(() => undefined);
    }
    await bridgeJSON(config, "/diary-flush-outcome", { status: "exhausted", session_id: sessionId, context_revision: revision, date: diary.date, compaction_sequence: sequence }).catch(() => undefined);
    return undefined;
  });
}

function installProviderRequestCapture(pi: ExtensionAPI) {
  const captureUrl = process.env.ORRERY_PI_PROVIDER_REQUEST_CAPTURE_URL;
  if (!captureUrl) return;
  pi.on("before_provider_request", async (event: any) => {
    try {
      await fetch(captureUrl, {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify(event?.payload ?? null),
      });
    } catch (error) {
      console.error("Orrery PI provider request capture failed", error);
    }
  });
}

function installDynamicSystemPrompt(pi: ExtensionAPI) {
  pi.on("before_agent_start", async (event: any) => {
    const path = process.env.ORRERY_PI_SYSTEM_PROMPT_PATH;
    if (!path) return undefined;
    try {
      const prompt = readFileSync(path, "utf8").trim();
      if (!prompt) return undefined;
      return { systemPrompt: String(event?.systemPrompt || "") + "\n\n" + prompt };
    } catch (error) {
      console.error("Orrery PI system prompt load failed", error);
      return undefined;
    }
  });
}

function runtimeToolResultContent(output: any): any[] {
  const source = Array.isArray(output?.content) ? output.content : [];
  const content: any[] = [];
  for (const block of source) {
    if (block?.type === "text" && typeof block.text === "string" && block.text) {
      content.push({ type: "text", text: block.text });
      continue;
    }
    if (block?.type === "image" && typeof block.data === "string" && block.data && typeof block.mimeType === "string" && block.mimeType.startsWith("image/")) {
      content.push({ type: "image", data: block.data, mimeType: block.mimeType });
    }
  }
  if (content.length > 0) return content;
  return [{ type: "text", text: JSON.stringify(output) }];
}

function installRuntimeApprovalGate(pi: ExtensionAPI, config: BridgeConfig) {
  if (!config.approval?.enabled || !config.base_url) return;
  pi.on("tool_call", async (event: any, ctx: any) => {
    const toolName = String(event?.toolName || "").trim();
    if (!toolName || !requiresApproval(toolName, config.approval)) return undefined;
    try {
      const response = await fetch(config.base_url + "/approval", {
        method: "POST",
        headers: bridgeHeaders(config),
        body: JSON.stringify({
          tool_call_id: event?.toolCallId || "",
          tool: toolName,
          input: event?.input || {},
        }),
        signal: ctx?.signal,
      });
      const payload = await response.json().catch(() => ({}));
      if (!response.ok) {
        return { block: true, reason: payload.error || ("Orrery runtime approval failed with " + response.status) };
      }
      if (!payload.allow) {
        return { block: true, reason: payload.reason || "Blocked by Orrery runtime approval" };
      }
    } catch (error) {
      return { block: true, reason: error instanceof Error ? error.message : "Orrery runtime approval failed" };
    }
    return undefined;
  });
}

function normalizeApprovalToolName(toolName: string): string {
  let normalized = "";
  let separator = false;
  for (const character of toolName.trim().toLowerCase()) {
    const isSeparator = character === "-" || character === " " || character === "\t" || character === "\n" || character === "\r";
    if (isSeparator) {
      if (!separator && normalized) normalized += "_";
      separator = true;
      continue;
    }
    normalized += character;
    separator = false;
  }
  return normalized;
}

function requiresApproval(toolName: string, approval: BridgeConfig["approval"]): boolean {
  const normalized = normalizeApprovalToolName(toolName);
  if (!normalized) return false;
  return (approval?.human_approval_tools || []).some((candidate) => normalizeApprovalToolName(String(candidate)) === normalized);
}

export default function (pi: ExtensionAPI) {
  installDynamicSystemPrompt(pi);
  installProviderRequestCapture(pi);
  const config = maybeLoadConfig();
  if (!config) return;
  installDiaryFlush(pi, config);
  installRuntimeApprovalGate(pi, config);
  for (const tool of config.tools) {
    pi.registerTool({
      name: tool.name,
      label: tool.label || tool.name,
      description: tool.description || "Orrery runtime tool " + tool.name,
      promptSnippet: tool.promptSnippet || tool.description,
      parameters: tool.parameters as any,
      async execute(toolCallId, params, signal) {
        const response = await fetch(config.base_url + "/tool-call", {
          method: "POST",
          headers: bridgeHeaders(config),
          body: JSON.stringify({ tool_call_id: toolCallId, tool: tool.name, input: params }),
          signal: toolCallSignal(config, signal),
        });
        const payload = await response.json().catch(() => ({}));
        if (!response.ok) {
          throw new Error(payload.error || ("Orrery runtime tool " + tool.name + " failed with " + response.status));
        }
        const output = payload.output || {};
        return {
          content: runtimeToolResultContent(output),
          details: output,
        };
      },
    });
  }
}
`
}
