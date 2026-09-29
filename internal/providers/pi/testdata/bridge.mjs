import { createInterface } from "node:readline";
createInterface({ input: process.stdin }).on("line", line => {
  const request = JSON.parse(line);
  if (!request.id) return;
  let result;
  switch (request.method) {
    case "initialize": result = { protocolVersion: "2024-11-05", capabilities: { tools: {} }, serverInfo: { name: "fake", version: "1" } }; break;
    case "tools/list": result = { tools: [{ name: "list", description: "List scoped data", inputSchema: { type: "object", properties: {} } }] }; break;
    case "tools/call": result = { content: [{ type: "text", text: "bridge result" }] }; break;
    default: throw new Error("unexpected method");
  }
  process.stdout.write(JSON.stringify({ jsonrpc: "2.0", id: request.id, result }) + "\n");
});
