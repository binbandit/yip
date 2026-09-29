# Browser test harness

`just e2e` builds this executable separately from `bin/yip`. Release builds do
not include it. It binds only loopback addresses, uses a caller-supplied temporary
directory, and never invokes an installed AI provider or imports credentials.

The script director, adapter, MCP client, and fixture repositories originate in
the former demo implementation. They now live entirely under `test/`. The test
adapter occupies the Codex provider slot so the production provider catalog and
protocol stay unchanged. Its probe describes scripted behavior, not a real model.
The adapter reads the stored manifest for its run and exercises the real native
runner, workspace preparation, MCP bridge, hub policy checks, and browser API.

These cases establish browser and workflow behavior under controlled scripts.
They are not evidence of real-provider model quality, authentication, or billing.
The separate smoke project still exercises the shipped CLI's fresh-hub setup.
