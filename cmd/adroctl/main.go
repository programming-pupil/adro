// adroctl is the operator-facing local bootstrap command.
package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/adro-project/adro/internal/config"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "up":
		up(os.Args[2:])
	case "install":
		install(os.Args[2:])
	case "health":
		health(os.Args[2:])
	case "version":
		fmt.Println("adroctl 0.1.0")
	case "config-check":
		configCheck(os.Args[2:])
	default:
		usage()
		os.Exit(2)
	}
}
func usage() { fmt.Println("Usage: adroctl <up|install --profile single-node|health|config-check|version>") }

func configCheck(_ []string) {
	if err := config.Validate(config.FromEnv()); err != nil {
		fmt.Fprintf(os.Stderr, "configuration blocked: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("configuration valid for the single-node reference profile")
}
func up(args []string) {
	fs := flag.NewFlagSet("up", flag.ExitOnError)
	addr := fs.String("addr", ":8080", "API listen address")
	fs.Parse(args)
	if err := config.Validate(config.FromEnv()); err != nil {
		fmt.Fprintf(os.Stderr, "real Multica configuration required: %v\n", err)
		os.Exit(1)
	}
	root := filepath.Join("var", "artifacts")
	if err := os.MkdirAll(root, 0750); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	apiBinary, lookErr := exec.LookPath("adro-api")
	var cmd *exec.Cmd
	if lookErr == nil {
		cmd = exec.Command(apiBinary, "-addr", *addr, "-artifact-root", root)
	} else {
		// Source checkouts can run without a prior install. Packaged
		// installations should place adro-api on PATH instead.
		cmd = exec.Command("go", "run", "./cmd/adro-api", "-addr", *addr, "-artifact-root", root)
	}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		os.Exit(1)
	}
}

// install performs the deterministic single-node bootstrap. The reference
// distribution owns the ADRO API, workbench, and filesystem volume; external
// Multica and production dependencies remain explicit deployment inputs.
func install(args []string) {
	fs := flag.NewFlagSet("install", flag.ExitOnError)
	profile := fs.String("profile", "", "installation profile (single-node)")
	compose := fs.String("compose-file", filepath.Join("deploy", "compose", "docker-compose.yml"), "Docker Compose file")
	dryRun := fs.Bool("dry-run", false, "validate and print the bootstrap command without starting containers")
	fs.Parse(args)
	if *profile != "single-node" {
		fmt.Fprintln(os.Stderr, "only --profile single-node is available in this release")
		os.Exit(2)
	}
	if _, err := os.Stat(*compose); err != nil {
		fmt.Fprintf(os.Stderr, "compose file is unavailable: %v\n", err)
		os.Exit(1)
	}
	artifactRoot := filepath.Join("var", "artifacts")
	if err := os.MkdirAll(artifactRoot, 0750); err != nil {
		fmt.Fprintf(os.Stderr, "create artifact volume: %v\n", err)
		os.Exit(1)
	}
	command := []string{"compose", "-f", *compose, "up", "-d", "--build"}
	if *dryRun {
		fmt.Printf("profile=%s\nartifact_root=%s\ncommand=docker %s\n", *profile, artifactRoot, strings.Join(command, " "))
		return
	}
	if _, err := exec.LookPath("docker"); err != nil {
		fmt.Fprintln(os.Stderr, "Docker is required for install; use `adroctl up` with ADRO_MULTICA_URL and ADRO_MULTICA_TOKEN for a remote Multica deployment")
		os.Exit(1)
	}
	cmd := exec.Command("docker", command...)
	cmd.Stdout, cmd.Stderr, cmd.Stdin = os.Stdout, os.Stderr, os.Stdin
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "single-node install failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("ADRO single-node profile started; run `adroctl health` to verify readiness")
}

func health(args []string) {
	fs := flag.NewFlagSet("health", flag.ExitOnError)
	url := fs.String("url", "http://127.0.0.1:8080/readyz", "readiness URL")
	fs.Parse(args)
	resp, err := http.Get(*url)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer resp.Body.Close()
	fmt.Println(resp.Status)
	if resp.StatusCode >= 300 {
		os.Exit(1)
	}
}
