// Command openbox is the single binary for the openbox personal box manager.
//
//	openbox login --server URL --token TOKEN   authenticate this machine
//	openbox control [flags]                    run the self-hosted control plane
//	openbox agent [flags]                      run the node daemon
//	openbox node token [--tag t]               mint a node enrollment token
//	openbox nodes [--tag t]                    list your nodes
//	openbox -t TAG [-s SID] <command...>       run a command on a node (shorthand)
//	openbox run -t TAG [-s SID] <command...>   run a command on a node
//
// Phase 1 uses a plain-TCP transport on loopback; the transport interface lets the
// embedded-Tailscale mesh drop in without changing any command.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"openbox.io/openbox/internal/client"
	"openbox.io/openbox/internal/config"
)

const (
	defaultControlURL  = "http://127.0.0.1:8080"
	defaultControlAddr = "127.0.0.1:8080"
	defaultAgentAddr   = "127.0.0.1:7600"
)

func main() { os.Exit(run(os.Args[1:])) }

func run(args []string) int {
	if len(args) == 0 {
		usage()
		return 2
	}
	// Shorthand: `openbox -t/-s/-n ... cmd` is an implicit `run`.
	if isRunFlag(args[0]) {
		return cmdRun(args)
	}
	switch args[0] {
	case "login":
		return cmdLogin(args[1:])
	case "whoami":
		return cmdWhoami(args[1:])
	case "control":
		return cmdControl(args[1:])
	case "agent":
		return cmdAgent(args[1:])
	case "node":
		return cmdNode(args[1:])
	case "nodes":
		return cmdNodes(args[1:])
	case "run":
		return cmdRun(args[1:])
	case "version", "-v", "--version":
		fmt.Println("openbox 0.1.0-phase1")
		return 0
	case "help", "-h", "--help":
		usage()
		return 0
	default:
		fmt.Fprintf(os.Stderr, "openbox: unknown command %q\n\n", args[0])
		usage()
		return 2
	}
}

func signalCtx() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
}

func fail(err error) int {
	fmt.Fprintf(os.Stderr, "openbox: %v\n", err)
	return 1
}

// --- run (the core agent-facing UX) ---

func isRunFlag(a string) bool {
	switch a {
	case "-t", "--tag", "-s", "--session", "-n", "--node",
		"--docker", "--nspawn", "--isolate":
		return true
	}
	return false
}

func cmdRun(args []string) int {
	target, cmdline, err := parseRunArgs(args)
	if err != nil {
		return fail(err)
	}
	if cmdline == "" {
		fmt.Fprintln(os.Stderr, "usage: openbox -t <tag> [-s <session>] <command...>")
		return 2
	}
	cfg, err := config.LoadClient()
	if err != nil {
		return fail(err)
	}

	ctx, stop := signalCtx()
	defer stop()

	tr, err := buildTransport("client", meshFlags{})
	if err != nil {
		return fail(err)
	}
	defer tr.Close()

	code, err := client.Run(ctx, cfg, tr, target, cmdline, os.Stdout, os.Stderr)
	// Persist any key generated during Signer().
	if cfg.Dirty() {
		_ = cfg.Save()
	}
	if err != nil {
		return fail(err)
	}
	return code
}

// parseRunArgs consumes leading -t/-s/-n flags, then treats the remainder as the
// command verbatim (so command flags are never interpreted by openbox).
func parseRunArgs(args []string) (client.Target, string, error) {
	var t client.Target
	i := 0
	for i < len(args) {
		switch args[i] {
		case "-t", "--tag":
			if i+1 >= len(args) {
				return t, "", fmt.Errorf("%s requires a value", args[i])
			}
			t.Tag = args[i+1]
			i += 2
		case "-s", "--session":
			if i+1 >= len(args) {
				return t, "", fmt.Errorf("%s requires a value", args[i])
			}
			t.SessionID = args[i+1]
			i += 2
		case "-n", "--node":
			if i+1 >= len(args) {
				return t, "", fmt.Errorf("%s requires a value", args[i])
			}
			t.NodeID = args[i+1]
			i += 2
		case "--docker":
			if i+1 >= len(args) {
				return t, "", fmt.Errorf("%s requires an image", args[i])
			}
			t.Isolation = "docker:" + args[i+1]
			i += 2
		case "--nspawn":
			if i+1 >= len(args) {
				return t, "", fmt.Errorf("%s requires a root dir", args[i])
			}
			t.Isolation = "nspawn:" + args[i+1]
			i += 2
		case "--isolate":
			if i+1 >= len(args) {
				return t, "", fmt.Errorf("%s requires a spec", args[i])
			}
			t.Isolation = args[i+1]
			i += 2
		case "--":
			i++
			return t, strings.Join(args[i:], " "), nil
		default:
			return t, strings.Join(args[i:], " "), nil
		}
	}
	return t, "", nil
}

func usage() {
	fmt.Fprint(os.Stderr, `openbox — personal box manager

commands:
  login --server URL --token TOKEN    authenticate this machine
  whoami                              show the logged-in user
  control [flags]                     run the self-hosted control plane
  agent  [flags]                      run the node daemon on this machine
  node token [--tag t ...]            mint a node enrollment token
  nodes [--tag t]                     list your nodes
  run -t TAG [-s SID] <command...>    run a command on a node
  version

shorthand:
  openbox -t mac uname -a
  openbox -t mac -s build1 cd /tmp && openbox -t mac -s build1 make
`)
}
