// Command yip runs the hub, runners, and operator tools.
package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/binbandit/yip/internal/buildinfo"
)

const usage = `yip — a self-hosted workspace for your AI engineering team

Usage:
  yip hub [flags]              Run the hub (API, web client, scheduler, runner listener)
  yip hub setup-code           Print a new one-time owner setup code
  yip demo [flags]             Run a labelled demo workspace with the fake provider
  yip runner pair [flags]      Pair this machine with a hub
  yip runner [flags]           Run a paired runner
  yip runner workspaces        List job workspaces and whether they hold uncommitted work
  yip runner cleanup           Delete one workspace (explicit selection and confirmation)
  yip doctor [flags]           Check hub or runner health on this machine
  yip backup --out DIR         Write an online, verified backup of the hub
  yip restore --from DIR       Restore a backup into a new data directory
  yip owner reset-password     Reset the owner's password (revokes sessions)
  yip forge github add         Store a GitHub credential for PR integration (--from-gh reuses the gh sign-in)
  yip service install hub|runner  Write a launchd/systemd service definition
  yip schema --out DIR         Generate JSON schemas and TypeScript types
  yip version                  Print the version

Run "yip <command> -h" for command flags.
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	cmd, args := os.Args[1], os.Args[2:]
	var err error
	switch cmd {
	case "hub":
		if len(args) > 0 && args[0] == "setup-code" {
			err = runSetupCode(args[1:])
		} else {
			err = runHub(args)
		}
	case "demo":
		err = runDemo(args)
	case "runner":
		switch {
		case len(args) > 0 && args[0] == "pair":
			err = runPair(args[1:])
		case len(args) > 0 && args[0] == "workspaces":
			err = runWorkspaces(args[1:])
		case len(args) > 0 && args[0] == "cleanup":
			err = runCleanup(args[1:])
		default:
			err = runRunner(args)
		}
	case "bridge":
		err = runBridge(args)
	case "agent-worker":
		err = runAgentWorker(args)
	case "doctor":
		err = runDoctor(args)
	case "backup":
		err = runBackup(args)
	case "restore":
		err = runRestore(args)
	case "owner":
		err = runOwner(args)
	case "forge":
		err = runForge(args)
	case "service":
		err = runService(args)
	case "schema":
		err = runSchema(args)
	case "version", "--version", "-v":
		fmt.Println("yip", buildinfo.Version)
	case "help", "-h", "--help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "yip: unknown command %q\n\n%s", cmd, usage)
		os.Exit(2)
	}
	if err != nil {
		msg := err.Error()
		if !strings.HasSuffix(msg, "\n") {
			msg += "\n"
		}
		fmt.Fprint(os.Stderr, "yip: "+msg)
		os.Exit(1)
	}
}
