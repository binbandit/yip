// Exercises the real host tool wrapper without sessions, credentials or models.
import { createInterface } from "node:readline";
import { pathToFileURL } from "node:url";
const { nativeTools } = await import(pathToFileURL(process.argv[2]).href);
const stub = () => ({});
const sdk = process.env.YIP_PI_TEST_SDK
  ? await import(pathToFileURL(process.env.YIP_PI_TEST_SDK).href)
  : {
    createReadTool: stub, createGrepTool: stub, createFindTool: stub, createLsTool: stub,
    createEditTool: stub, createWriteTool: stub,
    createBashTool: (cwd, options) => ({
      name: "bash",
      execute: (_id, args, signal) => options.operations.exec(args.command, cwd, {
        signal, timeout: args.timeout, onData() {},
      }),
    }),
  };
const send = record => process.stdout.write(JSON.stringify(record) + "\n");
const controller = new AbortController();
const bash = nativeTools(sdk, process.cwd(), "edit", async () => true,
  error => send({ type: "result", outcome: "failed", error: error.message }),
).find(tool => tool.name === "bash");
createInterface({ input: process.stdin }).on("line", line => {
  const command = JSON.parse(line);
  if (command.type === "abort") controller.abort();
  if (command.type === "finish") send({ type: "result", outcome: "succeeded" });
});
const background = process.env.YIP_TEST_CASE === "background";
const timeout = process.env.YIP_TEST_CASE === "timeout" ? 0.5 : undefined;
const command = `sleep 30 >/dev/null 2>&1 & echo $! > "$YIP_TEST_PID"${background ? "" : "; wait"}`;
try {
  await bash.execute("approved-tool", { command, timeout }, controller.signal);
  send({ type: "text", text: "shell exited" });
} catch (error) {
  send({ type: "result", outcome: "failed", error: error.message });
}
