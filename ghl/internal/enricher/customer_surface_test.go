package enricher

import (
	"testing"
)

// TestCustomerSurface_BuildFromFile exercises the end-to-end enrichment
// of a single Vue SFC: product-area lookup + component metadata + fetch
// calls fused into one CustomerSurface record.
func TestCustomerSurface_BuildFromFile(t *testing.T) {
	pm := &ProductMap{
		Mappings: []ProductMapping{
			{Repo: "ghl-crm-frontend", PathPrefix: "apps/settings/", Product: "CRM — Settings", Owner: "@crm-settings"},
		},
	}

	source := `
<template>
  <div>
    <h1>{{ t('settings.users.permissions.title') }}</h1>
  </div>
</template>

<script setup lang="ts">
import axios from 'axios'

const loadUser = async (id) => {
  const { data } = await axios.get('/v2/users/' + id + '/permissions')
  return data
}
</script>
`
	surface, err := BuildCustomerSurface(BuildCustomerSurfaceArgs{
		Repo:        "ghl-crm-frontend",
		FilePath:    "apps/settings/src/components/user/UserPermissionsV2.vue",
		Source:      source,
		ProductMap:  pm,
	})
	if err != nil {
		t.Fatalf("BuildCustomerSurface returned error: %v", err)
	}

	// Product area — from ProductMap lookup.
	if surface.Product != "CRM — Settings" {
		t.Errorf("Product = %q, want %q", surface.Product, "CRM — Settings")
	}
	if surface.Owner != "@crm-settings" {
		t.Errorf("Owner = %q, want %q", surface.Owner, "@crm-settings")
	}

	// Component metadata — from Vue extractor.
	if surface.ComponentName != "UserPermissionsV2" {
		t.Errorf("ComponentName = %q, want %q", surface.ComponentName, "UserPermissionsV2")
	}
	if !surface.HasScriptSetup {
		t.Errorf("HasScriptSetup = false, want true")
	}

	// Fetch calls — from FE fetch extractor.
	if len(surface.FetchCalls) != 1 {
		t.Fatalf("len(FetchCalls) = %d, want 1", len(surface.FetchCalls))
	}
	if surface.FetchCalls[0].Method != "GET" {
		t.Errorf("FetchCalls[0].Method = %q, want GET", surface.FetchCalls[0].Method)
	}

	// i18n keys — from template scan.
	if len(surface.I18nKeys) != 1 || surface.I18nKeys[0] != "settings.users.permissions.title" {
		t.Errorf("I18nKeys = %v, want [settings.users.permissions.title]", surface.I18nKeys)
	}

	// Echo of identity fields.
	if surface.Repo != "ghl-crm-frontend" {
		t.Errorf("Repo = %q", surface.Repo)
	}
	if surface.FilePath != "apps/settings/src/components/user/UserPermissionsV2.vue" {
		t.Errorf("FilePath = %q", surface.FilePath)
	}
}

// TestCustomerSurface_UnknownProductLabelled covers the "no product mapping"
// path. The surface is still produced, but product/owner get "Unknown —
// no product mapping" sentinels so downstream renderers surface the gap
// explicitly rather than silently leaving blanks.
func TestCustomerSurface_UnknownProductLabelled(t *testing.T) {
	pm := &ProductMap{Mappings: nil}

	surface, err := BuildCustomerSurface(BuildCustomerSurfaceArgs{
		Repo:       "some-new-repo",
		FilePath:   "apps/newthing/foo.vue",
		Source:     `<template><div>x</div></template><script setup></script>`,
		ProductMap: pm,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if surface.Product != "Unknown — no product mapping" {
		t.Errorf("Product = %q, want Unknown sentinel", surface.Product)
	}
	if surface.Owner != "" {
		t.Errorf("Owner = %q, want empty when product is Unknown", surface.Owner)
	}
}

// TestCustomerSurface_BackendOnlyFile covers the case where the file is a
// pure backend source (e.g., .ts worker file) — no Vue metadata, no fetch
// calls, but still labelled with product area.
func TestCustomerSurface_BackendOnlyFile(t *testing.T) {
	pm := &ProductMap{
		Mappings: []ProductMapping{
			{Repo: "platform-backend", PathPrefix: "apps/iam/workers/", Product: "Platform — IAM Cache Workers", Owner: "@platform-auth"},
		},
	}
	source := `
import IAM_REDIS_CLUSTER_CLIENT from 'common/clients/redis/iamRedisClusterClient'
import { BaseWorker } from '@platform-core/base-worker'

export default class IAMCachePopulateWorker extends BaseWorker {
  async processMessage(msg) { /* ... */ }
}
`
	surface, err := BuildCustomerSurface(BuildCustomerSurfaceArgs{
		Repo:       "platform-backend",
		FilePath:   "apps/iam/workers/iam-cache-populate-worker.ts",
		Source:     source,
		ProductMap: pm,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if surface.Product != "Platform — IAM Cache Workers" {
		t.Errorf("Product = %q", surface.Product)
	}
	if surface.ComponentName != "" {
		t.Errorf("ComponentName = %q, want empty (backend file)", surface.ComponentName)
	}
	if surface.HasScriptSetup {
		t.Errorf("HasScriptSetup = true, want false (backend file)")
	}
	if surface.HasTemplate {
		t.Errorf("HasTemplate = true, want false (backend file)")
	}
}

// TestCustomerSurface_BackendFileWithFetchCalls covers a rare but real case:
// a backend file that itself makes HTTP calls (e.g., an InternalRequest
// to another service). The fetch-call extractor should still capture those.
// NOTE: this test verifies the contract — currently the fetch extractor
// only looks for FE-style patterns (axios/fetch/$fetch/useFetch), not
// InternalRequest (which is a NestJS pattern already handled by the
// existing nestjs enricher). So a backend .ts with axios calls IS
// detected, which is correct behavior.
func TestCustomerSurface_BackendFileWithAxiosCall(t *testing.T) {
	pm := &ProductMap{}
	source := `
import axios from 'axios'
export async function pingHealth() {
  return axios.get('/v1/health')
}
`
	surface, err := BuildCustomerSurface(BuildCustomerSurfaceArgs{
		Repo:       "some-backend",
		FilePath:   "src/health.ts",
		Source:     source,
		ProductMap: pm,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(surface.FetchCalls) != 1 {
		t.Errorf("len(FetchCalls) = %d, want 1", len(surface.FetchCalls))
	}
}

// TestCustomerSurface_NilProductMapReturnsUnknown defends against a nil
// ProductMap being passed. No panic; Product labelled Unknown.
func TestCustomerSurface_NilProductMapReturnsUnknown(t *testing.T) {
	surface, err := BuildCustomerSurface(BuildCustomerSurfaceArgs{
		Repo:       "x",
		FilePath:   "a.vue",
		Source:     `<template><div /></template><script setup></script>`,
		ProductMap: nil, // defensive path
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if surface.Product != "Unknown — no product mapping" {
		t.Errorf("Product = %q, want Unknown sentinel", surface.Product)
	}
}

// TestCustomerSurface_EmptySourceYieldsMinimalRecord: edge case where the
// source is empty; we still return a record with Repo/FilePath/Product
// populated, with empty component + no fetch calls + no i18n.
// Rationale: callers batch many files; some may be empty/deleted/moved
// and we don't want one empty to abort the batch.
func TestCustomerSurface_EmptySourceYieldsMinimalRecord(t *testing.T) {
	pm := &ProductMap{
		Mappings: []ProductMapping{
			{Repo: "r", PathPrefix: "apps/", Product: "P", Owner: "@o"},
		},
	}
	surface, err := BuildCustomerSurface(BuildCustomerSurfaceArgs{
		Repo:       "r",
		FilePath:   "apps/empty.ts",
		Source:     "",
		ProductMap: pm,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if surface.Product != "P" {
		t.Errorf("Product = %q", surface.Product)
	}
	if len(surface.FetchCalls) != 0 {
		t.Errorf("FetchCalls not empty")
	}
	if surface.ComponentName != "" {
		t.Errorf("ComponentName = %q, want empty", surface.ComponentName)
	}
}
