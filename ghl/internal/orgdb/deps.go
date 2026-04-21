package orgdb

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// Dep represents a single GHL-internal package dependency.
type Dep struct {
	Scope       string // "@platform-core"
	Name        string // "base-service"
	DepType     string // "dependencies", "devDependencies", "peerDependencies"
	VersionSpec string // "^3.2.0"
}

// externalScopes lists npm scopes that are definitely NOT GHL-internal
// packages. Anything else starting with "@" is treated as potentially
// internal so that per-team namespaces (@revex, @membership, @contacts,
// etc.) aren't silently dropped. The cross-ref pass in
// InferPackageProviders naturally links dep rows to the real provider
// repos when they exist; unmatched rows stay as dangling metadata with
// provider_repo=NULL and don't leak into blast_radius output.
var externalScopes = map[string]bool{
	// Framework / runtime
	"@nestjs": true, "@angular": true, "@vue": true, "@nuxt": true,
	"@next": true, "@remix-run": true, "@sveltejs": true,
	"@babel": true, "@swc": true, "@vercel": true, "@netlify": true,
	"@fastify": true, "@hapi": true, "@koa": true, "@trpc": true,
	// Types
	"@types": true, "@typescript-eslint": true,
	// Cloud providers
	"@google-cloud": true, "@google": true, "@aws-sdk": true, "@smithy": true,
	"@azure": true, "@azure-rest": true, "@microsoft": true,
	"@cloudflare": true, "@firebase": true, "@heroku": true,
	// Data / eventing
	"@temporalio": true, "@apollo": true, "@graphql-tools": true,
	"@prisma": true, "@supabase": true, "@mongodb-js": true, "@elastic": true,
	"@redis": true, "@bull": true, "@bullmq": true, "@kafkajs": true,
	"@grpc": true, "@envoyproxy": true,
	// Observability
	"@sentry": true, "@sentry-internal": true, "@datadog": true,
	"@opentelemetry": true, "@rollbar": true, "@newrelic": true,
	// Linting / tooling
	"@eslint": true, "@rollup": true, "@rushstack": true, "@parcel": true,
	"@storybook": true, "@wdio": true, "@playwright": true, "@jest": true, "@vitest": true,
	"@testing-library": true, "@mock-server": true, "@faker-js": true,
	"@tanstack": true, "@tsconfig": true, "@commitlint": true,
	"@lerna": true, "@nx": true, "@pnpm": true, "@yarnpkg": true, "@turbo": true,
	// UI libraries
	"@radix-ui": true, "@emotion": true, "@mui": true, "@chakra-ui": true,
	"@fluentui": true, "@headlessui": true, "@reach": true,
	"@reduxjs": true, "@tiptap": true, "@hotwired": true,
	// Mobile / hybrid
	"@react-native": true, "@ionic": true, "@capacitor": true,
	"@expo": true, "@react-native-community": true, "@react-navigation": true,
	// SaaS SDKs
	"@slack": true, "@stripe": true, "@twilio": true, "@sendgrid": true,
	"@anthropic-ai": true, "@openai": true, "@hugging-face": true, "@langchain": true,
	// Misc
	"@sinclair": true, "@noble": true, "@panva": true, "@standard-schema": true,
	"@sigstore": true, "@npmcli": true, "@isaacs": true, "@colors": true,
	"@pkgjs": true, "@tootallnate": true, "@open-draft": true, "@mswjs": true,
}

// ParsePackageJSON reads a package.json file and extracts only GHL-internal
// dependencies — every scoped package whose scope isn't in the externalScopes
// denylist (npm framework / cloud / observability / UI library namespaces).
// External and unscoped deps are skipped.
func ParsePackageJSON(path string) ([]Dep, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("orgdb: read %s: %w", path, err)
	}

	var pkg struct {
		Dependencies     map[string]string `json:"dependencies"`
		DevDependencies  map[string]string `json:"devDependencies"`
		PeerDependencies map[string]string `json:"peerDependencies"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		return nil, fmt.Errorf("orgdb: parse %s: %w", path, err)
	}

	var deps []Dep
	extract := func(depMap map[string]string, depType string) {
		for name, version := range depMap {
			scope, pkgName := splitScoped(name)
			if scope == "" || !isInternalScope(scope) {
				continue
			}
			deps = append(deps, Dep{
				Scope:       scope,
				Name:        pkgName,
				DepType:     depType,
				VersionSpec: version,
			})
		}
	}

	extract(pkg.Dependencies, "dependencies")
	extract(pkg.DevDependencies, "devDependencies")
	extract(pkg.PeerDependencies, "peerDependencies")

	return deps, nil
}

func splitScoped(name string) (string, string) {
	if !strings.HasPrefix(name, "@") {
		return "", name
	}
	idx := strings.Index(name, "/")
	if idx < 0 {
		return "", name
	}
	return name[:idx], name[idx+1:]
}

func isInternalScope(scope string) bool {
	if scope == "" || !strings.HasPrefix(scope, "@") {
		return false
	}
	return !externalScopes[scope]
}

// ParsePackageName reads the "name" field from a package.json file and splits it
// into scope and name. For example, "@platform-core/base-service" → ("@platform-core", "base-service").
// Returns empty strings if the name is not a scoped GHL-internal package.
func ParsePackageName(path string) (scope, name string, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", "", fmt.Errorf("orgdb: read %s: %w", path, err)
	}

	var pkg struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		return "", "", fmt.Errorf("orgdb: parse %s: %w", path, err)
	}

	if pkg.Name == "" {
		return "", "", nil
	}

	s, n := splitScoped(pkg.Name)
	if s == "" || !isInternalScope(s) {
		return "", "", nil
	}
	return s, n, nil
}
