// adroctl is the operator-facing local bootstrap command.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/adro-project/adro/internal/config"
	"github.com/adro-project/adro/internal/orchestration"
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
	case "graph-validate":
		graphValidate(os.Args[2:])
	case "agent", "squad", "plan":
		orchestrationControl(os.Args[1], os.Args[2:])
	case "api":
		genericAPI(os.Args[2:])
	default:
		usage()
		os.Exit(2)
	}
}
func usage() {
	fmt.Println("Usage: adroctl <up|install|health|config-check|graph-validate|agent|squad|plan|api|version>")
	fmt.Println("  adroctl agent <list|get|create|validate|enable|disable|archive> [flags]")
	fmt.Println("  adroctl squad <list|get|create|validate|dry-run|publish|disable|archive> [flags]")
	fmt.Println("  adroctl plan <list|get|create|publish|timeline|replay|diagnostics> [flags]")
	fmt.Println("  adroctl api --method GET --path /api/v1/... [--file body.json]")
}

type apiOptions struct {
	BaseURL        string
	Workspace      string
	Tenant         string
	Token          string
	ID             string
	RequirementID  string
	Status         string
	File           string
	IdempotencyKey string
}

func orchestrationControl(resource string, args []string) {
	if len(args) == 0 {
		fmt.Fprintf(os.Stderr, "%s subcommand is required\n", resource)
		os.Exit(2)
	}
	action := args[0]
	fs := flag.NewFlagSet(resource+" "+action, flag.ExitOnError)
	opts := bindAPIOptions(fs)
	fs.Parse(args[1:])
	method, path, bodyRequired, err := orchestrationRequest(resource, action, opts)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	body, err := requestBody(opts.File, bodyRequired)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	response, err := doAPIRequest(method, path, body, opts)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	printJSON(response)
}

func genericAPI(args []string) {
	fs := flag.NewFlagSet("api", flag.ExitOnError)
	opts := bindAPIOptions(fs)
	method := fs.String("method", http.MethodGet, "HTTP method")
	path := fs.String("path", "", "API path beginning with /api/v1/")
	fs.Parse(args)
	if !strings.HasPrefix(*path, "/api/") {
		fmt.Fprintln(os.Stderr, "--path must begin with /api/")
		os.Exit(2)
	}
	body, err := requestBody(opts.File, false)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	response, err := doAPIRequest(strings.ToUpper(*method), *path, body, opts)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	printJSON(response)
}

func bindAPIOptions(fs *flag.FlagSet) apiOptions {
	opts := apiOptions{}
	fs.StringVar(&opts.BaseURL, "api", envDefault("ADRO_API_URL", "http://127.0.0.1:8080"), "ADRO API base URL")
	fs.StringVar(&opts.Workspace, "workspace", envDefault("ADRO_WORKSPACE_ID", "local"), "workspace ID")
	fs.StringVar(&opts.Tenant, "tenant", os.Getenv("ADRO_TENANT_ID"), "tenant ID")
	fs.StringVar(&opts.Token, "token", os.Getenv("ADRO_TOKEN"), "bearer token (prefer ADRO_TOKEN)")
	fs.StringVar(&opts.ID, "id", "", "resource or run ID")
	fs.StringVar(&opts.RequirementID, "requirement", "", "requirement ID")
	fs.StringVar(&opts.Status, "status", "", "status filter")
	fs.StringVar(&opts.File, "file", "", "JSON request body")
	fs.StringVar(&opts.IdempotencyKey, "idempotency-key", "", "idempotency key")
	return opts
}

func orchestrationRequest(resource, action string, opts apiOptions) (string, string, bool, error) {
	workspace := url.PathEscape(strings.TrimSpace(opts.Workspace))
	id := url.PathEscape(strings.TrimSpace(opts.ID))
	requirement := url.PathEscape(strings.TrimSpace(opts.RequirementID))
	requireID := func() error {
		if id == "" {
			return errors.New("--id is required")
		}
		return nil
	}
	switch resource {
	case "agent":
		collection := "/api/v1/workspaces/" + workspace + "/agents"
		switch action {
		case "list":
			if opts.Status != "" {
				collection += "?status=" + url.QueryEscape(opts.Status)
			}
			return http.MethodGet, collection, false, nil
		case "create":
			return http.MethodPost, collection, true, nil
		case "get":
			if err := requireID(); err != nil {
				return "", "", false, err
			}
			return http.MethodGet, "/api/v1/agents/" + id + "?workspace_id=" + url.QueryEscape(opts.Workspace), false, nil
		case "validate", "enable", "disable", "archive":
			if err := requireID(); err != nil {
				return "", "", false, err
			}
			return http.MethodPost, "/api/v1/agents/" + id + "/" + action + "?workspace_id=" + url.QueryEscape(opts.Workspace), false, nil
		}
	case "squad":
		collection := "/api/v1/workspaces/" + workspace + "/squads"
		switch action {
		case "list":
			if opts.Status != "" {
				collection += "?status=" + url.QueryEscape(opts.Status)
			}
			return http.MethodGet, collection, false, nil
		case "create":
			return http.MethodPost, collection, true, nil
		case "get":
			if err := requireID(); err != nil {
				return "", "", false, err
			}
			return http.MethodGet, "/api/v1/squads/" + id + "?workspace_id=" + url.QueryEscape(opts.Workspace), false, nil
		case "validate", "dry-run", "publish", "disable", "archive":
			if err := requireID(); err != nil {
				return "", "", false, err
			}
			return http.MethodPost, "/api/v1/squads/" + id + "/" + action + "?workspace_id=" + url.QueryEscape(opts.Workspace), false, nil
		}
	case "plan":
		switch action {
		case "list":
			return http.MethodGet, "/api/v1/execution-plans?workspace_id=" + url.QueryEscape(opts.Workspace), false, nil
		case "get":
			if err := requireID(); err != nil {
				return "", "", false, err
			}
			return http.MethodGet, "/api/v1/execution-plans/" + id + "?workspace_id=" + url.QueryEscape(opts.Workspace), false, nil
		case "create", "publish":
			if requirement == "" {
				return "", "", false, errors.New("--requirement is required")
			}
			suffix := ""
			if action == "publish" {
				suffix = "/publish"
			}
			return http.MethodPost, "/api/v1/requirements/" + requirement + "/execution-plan" + suffix, true, nil
		case "timeline":
			if err := requireID(); err != nil {
				return "", "", false, err
			}
			return http.MethodGet, "/api/v1/plans/" + id + "/timeline", false, nil
		case "replay", "diagnostics":
			if err := requireID(); err != nil {
				return "", "", false, err
			}
			return http.MethodGet, "/api/v1/runs/" + id + "/" + action, false, nil
		}
	}
	return "", "", false, fmt.Errorf("unsupported %s action %q", resource, action)
}

func requestBody(file string, required bool) ([]byte, error) {
	file = strings.TrimSpace(file)
	if file == "" {
		if required {
			return nil, errors.New("--file is required")
		}
		return nil, nil
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, fmt.Errorf("read request body: %w", err)
	}
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return nil, fmt.Errorf("request body is not valid JSON: %w", err)
	}
	return data, nil
}

func doAPIRequest(method, path string, body []byte, opts apiOptions) ([]byte, error) {
	base := strings.TrimRight(strings.TrimSpace(opts.BaseURL), "/")
	if _, err := url.ParseRequestURI(base); err != nil {
		return nil, fmt.Errorf("invalid --api URL: %w", err)
	}
	request, err := http.NewRequest(method, base+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json")
	if len(body) > 0 {
		request.Header.Set("Content-Type", "application/json")
	}
	if opts.Workspace != "" {
		request.Header.Set("X-Workspace-ID", opts.Workspace)
	}
	if opts.Tenant != "" {
		request.Header.Set("X-Tenant-ID", opts.Tenant)
	}
	if opts.Token != "" {
		request.Header.Set("Authorization", "Bearer "+opts.Token)
	}
	if opts.IdempotencyKey != "" {
		request.Header.Set("Idempotency-Key", opts.IdempotencyKey)
	}
	response, err := (&http.Client{Timeout: 30 * time.Second}).Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 24<<20))
	if err != nil {
		return nil, err
	}
	if response.StatusCode >= 300 {
		return nil, fmt.Errorf("ADRO API %s: %s", response.Status, strings.TrimSpace(string(data)))
	}
	return data, nil
}

func printJSON(data []byte) {
	var value any
	if json.Unmarshal(data, &value) == nil {
		formatted, _ := json.MarshalIndent(value, "", "  ")
		fmt.Println(string(formatted))
		return
	}
	fmt.Println(string(data))
}

func envDefault(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func graphValidate(args []string) {
	fs := flag.NewFlagSet("graph-validate", flag.ExitOnError)
	file := fs.String("file", "", "workflow graph JSON file")
	fs.Parse(args)
	if strings.TrimSpace(*file) == "" {
		fmt.Fprintln(os.Stderr, "--file is required")
		os.Exit(2)
	}
	data, err := os.ReadFile(*file)
	if err != nil {
		fmt.Fprintf(os.Stderr, "read graph: %v\n", err)
		os.Exit(1)
	}
	var graph orchestration.WorkflowGraph
	if err := json.Unmarshal(data, &graph); err != nil {
		fmt.Fprintf(os.Stderr, "decode graph: %v\n", err)
		os.Exit(1)
	}
	if err := orchestration.ValidateGraph(graph); err != nil {
		fmt.Fprintf(os.Stderr, "graph invalid: %v\n", err)
		os.Exit(1)
	}
	digest, err := graph.CanonicalHash()
	if err != nil {
		fmt.Fprintf(os.Stderr, "hash graph: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("graph valid\nvalidation_digest=%s\n", digest)
}

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
		fmt.Fprintf(os.Stderr, "local executor configuration required: %v\n", err)
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
// distribution owns the ADRO API, workbench, and filesystem volume; the local
// coding executable remains an explicit operator-selected dependency.
func install(args []string) {
	fs := flag.NewFlagSet("install", flag.ExitOnError)
	profile := fs.String("profile", "", "installation profile (single-node)")
	dryRun := fs.Bool("dry-run", false, "validate and print the native bootstrap command without starting processes")
	fs.Parse(args)
	if *profile != "single-node" {
		fmt.Fprintln(os.Stderr, "only --profile single-node is available in this release")
		os.Exit(2)
	}
	artifactRoot := filepath.Join("var", "artifacts")
	if err := os.MkdirAll(artifactRoot, 0750); err != nil {
		fmt.Fprintf(os.Stderr, "create artifact volume: %v\n", err)
		os.Exit(1)
	}
	command := []string{"./start.sh", "--no-open"}
	if *dryRun {
		fmt.Printf("profile=%s\nartifact_root=%s\ncommand=%s\n", *profile, artifactRoot, strings.Join(command, " "))
		return
	}
	cmd := exec.Command(command[0], command[1:]...)
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
