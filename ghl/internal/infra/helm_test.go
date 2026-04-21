package infra

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(p), err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
}

// T3K — Helm parser for GHL-style monorepo deployments.
//
// Real GHL repos store per-app, per-env Helm values at
// apps/<app>/deployments/<env>/values.<kind>.<env>.yaml with labels that
// include team/subTeam/repo/app/product. The parser extracts one
// DeploymentRef per values file.

func TestExtractDeployments_GhlStyleMonorepoValues(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "apps/labs/deployments/staging/values.server.staging.yaml", `
environment: staging
labels:
  team: revex
  subTeam: internal-tools
  repo: ghl-revex-backend
  app: labs
  product: labs
  DeploymentName: labs
service:
  clusterIP: None
`)
	writeFile(t, root, "apps/labs/deployments/production/values.server.production.yaml", `
environment: production
labels:
  team: revex
  subTeam: internal-tools
  repo: ghl-revex-backend
  app: labs
  DeploymentName: labs
`)
	writeFile(t, root, "apps/labs/deployments/staging/values.revex-labs-worker.yaml", `
environment: staging
labels:
  team: revex
  subTeam: internal-tools
  app: labs-worker
  DeploymentName: labs-worker
  deploymentType: worker
`)

	refs, err := ExtractDeployments(root)
	if err != nil {
		t.Fatalf("ExtractDeployments: %v", err)
	}
	if len(refs) != 3 {
		t.Fatalf("want 3 deployment refs, got %d: %+v", len(refs), refs)
	}

	byKey := map[string]DeploymentRef{}
	for _, r := range refs {
		byKey[r.AppName+":"+r.Env] = r
	}

	// server staging
	if d := byKey["labs:staging"]; d.Team != "revex" || d.SubTeam != "internal-tools" || d.DeployType != "deployment" {
		t.Errorf("labs:staging got %+v; want team=revex subTeam=internal-tools deployType=deployment", d)
	}
	// worker inferred from deploymentType
	if d := byKey["labs-worker:staging"]; d.DeployType != "worker" {
		t.Errorf("labs-worker:staging DeployType: got %q, want worker", d.DeployType)
	}
	// env is derived from filename path even if YAML is missing it
	if d := byKey["labs:production"]; d.Env != "production" {
		t.Errorf("labs:production Env: got %q, want production", d.Env)
	}
}

func TestExtractDeployments_LegacyDeploymentsYaml(t *testing.T) {
	// Some older repos just have deployments/<env>.yaml or
	// deployments/values-<env>.yaml files at the repo root.
	root := t.TempDir()
	writeFile(t, root, "deployments/values-staging.yaml", `
environment: staging
labels:
  team: platform
  app: image-processing-service
`)
	writeFile(t, root, "deployments/values-production.yaml", `
environment: production
labels:
  team: platform
  app: image-processing-service
`)
	refs, err := ExtractDeployments(root)
	if err != nil {
		t.Fatalf("ExtractDeployments: %v", err)
	}
	if len(refs) != 2 {
		t.Fatalf("want 2 refs, got %d: %+v", len(refs), refs)
	}
	for _, r := range refs {
		if r.Team != "platform" {
			t.Errorf("Team: got %q, want platform (file=%s)", r.Team, r.SourceFile)
		}
		if r.AppName != "image-processing-service" {
			t.Errorf("AppName: got %q, want image-processing-service (file=%s)", r.AppName, r.SourceFile)
		}
	}
}

func TestExtractDeployments_ChartYamlAlone(t *testing.T) {
	// A repo with a plain Helm chart should be captured by Chart.yaml alone.
	root := t.TempDir()
	writeFile(t, root, "deployments/ghl/helm/Chart.yaml", `
apiVersion: v2
name: codebase-memory-mcp
description: GHL fleet server
version: 0.1.0
`)
	writeFile(t, root, "deployments/ghl/helm/values.yaml", `
image:
  repository: gcr.io/highlevel-staging/codebase-memory-mcp-ghl
replicaCount: 1
`)
	refs, err := ExtractDeployments(root)
	if err != nil {
		t.Fatalf("ExtractDeployments: %v", err)
	}
	if len(refs) != 1 {
		t.Fatalf("want 1 ref from Chart.yaml, got %d: %+v", len(refs), refs)
	}
	ref := refs[0]
	if ref.HelmChart != "codebase-memory-mcp" {
		t.Errorf("HelmChart: got %q, want codebase-memory-mcp", ref.HelmChart)
	}
	if ref.AppName != "codebase-memory-mcp" {
		t.Errorf("AppName: got %q, want codebase-memory-mcp", ref.AppName)
	}
}

func TestExtractDeployments_SkipsNonYamlAndHidden(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "apps/bad/deployments/staging/values.json", `{"not": "yaml"}`)
	writeFile(t, root, "apps/bad/deployments/staging/.values.hidden.yaml", `labels: { team: x, app: y }`)
	writeFile(t, root, "apps/bad/node_modules/somepkg/values.yaml", `labels: { team: skip, app: skip }`)
	refs, err := ExtractDeployments(root)
	if err != nil {
		t.Fatalf("ExtractDeployments: %v", err)
	}
	if len(refs) != 0 {
		t.Errorf("want 0 refs (non-yaml/hidden/node_modules skipped), got %d: %+v", len(refs), refs)
	}
}
