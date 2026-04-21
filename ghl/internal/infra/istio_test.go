package infra

import (
	"testing"
)

func TestExtractIstioVirtualServices_SingleHostRoute(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "deployments/ghl/helm/templates/virtualservice.yaml", `
apiVersion: networking.istio.io/v1beta1
kind: VirtualService
metadata:
  name: codebase-memory-mcp
  namespace: agentic
spec:
  hosts:
    - codebase-memory-mcp.internal.gohighlevel.com
  http:
    - match:
        - uri:
            prefix: /
      route:
        - destination:
            host: codebase-memory-mcp.agentic.svc.cluster.local
            port:
              number: 80
`)

	vs, err := ExtractIstioVirtualServices(root)
	if err != nil {
		t.Fatalf("ExtractIstioVirtualServices: %v", err)
	}
	if len(vs) != 1 {
		t.Fatalf("want 1 virtualservice, got %d: %+v", len(vs), vs)
	}
	got := vs[0]
	if got.SourceApp != "codebase-memory-mcp" {
		t.Errorf("SourceApp: got %q, want codebase-memory-mcp", got.SourceApp)
	}
	if got.TargetFQDN != "codebase-memory-mcp.agentic.svc.cluster.local" {
		t.Errorf("TargetFQDN: got %q", got.TargetFQDN)
	}
	// Env is derived from /staging/ or /production/ segments. A generic
	// templates/ path has no env marker → empty is correct.
	if got.Env != "" {
		t.Errorf("Env: got %q, want empty (no env segment in path)", got.Env)
	}
}

func TestExtractIstioVirtualServices_EnvFromPath(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "deploy/staging/virtualservice.yaml", `
apiVersion: networking.istio.io/v1beta1
kind: VirtualService
metadata:
  name: app-staging
spec:
  hosts: [app.staging.example.com]
  http:
    - route:
        - destination:
            host: app.staging.svc.cluster.local
`)
	writeFile(t, root, "deploy/production/virtualservice.yaml", `
apiVersion: networking.istio.io/v1beta1
kind: VirtualService
metadata:
  name: app-prod
spec:
  hosts: [app.example.com]
  http:
    - route:
        - destination:
            host: app.production.svc.cluster.local
`)
	vs, err := ExtractIstioVirtualServices(root)
	if err != nil {
		t.Fatalf("ExtractIstioVirtualServices: %v", err)
	}
	if len(vs) != 2 {
		t.Fatalf("want 2, got %d: %+v", len(vs), vs)
	}
	byEnv := map[string]string{}
	for _, v := range vs {
		byEnv[v.Env] = v.SourceApp
	}
	if byEnv["staging"] != "app-staging" {
		t.Errorf("staging: got %q, want app-staging", byEnv["staging"])
	}
	if byEnv["production"] != "app-prod" {
		t.Errorf("production: got %q, want app-prod", byEnv["production"])
	}
}

func TestExtractIstioVirtualServices_MultipleRoutes(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "istio/services/vs.yaml", `
apiVersion: networking.istio.io/v1beta1
kind: VirtualService
metadata:
  name: revex-proxy
spec:
  hosts:
    - revex.example.com
  http:
    - route:
        - destination:
            host: revex-primary.revex.svc.cluster.local
    - route:
        - destination:
            host: revex-worker.revex.svc.cluster.local
`)
	vs, err := ExtractIstioVirtualServices(root)
	if err != nil {
		t.Fatalf("ExtractIstioVirtualServices: %v", err)
	}
	if len(vs) != 2 {
		t.Fatalf("want 2 destinations (one per http route), got %d: %+v", len(vs), vs)
	}
	for _, v := range vs {
		if v.SourceApp != "revex-proxy" {
			t.Errorf("SourceApp: got %q, want revex-proxy", v.SourceApp)
		}
	}
}

func TestExtractIstioVirtualServices_IgnoresOtherKinds(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "templates/deployment.yaml", `
apiVersion: apps/v1
kind: Deployment
metadata:
  name: not-a-virtualservice
spec:
  replicas: 1
`)
	writeFile(t, root, "templates/service.yaml", `
apiVersion: v1
kind: Service
metadata:
  name: not-a-virtualservice
`)
	vs, err := ExtractIstioVirtualServices(root)
	if err != nil {
		t.Fatalf("ExtractIstioVirtualServices: %v", err)
	}
	if len(vs) != 0 {
		t.Errorf("want 0 vs rows, got %d: %+v", len(vs), vs)
	}
}

func TestExtractIstioVirtualServices_MultiDocYaml(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "templates/combined.yaml", `
apiVersion: networking.istio.io/v1beta1
kind: VirtualService
metadata:
  name: first-vs
spec:
  hosts: [first.example.com]
  http:
    - route:
        - destination:
            host: first-backend.default.svc.cluster.local
---
apiVersion: networking.istio.io/v1beta1
kind: VirtualService
metadata:
  name: second-vs
spec:
  hosts: [second.example.com]
  http:
    - route:
        - destination:
            host: second-backend.default.svc.cluster.local
`)
	vs, err := ExtractIstioVirtualServices(root)
	if err != nil {
		t.Fatalf("ExtractIstioVirtualServices: %v", err)
	}
	if len(vs) != 2 {
		t.Fatalf("want 2 from multi-doc YAML, got %d: %+v", len(vs), vs)
	}
	names := map[string]bool{}
	for _, v := range vs {
		names[v.SourceApp] = true
	}
	if !names["first-vs"] || !names["second-vs"] {
		t.Errorf("missing expected SourceApps, got %v", names)
	}
}
