// Package isolation describes how a command is executed on a node. openbox favors a
// lightweight, native default and lets the caller opt up to stronger isolation:
//
//	native            run directly on the host (a dedicated shell process)
//	docker:<image>    run inside a container from <image> (full docker available)
//	nspawn:<dir>      run inside a systemd-nspawn container rooted at <dir> (Linux)
//
// The same spec drives both one-shot commands and persistent sessions: for a session
// the shell argv is started once and lives for the session's lifetime, so a docker
// session keeps a single container alive across commands (cwd/env persist in it).
package isolation

import (
	"fmt"
	"strings"
)

// Kind enumerates the isolation backends.
type Kind string

const (
	Native Kind = "native"
	Docker Kind = "docker"
	Nspawn Kind = "nspawn"
)

// Isolation is a parsed execution mode.
type Isolation struct {
	Kind  Kind
	Image string // docker image, or nspawn root directory
}

// Default is the lightweight native mode.
func Default() Isolation { return Isolation{Kind: Native} }

// Parse reads a spec like "native", "docker:ubuntu:22.04", or "nspawn:/var/lib/m/deb".
// An empty spec yields the native default.
func Parse(spec string) (Isolation, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" || spec == "native" {
		return Default(), nil
	}
	kind, arg, ok := strings.Cut(spec, ":")
	switch Kind(kind) {
	case Docker:
		if !ok || arg == "" {
			return Isolation{}, fmt.Errorf("docker isolation requires an image, e.g. docker:ubuntu:22.04")
		}
		return Isolation{Kind: Docker, Image: arg}, nil
	case Nspawn:
		if !ok || arg == "" {
			return Isolation{}, fmt.Errorf("nspawn isolation requires a root dir, e.g. nspawn:/var/lib/machines/x")
		}
		return Isolation{Kind: Nspawn, Image: arg}, nil
	default:
		return Isolation{}, fmt.Errorf("unknown isolation %q", spec)
	}
}

// String renders the spec form.
func (i Isolation) String() string {
	switch i.Kind {
	case Docker, Nspawn:
		return string(i.Kind) + ":" + i.Image
	default:
		return string(Native)
	}
}

// ShellArgv returns the argv that starts a persistent shell reading commands from
// stdin (used by sessions).
func (i Isolation) ShellArgv() []string {
	switch i.Kind {
	case Docker:
		// -i keeps stdin open; the container lives as long as the shell does.
		return []string{"docker", "run", "-i", "--rm", i.Image, "/bin/sh"}
	case Nspawn:
		return []string{"systemd-nspawn", "-q", "-D", i.Image, "/bin/sh"}
	default:
		return []string{"/bin/sh"}
	}
}

// OneShotArgv returns the argv that runs a single command and exits.
func (i Isolation) OneShotArgv(cmdline string) []string {
	switch i.Kind {
	case Docker:
		return []string{"docker", "run", "--rm", i.Image, "/bin/sh", "-c", cmdline}
	case Nspawn:
		return []string{"systemd-nspawn", "-q", "-D", i.Image, "/bin/sh", "-c", cmdline}
	default:
		return []string{"/bin/sh", "-c", cmdline}
	}
}
