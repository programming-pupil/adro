package api

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

type openAPIOperation struct {
	ID     string `json:"operation_id"`
	Method string `json:"method"`
	Path   string `json:"path"`
}

type operationMatrixResult struct {
	OperationID string `json:"operation_id"`
	Method      string `json:"method"`
	Path        string `json:"path"`
	Status      int    `json:"status"`
	RequestID   string `json:"request_id"`
	ContentType string `json:"content_type"`
}

var pathParameterPattern = regexp.MustCompile(`\{[^}]+\}`)

// TestOpenAPIOperationMatrix sends every declared method and path through the
// real dispatcher with deliberately minimal input. Resource-family tests own
// success fixtures; this matrix proves route reachability, request correlation,
// fail-closed error media types, and complete per-operation evidence.
func TestOpenAPIOperationMatrix(t *testing.T) {
	root := repositoryRoot(t)
	operations := readOpenAPIOperations(t, filepath.Join(root, "openapi", "openapi.yaml"))
	if len(operations) == 0 {
		t.Fatal("OpenAPI operation inventory is empty")
	}

	server := testServer(t)
	results := make([]operationMatrixResult, 0, len(operations))
	seen := map[string]bool{}
	for _, operation := range operations {
		operation := operation
		t.Run(operation.ID, func(t *testing.T) {
			if seen[operation.ID] {
				t.Fatalf("duplicate operationId %q", operation.ID)
			}
			seen[operation.ID] = true
			requestPath := pathParameterPattern.ReplaceAllString(operation.Path, "coverage-missing")
			headers := map[string]string{
				"Content-Type":    "application/json",
				"Idempotency-Key": "coverage-" + operation.ID,
				"X-Tenant-ID":     "coverage-tenant",
				"X-Workspace-ID":  "coverage-workspace",
				"X-Member-ID":     "coverage-member",
			}
			response := request(t, server.Routes(), operation.Method, requestPath, `{}`, headers)
			if response.Code == http.StatusMethodNotAllowed {
				t.Fatalf("OpenAPI method is rejected by dispatcher: %s %s body=%s", operation.Method, operation.Path, response.Body.String())
			}
			if response.Code == http.StatusNotFound && strings.Contains(response.Body.String(), "route not found") {
				t.Fatalf("OpenAPI path is not routed: %s %s", operation.Method, operation.Path)
			}
			requestID := response.Header().Get("X-Request-ID")
			if requestID == "" {
				t.Fatalf("%s %s omitted X-Request-ID", operation.Method, operation.Path)
			}
			contentType := response.Header().Get("Content-Type")
			if response.Code >= 400 && operation.Method != http.MethodHead && !strings.HasPrefix(contentType, "application/problem+json") {
				t.Fatalf("%s %s returned error %d with %q instead of application/problem+json: %s", operation.Method, operation.Path, response.Code, contentType, response.Body.String())
			}
			results = append(results, operationMatrixResult{OperationID: operation.ID, Method: operation.Method, Path: operation.Path, Status: response.Code, RequestID: requestID, ContentType: contentType})
		})
	}

	if len(results) != len(operations) {
		t.Fatalf("operation matrix recorded %d/%d results", len(results), len(operations))
	}
	writeOperationMatrixEvidence(t, root, results)
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate operation matrix source")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
}

func readOpenAPIOperations(t *testing.T, path string) []openAPIOperation {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	methodPattern := regexp.MustCompile(`^    (get|post|put|patch|delete|head|options):\s*$`)
	pathPattern := regexp.MustCompile(`^  ["']?(/[^"']+)["']?:\s*$`)
	idPattern := regexp.MustCompile(`^      operationId:\s*([A-Za-z0-9_]+)\s*$`)
	var currentPath, currentMethod string
	operations := []openAPIOperation{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if match := pathPattern.FindStringSubmatch(line); match != nil {
			currentPath, currentMethod = match[1], ""
			continue
		}
		if match := methodPattern.FindStringSubmatch(line); match != nil && currentPath != "" {
			currentMethod = strings.ToUpper(match[1])
			continue
		}
		if match := idPattern.FindStringSubmatch(line); match != nil && currentPath != "" && currentMethod != "" {
			operations = append(operations, openAPIOperation{ID: match[1], Method: currentMethod, Path: currentPath})
			currentMethod = ""
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return operations
}

func writeOperationMatrixEvidence(t *testing.T, root string, results []operationMatrixResult) {
	t.Helper()
	sha := "unknown"
	if output, err := exec.Command("git", "-C", root, "rev-parse", "HEAD").Output(); err == nil {
		sha = strings.TrimSpace(string(output))
	}
	dir := filepath.Join(root, "var", "test-report", "api-operation-matrix", sha)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	report := map[string]any{"schema_version": 1, "source_sha": sha, "status": "passed", "count": len(results), "results": results}
	contents, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	contents = append(contents, '\n')
	if err := os.WriteFile(filepath.Join(dir, "report.json"), contents, 0o600); err != nil {
		t.Fatal(fmt.Errorf("write operation evidence: %w", err))
	}
}
