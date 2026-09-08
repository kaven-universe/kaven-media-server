package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"kaven.xyz/kaven/kaven-media-server/internal/buildinfo"
	"kaven.xyz/kaven/kaven-media-server/internal/integrity"
)

func TestRunCheck(t *testing.T) {
	dataDir := t.TempDir()
	for _, directory := range []string{"images", "cache", "bing"} {
		if err := os.Mkdir(filepath.Join(dataDir, directory), 0o755); err != nil {
			t.Fatalf("create %s: %v", directory, err)
		}
	}
	var output bytes.Buffer
	if err := runCheck(context.Background(), dataDir, &output, false); err != nil {
		t.Fatalf("run check: %v", err)
	}
	if !strings.Contains(output.String(), "status: ok") {
		t.Fatalf("check output = %q", output.String())
	}
	output.Reset()
	if err := runCheck(context.Background(), dataDir, &output, true); err != nil {
		t.Fatalf("run JSON check: %v", err)
	}
	if !strings.Contains(output.String(), `"healthy":true`) || !strings.Contains(output.String(), `"issues":[]`) {
		t.Fatalf("healthy JSON check output = %q", output.String())
	}
	var jsonReport struct {
		Execution      *integrity.ExecutionMetadata `json:"execution"`
		SchemaVersions []int                        `json:"schemaVersions"`
		TableCounts    integrity.TableCounts        `json:"tableCounts"`
	}
	if err := json.Unmarshal(output.Bytes(), &jsonReport); err != nil {
		t.Fatal(err)
	}
	if jsonReport.Execution == nil || jsonReport.Execution.GeneratedAt.IsZero() || jsonReport.Execution.Producer.Version == "" || jsonReport.Execution.Producer.Revision == "" {
		t.Fatalf("integrity execution metadata = %#v", jsonReport.Execution)
	}
	if len(jsonReport.SchemaVersions) != 1 || jsonReport.SchemaVersions[0] != 1 {
		t.Fatalf("integrity schema versions = %v", jsonReport.SchemaVersions)
	}
	if jsonReport.TableCounts != (integrity.TableCounts{}) {
		t.Fatalf("integrity table counts = %#v", jsonReport.TableCounts)
	}

	if err := os.WriteFile(filepath.Join(dataDir, "images", "orphan.jpg"), []byte("orphan"), 0o600); err != nil {
		t.Fatalf("write orphan: %v", err)
	}
	output.Reset()
	err := runCheck(context.Background(), dataDir, &output, false)
	if err == nil || !strings.Contains(err.Error(), "found 1 issue(s)") {
		t.Fatalf("unhealthy check error = %v", err)
	}
	if !strings.Contains(output.String(), "orphan_file images/orphan.jpg") {
		t.Fatalf("unhealthy check output = %q", output.String())
	}
	output.Reset()
	err = runCheck(context.Background(), dataDir, &output, true)
	if err == nil || !strings.Contains(output.String(), `"healthy":false`) || !strings.Contains(output.String(), `"issueCount":1`) || !strings.Contains(output.String(), `"execution"`) {
		t.Fatalf("JSON check error/output = %v / %q", err, output.String())
	}
}

func TestRunVerifyCIRejectsIncompleteArguments(t *testing.T) {
	if err := runVerifyCI([]string{"--evidence-dir", t.TempDir()}, io.Discard); err == nil || !strings.Contains(err.Error(), "requires --evidence-dir and --revision") {
		t.Fatalf("verify CI argument error = %v", err)
	}
}

func TestCheckCommandRejectsPositionalArguments(t *testing.T) {
	originalArgs := os.Args
	os.Args = []string{"kaven-media", "check", "unexpected"}
	t.Cleanup(func() { os.Args = originalArgs })
	if err := run(); err == nil || !strings.Contains(err.Error(), "no positional arguments") {
		t.Fatalf("check positional argument error = %v", err)
	}
}

func TestVersionInformationIsMachineReadable(t *testing.T) {
	var output bytes.Buffer
	if err := runVersion(nil, &output); err != nil {
		t.Fatal(err)
	}
	var info buildinfo.Info
	if err := json.NewDecoder(&output).Decode(&info); err != nil {
		t.Fatal(err)
	}
	if info.Version == "" || info.Revision == "" || info.GoVersion == "" {
		t.Fatalf("version info = %#v", info)
	}
	if err := runVersion([]string{"extra"}, io.Discard); err == nil {
		t.Fatal("version accepted an argument")
	}
}
