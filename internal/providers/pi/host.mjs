// The subprocess boundary is yip-owned; all model/auth/tool behavior uses Pi's SDK.
import { spawn } from "node:child_process";
import { realpathSync } from "node:fs";
import { constants as osConstants } from "node:os";
import { pathToFileURL } from "node:url";

export function emptyResources(sdk, instructions) {
  return {
    getExtensions: () => ({ extensions: [], errors: [], runtime: sdk.createExtensionRuntime() }),
    getSkills: () => ({ skills: [], diagnostics: [] }),
    getPrompts: () => ({ prompts: [], diagnostics: [] }),
    getThemes: () => ({ themes: [], diagnostics: [] }),
    getAgentsFiles: () => ({ agentsFiles: [] }),
    getSystemPrompt: () => undefined,
    getSystemPromptSource: () => undefined,
    getAppendSystemPrompt: () => [instructions],
    getAppendSystemPromptSources: () => [],
    extendResources() {},
    async reload() {},
  };
}

export async function discoverModels(sdk, credentials) {
  if (sdk.ModelRuntime) {
    const modelRuntime = await sdk.ModelRuntime.create({
      modelsPath: null, refreshOnCreate: false, allowModelNetwork: false,
      ...(credentials ? { credentials } : {}),
    });
    // getAvailable/checkAuth can resolve API-key shell helpers in current Pi.
    // listCredentials is explicitly non-secret metadata and never executes them.
    const configured = new Map((await modelRuntime.listCredentials()).map(c => [c.providerId, c.type]));
    const available = modelRuntime.getModels().filter(m => configured.has(m.provider));
    const subscription = model => configured.get(model.provider) === "oauth"
      && modelRuntime.getProvider(model.provider)?.auth.oauth?.isSubscription === true;
    return { available, subscription, sessionOptions: { modelRuntime } };
  }
  const authStorage = sdk.AuthStorage.create();
  if (authStorage.drainErrors().length) throw new Error("Pi could not load local authentication; run pi and /login");
  const modelRegistry = sdk.ModelRegistry.inMemory(authStorage);
  return {
    available: modelRegistry.getAvailable(),
    subscription: model => modelRegistry.isUsingOAuth(model),
    sessionOptions: { authStorage, modelRegistry },
  };
}

// Split only on LF, not Unicode separators that may occur inside JSON strings.
function records(stream, receive, failed) {
  let buffer = "";
  stream.setEncoding("utf8");
  stream.on("data", chunk => {
    buffer += chunk;
    if (buffer.length > 32 * 1024 * 1024) return failed(new Error("RPC record too large"));
    let end;
    while ((end = buffer.indexOf("\n")) !== -1) {
      const line = buffer.slice(0, end);
      buffer = buffer.slice(end + 1);
      try { receive(JSON.parse(line)); } catch (error) { failed(error); }
    }
  });
}

export async function connectBridge(spec) {
  if (!spec.Command) throw new Error("yip bridge command is required");
  const child = spawn(spec.Command, spec.Args ?? [], {
    env: { ...process.env, ...spec.Env }, stdio: ["pipe", "pipe", "ignore"],
  });
  const exited = new Promise(resolve => {
    child.once("exit", resolve);
    child.once("error", resolve);
  });
  let next = 0;
  const pending = new Map();
  const fail = error => {
    for (const waiter of pending.values()) {
      clearTimeout(waiter.timer);
      waiter.reject(error);
    }
    pending.clear();
  };
  child.on("error", fail);
  child.on("exit", () => fail(new Error("yip bridge exited")));
  records(child.stdout, record => {
    const waiter = pending.get(record.id);
    if (!waiter) return;
    pending.delete(record.id);
    clearTimeout(waiter.timer);
    if (record.error) waiter.reject(new Error(record.error.message));
    else waiter.resolve(record.result);
  }, fail);
  const request = (method, params) => new Promise((resolve, reject) => {
    const id = ++next;
    const timer = setTimeout(() => {
      pending.delete(id);
      reject(new Error(`yip bridge timed out: ${method}`));
    }, method === "tools/call" ? 24 * 60 * 60 * 1000 : 15000);
    pending.set(id, { resolve, reject, timer });
    child.stdin.write(JSON.stringify({ jsonrpc: "2.0", id, method, params }) + "\n");
  });
  await request("initialize", {
    protocolVersion: "2024-11-05", capabilities: {},
    clientInfo: { name: "yip-pi", version: "1" },
  });
  child.stdin.write(JSON.stringify({ jsonrpc: "2.0", method: "notifications/initialized" }) + "\n");
  const tools = [];
  let cursor;
  do {
    const page = await request("tools/list", cursor ? { cursor } : {});
    tools.push(...page.tools);
    cursor = page.nextCursor;
  } while (cursor);
  const definitions = tools.map(tool => ({
    name: `yip_${tool.name}`, label: tool.name, description: tool.description ?? tool.name,
    parameters: tool.inputSchema,
    async execute(_id, args) {
      const result = await request("tools/call", { name: tool.name, arguments: args });
      if (result.isError) throw new Error(JSON.stringify(result.content));
      return { content: result.content, details: {} };
    },
  }));
  return {
    tools: definitions,
    async close() {
      child.stdin.end();
      // The Go supervisor remains responsible for the entire process group.
      await Promise.race([exited, new Promise(resolve => setTimeout(resolve, 250))]);
      if (child.exitCode === null) child.kill("SIGTERM");
    },
  };
}

// Pi's default bash backend starts detached process groups. Use its supported
// operations boundary instead so the Go supervisor owns shells AND background
// descendants, including when the SDK host crashes or the shell exits first.
export function attachedBashOperations(failAttempt) {
  return {
    exec(command, cwd, { onData, signal, timeout, env }) {
      if (signal?.aborted) return Promise.reject(new Error("aborted"));
      if (timeout !== undefined && (!Number.isFinite(timeout) || timeout <= 0 || timeout > 2147483.647))
        return Promise.reject(new Error("Invalid bash timeout"));
      return new Promise((resolve, reject) => {
        const shellEnv = { ...(env ?? process.env) };
        delete shellEnv.BASH_ENV;
        delete shellEnv.ENV;
        const child = spawn("/bin/bash", ["--noprofile", "--norc", "-c", command], {
          cwd, env: shellEnv, detached: false, stdio: ["ignore", "pipe", "pipe"],
        });
        let failure;
        let deadline;
        let drain;
        let finished = false;
        const finish = (code, signalName) => {
          if (finished) return;
          finished = true;
          clearTimeout(deadline);
          clearTimeout(drain);
          signal?.removeEventListener("abort", abort);
          child.stdout.destroy();
          child.stderr.destroy();
          if (failure) reject(failure);
          else resolve({ exitCode: code ?? (128 + (osConstants.signals[signalName] ?? 0)) });
        };
        const stop = error => {
          if (finished || failure) return;
          failure = error;
          // Do not pretend killing the shell kills its children. End the whole
          // attempt; the Go supervisor terminates and verifies the shared group.
          failAttempt(error);
          child.kill("SIGTERM");
        };
        const abort = () => stop(new Error("Pi bash aborted"));
        child.stdout.on("data", onData);
        child.stderr.on("data", onData);
        child.once("error", error => {
          failure = error;
          finish(null, null);
        });
        child.once("close", finish);
        child.once("exit", (code, signalName) => {
          // Background children may retain the shell's pipes. They remain in
          // the supervised group, but must not prevent reporting shell exit.
          drain = setTimeout(() => finish(code, signalName), 100);
        });
        if (timeout !== undefined)
          deadline = setTimeout(() => stop(new Error(`Pi bash timeout:${timeout}`)), timeout * 1000);
        if (signal?.aborted) abort();
        else signal?.addEventListener("abort", abort, { once: true });
      });
    },
  };
}

export function nativeTools(sdk, cwd, mode, approve, failAttempt = () => {}) {
  if (!["edit", "readonly", "conversation"].includes(mode)) throw new Error("Unsupported run mode");
  const tools = [sdk.createReadTool(cwd), sdk.createGrepTool(cwd), sdk.createFindTool(cwd), sdk.createLsTool(cwd)];
  if (mode === "edit") {
    const bash = sdk.createBashTool(cwd, { operations: attachedBashOperations(failAttempt) });
    for (const tool of [bash, sdk.createEditTool(cwd), sdk.createWriteTool(cwd)]) {
      tools.push({
        ...tool,
        async execute(id, args, ...rest) {
          if (!await approve(id, tool.name, args)) throw new Error("Action denied by yip");
          return tool.execute(id, args, ...rest);
        },
      });
    }
  }
  return tools;
}

async function main() {
  const sdk = await import(pathToFileURL(process.argv[2]).href);
  const send = record => process.stdout.write(JSON.stringify(record) + "\n");
  let session;
  let running = false;
  let finished = false;
  const approvals = new Map();
  const fatal = error => {
    if (!finished) send({ type: "result", outcome: "failed", error: String(error.message ?? error) });
    finished = true;
  };
  records(process.stdin, command => { void handle(command).catch(fatal); }, fatal);
  async function handle(command) {
    if (command.type === "approval") {
      const resolve = approvals.get(command.id);
      if (!resolve) throw new Error("Unknown approval");
      approvals.delete(command.id);
      resolve(command.allow === true);
      return;
    }
    if (command.type === "abort") {
      for (const resolve of approvals.values()) resolve(false);
      approvals.clear();
      await session?.abort();
      return;
    }
    if (command.type === "steer") {
      if (!running || finished) throw new Error("Pi is not running");
      await session.steer(command.text);
      send({ type: "response", id: command.id, success: true });
      return;
    }
    if (command.type !== "start" && command.type !== "probe") throw new Error("Unknown host command");
    const { available, subscription, sessionOptions } = await discoverModels(sdk);
    if (command.type === "probe") {
      send({
        type: "probe",
        billing: available.some(m => !subscription(m)) ? "api" : available.length ? "subscription" : "unknown",
        models: available.map((m, index) => ({ id: `${m.provider}/${m.id}`, label: m.name, default: index === 0 })),
      });
      return;
    }
    if (session || running) throw new Error("Already started");
    const spec = command.spec;
    const model = spec.Model
      ? available.find(m => `${m.provider}/${m.id}` === spec.Model)
      : available[0];
    if (!model) throw new Error("No configured Pi model; run pi and /login, then select a provider/model in yip");
    const approve = (id, tool, args) => new Promise(resolve => {
      approvals.set(id, resolve);
      send({ type: "approval", id, tool, args });
    });
    const bridge = await connectBridge(spec.MCP);
    const customTools = bridge.tools;
    const native = nativeTools(sdk, spec.Workdir, spec.Mode, approve, fatal);
    const allTools = [...native, ...customTools];
    const created = await sdk.createAgentSession({
      cwd: spec.Workdir, ...sessionOptions, model,
      sessionManager: sdk.SessionManager.inMemory(spec.Workdir),
      settingsManager: sdk.SettingsManager.inMemory({ compaction: { enabled: false }, retry: { enabled: false } }),
      resourceLoader: emptyResources(sdk, spec.Instructions),
      // Both tested SDKs take tool *names*, not the objects shown in older docs.
      // SDK custom definitions override the identically named built-ins.
      tools: allTools.map(tool => tool.name), customTools: allTools,
    });
    session = created.session;
    // Check the actual tool allowlist before sending any prompt.
    const activeTools = session.getActiveToolNames();
    const expectedTools = new Set(allTools.map(tool => tool.name));
    if (activeTools.length !== expectedTools.size || activeTools.some(name => !expectedTools.has(name))) {
      session.dispose();
      await bridge.close();
      throw new Error("Pi SDK did not apply the required tool allowlist");
    }
    let finalText = "";
    let error = "";
    let input = 0, output = 0, cost = 0;
    session.subscribe(event => {
      if (event.type === "message_update" && event.assistantMessageEvent.type === "text_delta")
        send({ type: "text", text: event.assistantMessageEvent.delta });
      if (event.type === "tool_execution_start" || event.type === "tool_execution_end")
        send({ type: event.type, tool: event.toolName, data: event });
      if (event.type === "message_end" && event.message.role === "assistant") {
        const message = event.message;
        finalText = message.content.filter(c => c.type === "text").map(c => c.text).join("");
        if (message.stopReason === "error" || message.stopReason === "aborted")
          error = message.errorMessage || message.stopReason;
        input += (message.usage?.input ?? 0) + (message.usage?.cacheRead ?? 0) + (message.usage?.cacheWrite ?? 0);
        output += message.usage?.output ?? 0;
        cost += message.usage?.cost?.total ?? 0;
      }
    });
    running = true;
    try {
      await session.prompt(spec.Prompt, { expandPromptTemplates: false });
    } finally {
      session.dispose();
      await bridge.close();
    }
    running = false;
    finished = true;
    send({
      type: "result", outcome: error ? "failed" : "succeeded", text: finalText, error, input, output, cost,
      billing: subscription(model) ? "subscription" : "api",
    });
  }
}

if (process.argv[1] && import.meta.url === pathToFileURL(realpathSync(process.argv[1])).href) {
  main().catch(error => {
    process.stdout.write(JSON.stringify({ type: "result", outcome: "failed", error: error.message }) + "\n");
    process.exitCode = 1;
  });
}
