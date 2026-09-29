import assert from "node:assert/strict";
import test from "node:test";
import { pathToFileURL } from "node:url";
import { discoverModels, emptyResources, nativeTools } from "./host.mjs";

const sdk = Object.fromEntries(["Read", "Grep", "Find", "Ls", "Bash", "Edit", "Write"].map(name => [
  `create${name}Tool`,
  () => ({ name: name.toLowerCase(), execute: async () => ({ content: [{ type: "text", text: "executed" }] }) }),
]));
sdk.createExtensionRuntime = () => ({});

test("readonly and conversation cannot execute mutations or shell", async () => {
  for (const mode of ["readonly", "conversation"]) {
    const tools = nativeTools(sdk, "/workspace", mode, () => { throw new Error("must not approve"); });
    assert.deepEqual(tools.map(t => t.name), ["read", "grep", "find", "ls"]);
  }
  assert.throws(() => nativeTools(sdk, "/workspace", "unsafe", async () => true));
});

test("every mutating builtin is gated on the exact tool arguments", async () => {
  const requests = [];
  const tools = nativeTools(sdk, "/workspace", "edit", async (...args) => {
    requests.push(args);
    return false;
  });
  for (const name of ["bash", "edit", "write"]) {
    await assert.rejects(tools.find(t => t.name === name).execute("id", { command: "echo hi" }), /denied/);
  }
  assert.deepEqual(requests.map(args => args[1]), ["bash", "edit", "write"]);
  assert.deepEqual(requests[0], ["id", "bash", { command: "echo hi" }]);
  const allowed = nativeTools(sdk, "/workspace", "edit", async () => true);
  assert.equal((await allowed.find(t => t.name === "bash").execute("id", {})).content[0].text, "executed");
});

test("resources cannot auto-load executable or prompt configuration", async () => {
  const loader = emptyResources(sdk, "yip instructions");
  await loader.reload();
  loader.extendResources({ skillPaths: ["/untrusted"] });
  assert.deepEqual(loader.getExtensions().extensions, []);
  assert.deepEqual(loader.getAgentsFiles().agentsFiles, []);
  assert.deepEqual(loader.getSkills().skills, []);
  assert.deepEqual(loader.getPrompts().prompts, []);
  assert.deepEqual(loader.getThemes().themes, []);
  assert.deepEqual(loader.getAppendSystemPrompt(), ["yip instructions"]);
  assert.equal(loader.getSystemPrompt(), undefined);
});

test("current discovery uses only credential metadata and disables config/network", async () => {
  const models = [
    { provider: "subscription", id: "a" },
    { provider: "oauth-without-plan", id: "b" },
    { provider: "api", id: "c" },
    { provider: "unconfigured", id: "d" },
  ];
  const current = {
    ModelRuntime: {
      async create(options) {
        assert.equal(options.modelsPath, null);
        assert.equal(options.refreshOnCreate, false);
        assert.equal(options.allowModelNetwork, false);
        return {
          async listCredentials() {
            return [
              { providerId: "subscription", type: "oauth" },
              { providerId: "oauth-without-plan", type: "oauth" },
              { providerId: "api", type: "api_key" },
            ];
          },
          getModels: () => models,
          getProvider: id => ({ auth: { oauth: { isSubscription: id === "subscription" } } }),
          getAvailable() { throw new Error("would resolve credentials"); },
          getAuth() { throw new Error("would expose credentials"); },
        };
      },
    },
  };
  const result = await discoverModels(current);
  assert.deepEqual(result.available, models.slice(0, 3));
  assert.deepEqual(result.available.map(result.subscription), [true, false, false]);
});

test("official SDK enforces names and custom overrides without account or model access", {
  skip: !process.env.YIP_PI_TEST_SDK,
}, async () => {
  const official = await import(pathToFileURL(process.env.YIP_PI_TEST_SDK).href);
  let sessionOptions;
  let model;
  if (official.ModelRuntime) {
    const credentials = {
      async read() { return undefined; },
      async list() { return []; },
      async modify() { throw new Error("No credential writes in tests"); },
      async delete() { throw new Error("No credential writes in tests"); },
    };
    ({ sessionOptions } = await discoverModels(official, credentials));
    model = sessionOptions.modelRuntime.getModels()[0];
  } else {
    const authStorage = official.AuthStorage.inMemory();
    const modelRegistry = official.ModelRegistry.inMemory(authStorage);
    sessionOptions = { authStorage, modelRegistry };
    model = modelRegistry.getAll()[0];
  }
  for (const mode of ["readonly", "edit"]) {
    const customTools = nativeTools(official, process.cwd(), mode, async () => false);
    const { session } = await official.createAgentSession({
      cwd: process.cwd(), ...sessionOptions, model,
      sessionManager: official.SessionManager.inMemory(),
      settingsManager: official.SettingsManager.inMemory(),
      resourceLoader: emptyResources(official, "test"),
      tools: customTools.map(tool => tool.name), customTools,
    });
    try {
      assert.deepEqual(session.getActiveToolNames().sort(), customTools.map(t => t.name).sort());
      const bash = session.agent.state.tools.find(tool => tool.name === "bash");
      if (mode === "readonly") assert.equal(bash, undefined);
      else await assert.rejects(bash.execute("test", { command: "exit 99" }), /denied/);
    } finally {
      session.dispose();
    }
  }
});
