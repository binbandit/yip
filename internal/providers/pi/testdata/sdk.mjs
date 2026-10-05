// A fake official SDK boundary: no credentials or model APIs are accessed.
export const AuthStorage = { create: () => ({ drainErrors: () => [] }) };
export const ModelRegistry = {
  inMemory: () => ({
    getAvailable: () => process.env.PI_TEST_AUTH === "none" ? [] : [
      { provider: "oauth", id: "model", name: "OAuth model" },
      ...(process.env.PI_TEST_AUTH === "mixed" ? [{ provider: "api", id: "model", name: "API model" }] : []),
    ],
    isUsingOAuth: model => model.provider === "oauth",
  }),
};
export const ModelRuntime = process.env.PI_TEST_LEGACY ? undefined : {
  async create(options) {
    if (options.modelsPath !== null || options.refreshOnCreate !== false || options.allowModelNetwork !== false)
      throw new Error("unsafe discovery");
    return {
      async listCredentials() {
        return ModelRegistry.inMemory().getAvailable().map(model => ({
          providerId: model.provider, type: model.provider === "oauth" ? "oauth" : "api_key",
        }));
      },
      getModels: () => ModelRegistry.inMemory().getAvailable(),
      getProvider: () => ({ auth: { oauth: { isSubscription: true } } }),
      async getAvailable() { throw new Error("must not resolve credentials during discovery"); },
    };
  },
};
export const SessionManager = { inMemory: () => ({}) };
export const SettingsManager = { inMemory: settings => settings };
export const createExtensionRuntime = () => ({});
const tool = name => () => ({
  name, label: name, description: name, parameters: { type: "object", properties: {} },
  execute: async () => ({ content: [{ type: "text", text: "native result" }] }),
});
export const createReadTool = tool("read");
export const createGrepTool = tool("grep");
export const createFindTool = tool("find");
export const createLsTool = tool("ls");
export const createBashTool = tool("bash");
export const createEditTool = tool("edit");
export const createWriteTool = tool("write");
export async function createAgentSession(options) {
  if (process.env.PI_TEST_SECRET) throw new Error("ambient environment leaked");
  if (options.resourceLoader.getExtensions().extensions.length) throw new Error("unsafe extensions");
  if (options.resourceLoader.getAgentsFiles().agentsFiles.length) throw new Error("unsafe context");
  if (options.tools.some(t => typeof t !== "string")) throw new Error("tools must be names");
  let listener = () => {};
  let release;
  return {
    session: {
      getActiveToolNames() {
        return process.env.PI_TEST_EXTRA_TOOL ? [...options.tools, "unexpected_tool"] : options.tools;
      },
      subscribe(fn) { listener = fn; },
      async steer(text) {
        if (text !== "continue") throw new Error("bad steer");
        setTimeout(() => release?.(), 20);
      },
      async abort() { release?.(); },
      dispose() {},
      async prompt(prompt) {
        if (prompt === "error") throw new Error("fake provider failure");
        if (prompt === "crash") process.exit(9);
        listener({ type: "message_update", assistantMessageEvent: { type: "text_delta", delta: "Hello\u2028world" } });
        if (prompt === "hang") await new Promise(() => {});
        if (prompt === "wait" || prompt === "steer") await new Promise(resolve => { release = resolve; });
        if (prompt === "approval") {
          const bash = options.customTools.find(t => t.name === "bash");
          await bash.execute("approval-1", { command: "echo safe" });
        }
        if (prompt === "skills") {
          const read = options.customTools.find(t => t.name === "yip_skill_read");
          if (!read) throw new Error("skill_read was not discovered");
          const docs = [];
          for (const name of ["arena", "babysit-pr", "bro", "file-pr"]) {
            const result = await read.execute(name, { name });
            if (result.isError) throw new Error("skill read failed");
            docs.push(JSON.parse(result.content[0].text));
          }
          listener({ type: "message_end", message: { role: "assistant", stopReason: "stop",
            content: [{ type: "text", text: JSON.stringify(docs) }] } });
          return;
        }
        const bridge = options.customTools.find(t => t.name === "yip_list");
        if (!bridge) throw new Error("bridge tool missing");
        listener({ type: "tool_execution_start", toolName: bridge.name, toolCallId: "call" });
        const result = await bridge.execute("call", {});
        listener({ type: "tool_execution_end", toolName: bridge.name, toolCallId: "call", result });
        listener({
          type: "message_end",
          message: { role: "assistant", stopReason: "stop", content: [{ type: "text", text: result.content[0].text }],
            usage: { input: 2, output: 3, cacheRead: 4, cacheWrite: 5, cost: { total: 0.01 } } },
        });
      },
    },
  };
}
