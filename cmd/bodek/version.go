package main

import (
	"fmt"
	"io"
	"os"
	"runtime/debug"
	"strings"
)

// version is stamped by GoReleaser via -X main.version; empty for local builds.
var version string

// currentVersion reports the running build's version: the GoReleaser-stamped
// value, the module version for `go install …@latest` builds, or "dev" for
// anything else (plain `go build`, `go run`).
func currentVersion() string {
	if version != "" {
		return version
	}
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return bi.Main.Version
	}
	return "dev"
}

const upgradeUsage = `Usage: bodek upgrade [--skip-signature]

Upgrade bodek to the latest GitHub release. The release's checksums.txt is
verified against its Sigstore signature (the release workflow's keyless
certificate for that exact tag, logged in Rekor); the archive matching the
current OS/architecture is then downloaded, verified against checksums.txt
(SHA-256), and installed atomically over the running binary. Any
verification failure refuses the upgrade.

  --skip-signature   UNSAFE: skip the Sigstore check and trust checksums.txt
                     from the release itself. Only for when this binary's
                     embedded Sigstore trust root can no longer verify new
                     releases; verify the download yourself with cosign.
`

// handleSubcommand runs the bare subcommands (`bodek version`,
// `bodek upgrade`) before flag parsing kicks in. It reports whether args
// named a subcommand; anything else falls through to the normal TUI path.
func handleSubcommand(args []string, stdout io.Writer) (handled bool, err error) {
	if len(args) == 0 {
		return false, nil
	}
	switch args[0] {
	case "version":
		if v := currentVersion(); v == "dev" {
			_, _ = fmt.Fprintln(stdout, "bodek dev (built from source)")
		} else {
			_, _ = fmt.Fprintf(stdout, "bodek v%s\n", strings.TrimPrefix(v, "v"))
		}
		return true, nil
	case "upgrade":
		var opts upgradeOptions
		for _, a := range args[1:] {
			switch a {
			case "--skip-signature":
				opts.skipSignature = true
			case "--help", "-h":
				_, _ = fmt.Fprint(stdout, upgradeUsage)
				return true, nil
			default:
				return true, fmt.Errorf("unknown flag %q for upgrade", a)
			}
		}
		if opts.skipSignature {
			_, _ = fmt.Fprintln(os.Stderr, "warning: --skip-signature: the release signature is NOT verified; a tampered release would be installed")
		}
		return true, runUpgrade(opts, stdout)
	}
	return false, nil
}
