package enricher

import (
	"testing"
)

// TestExtractFEFetchCalls_Axios covers the most common axios patterns in GHL
// frontends: direct axios.get/post/put/patch/delete and axios() with config.
func TestExtractFEFetchCalls_Axios(t *testing.T) {
	source := `
import axios from 'axios'

export async function loadUser(id) {
  const { data } = await axios.get('/v2/users/' + id + '/permissions')
  return data
}

export async function savePerms(id, body) {
  await axios.post(` + "`" + `/v2/users/${id}/permissions` + "`" + `, body)
}
`
	calls := ExtractFEFetchCalls(source, "apps/settings/src/components/user/UserPermissionsV2.vue")
	if len(calls) != 2 {
		t.Fatalf("len(calls) = %d, want 2; got %+v", len(calls), calls)
	}
	// Order must be source-order (deterministic).
	want := []FetchCall{
		{Method: "GET", URLPattern: "/v2/users/+/permissions", Style: "axios", FilePath: "apps/settings/src/components/user/UserPermissionsV2.vue", Line: 5},
		{Method: "POST", URLPattern: "/v2/users/${id}/permissions", Style: "axios", FilePath: "apps/settings/src/components/user/UserPermissionsV2.vue", Line: 10},
	}
	for i, w := range want {
		if i >= len(calls) {
			t.Fatalf("missing call[%d]", i)
		}
		got := calls[i]
		if got.Method != w.Method || got.Style != w.Style || got.FilePath != w.FilePath {
			t.Errorf("call[%d] = {Method:%q, Style:%q, FilePath:%q}, want {Method:%q, Style:%q, FilePath:%q}",
				i, got.Method, got.Style, got.FilePath, w.Method, w.Style, w.FilePath)
		}
		if got.URLPattern == "" {
			t.Errorf("call[%d].URLPattern is empty; want non-empty URL", i)
		}
	}
}

// TestExtractFEFetchCalls_Fetch covers native fetch() calls.
func TestExtractFEFetchCalls_Fetch(t *testing.T) {
	source := `
export function refreshLocations() {
  return fetch('/v2/locations', { method: 'GET' }).then(r => r.json())
}
`
	calls := ExtractFEFetchCalls(source, "apps/crm/src/store/locations.ts")
	if len(calls) != 1 {
		t.Fatalf("len(calls) = %d, want 1", len(calls))
	}
	if calls[0].Style != "fetch" {
		t.Errorf("Style = %q, want %q", calls[0].Style, "fetch")
	}
	if calls[0].URLPattern != "/v2/locations" {
		t.Errorf("URLPattern = %q, want %q", calls[0].URLPattern, "/v2/locations")
	}
}

// TestExtractFEFetchCalls_NuxtDollarFetch covers Nuxt 3's $fetch helper.
func TestExtractFEFetchCalls_NuxtDollarFetch(t *testing.T) {
	source := `
const data = await $fetch('/v2/courses/' + courseId, { method: 'GET' })
`
	calls := ExtractFEFetchCalls(source, "apps/courses/src/pages/[id].vue")
	if len(calls) != 1 {
		t.Fatalf("len(calls) = %d, want 1", len(calls))
	}
	if calls[0].Style != "$fetch" {
		t.Errorf("Style = %q, want %q", calls[0].Style, "$fetch")
	}
}

// TestExtractFEFetchCalls_UseFetch covers Vue Query / useFetch composables.
func TestExtractFEFetchCalls_UseFetch(t *testing.T) {
	source := `
const { data } = useFetch('/v2/users/me')
`
	calls := ExtractFEFetchCalls(source, "apps/settings/src/components/UserMenu.vue")
	if len(calls) != 1 {
		t.Fatalf("len(calls) = %d, want 1", len(calls))
	}
	if calls[0].Style != "useFetch" {
		t.Errorf("Style = %q, want %q", calls[0].Style, "useFetch")
	}
}

// TestExtractFEFetchCalls_MultipleInOneFile verifies that multiple heterogeneous
// fetch calls in one file are each captured with correct line numbers.
func TestExtractFEFetchCalls_MultipleInOneFile(t *testing.T) {
	source := `
import axios from 'axios'

async function a() { return axios.get('/v2/a') }
async function b() { return fetch('/v2/b') }
async function c() { return $fetch('/v2/c') }
`
	calls := ExtractFEFetchCalls(source, "apps/x/y.vue")
	if len(calls) != 3 {
		t.Fatalf("len(calls) = %d, want 3; got %+v", len(calls), calls)
	}
	if calls[0].Method != "GET" || calls[0].Style != "axios" {
		t.Errorf("call[0] = %+v, want axios GET", calls[0])
	}
	if calls[1].Style != "fetch" {
		t.Errorf("call[1].Style = %q, want fetch", calls[1].Style)
	}
	if calls[2].Style != "$fetch" {
		t.Errorf("call[2].Style = %q, want $fetch", calls[2].Style)
	}
	// Ascending line numbers — preserves source order.
	if !(calls[0].Line < calls[1].Line && calls[1].Line < calls[2].Line) {
		t.Errorf("lines not in source order: %d, %d, %d", calls[0].Line, calls[1].Line, calls[2].Line)
	}
}

// TestExtractFEFetchCalls_NoFalsePositivesInComments verifies the extractor
// doesn't fire on strings that look like fetch calls but are inside comments.
// This matters because SFC templates and docstrings often contain example code.
func TestExtractFEFetchCalls_NoFalsePositivesInComments(t *testing.T) {
	source := `
// Example: fetch('/v2/example')
/* axios.post('/v2/old-endpoint') */
const real = axios.get('/v2/real')
`
	calls := ExtractFEFetchCalls(source, "apps/x/y.ts")
	if len(calls) != 1 {
		t.Fatalf("len(calls) = %d, want 1 (only the non-commented call); got %+v", len(calls), calls)
	}
	if calls[0].URLPattern != "/v2/real" {
		t.Errorf("URLPattern = %q, want %q", calls[0].URLPattern, "/v2/real")
	}
}

// TestExtractFEFetchCalls_EmptySource verifies the extractor handles empty
// or whitespace-only input without panic.
func TestExtractFEFetchCalls_EmptySource(t *testing.T) {
	if calls := ExtractFEFetchCalls("", "empty.ts"); calls != nil {
		t.Errorf("expected nil for empty source, got %+v", calls)
	}
	if calls := ExtractFEFetchCalls("\n\n   \n", "whitespace.ts"); calls != nil {
		t.Errorf("expected nil for whitespace-only source, got %+v", calls)
	}
}
