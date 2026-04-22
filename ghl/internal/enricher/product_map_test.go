package enricher

import (
	"path/filepath"
	"testing"
)

// TestProductMap_LoadFromYAML verifies that ProductMap can be parsed from a
// well-formed YAML file containing per-repo, per-path-prefix product metadata.
func TestProductMap_LoadFromYAML(t *testing.T) {
	// Load the production data file so we verify the shipping YAML is valid.
	// Tests that rely on the file content (not just the parser) use inline
	// fixtures instead.
	path := filepath.Join("data", "product_map.yaml")
	pm, err := LoadProductMap(path)
	if err != nil {
		t.Fatalf("LoadProductMap(%q) returned error: %v", path, err)
	}
	if len(pm.Mappings) == 0 {
		t.Fatalf("LoadProductMap returned zero mappings; expected at least one bootstrap entry")
	}
	// Every mapping must declare required fields. A missing field is a data
	// bug that would produce empty customer-surface labels downstream.
	// PathPrefix is intentionally allowed to be "" — strings.HasPrefix(s, "")
	// is always true, so an empty prefix matches any file in the repo (whole-repo
	// entry for single-product repos).
	for i, m := range pm.Mappings {
		if m.Repo == "" {
			t.Errorf("mapping[%d]: empty Repo", i)
		}
		if m.Product == "" {
			t.Errorf("mapping[%d]: empty Product", i)
		}
	}
}

// TestProductMap_LongestPrefixWins verifies the lookup uses longest-prefix
// matching so that more specific sub-paths override their parent mappings.
//
// Example: "apps/iam" routes to "Platform — IAM", while parent
// "apps" routes to a broader "Platform" product.
func TestProductMap_LongestPrefixWins(t *testing.T) {
	pm := &ProductMap{
		Mappings: []ProductMapping{
			{Repo: "platform-backend", PathPrefix: "apps/", Product: "Platform", Owner: "@platform"},
			{Repo: "platform-backend", PathPrefix: "apps/iam/", Product: "Platform — IAM", Owner: "@platform-auth"},
			{Repo: "platform-backend", PathPrefix: "apps/iam/workers/", Product: "Platform — IAM Workers", Owner: "@platform-auth"},
		},
	}

	tests := []struct {
		name        string
		repo        string
		filePath    string
		wantProduct string
		wantOwner   string
	}{
		{
			name:        "most-specific prefix for workers file",
			repo:        "platform-backend",
			filePath:    "apps/iam/workers/iam-cache-populate-worker.ts",
			wantProduct: "Platform — IAM Workers",
			wantOwner:   "@platform-auth",
		},
		{
			name:        "next-most-specific prefix for non-workers iam file",
			repo:        "platform-backend",
			filePath:    "apps/iam/src/models/firestore/company.ts",
			wantProduct: "Platform — IAM",
			wantOwner:   "@platform-auth",
		},
		{
			name:        "parent prefix for unrelated apps directory",
			repo:        "platform-backend",
			filePath:    "apps/snapshots/src/service.ts",
			wantProduct: "Platform",
			wantOwner:   "@platform",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			info, found := pm.ProductForFile(tc.repo, tc.filePath)
			if !found {
				t.Fatalf("ProductForFile(%q, %q) returned not-found", tc.repo, tc.filePath)
			}
			if info.Product != tc.wantProduct {
				t.Errorf("Product = %q, want %q", info.Product, tc.wantProduct)
			}
			if info.Owner != tc.wantOwner {
				t.Errorf("Owner = %q, want %q", info.Owner, tc.wantOwner)
			}
		})
	}
}

// TestProductMap_UnknownRepoReturnsNotFound verifies that lookups for repos
// that aren't in the map return found=false. Callers can then label the
// surface "Unknown — no product mapping" rather than guessing.
func TestProductMap_UnknownRepoReturnsNotFound(t *testing.T) {
	pm := &ProductMap{
		Mappings: []ProductMapping{
			{Repo: "platform-backend", PathPrefix: "apps/", Product: "Platform", Owner: "@platform"},
		},
	}
	if _, found := pm.ProductForFile("some-other-repo", "apps/iam/file.ts"); found {
		t.Errorf("ProductForFile should have returned not-found for unknown repo")
	}
}

// TestProductMap_EmptyPathReturnsNotFound verifies that file paths that do
// not match ANY mapping prefix return not-found rather than the empty-string
// product.
func TestProductMap_EmptyPathReturnsNotFound(t *testing.T) {
	pm := &ProductMap{
		Mappings: []ProductMapping{
			{Repo: "platform-backend", PathPrefix: "apps/iam/", Product: "Platform — IAM", Owner: "@platform-auth"},
		},
	}
	if _, found := pm.ProductForFile("platform-backend", "common/utils/helper.ts"); found {
		t.Errorf("ProductForFile should have returned not-found for non-matching path")
	}
}

// TestProductMap_RepoIsolation verifies that a mapping for one repo does not
// leak into lookups for a different repo with the same path prefix.
func TestProductMap_RepoIsolation(t *testing.T) {
	pm := &ProductMap{
		Mappings: []ProductMapping{
			{Repo: "platform-backend", PathPrefix: "apps/iam/", Product: "Platform — IAM", Owner: "@platform-auth"},
			{Repo: "ghl-revex-backend", PathPrefix: "apps/iam/", Product: "Revex — IAM Adapter", Owner: "@revex"},
		},
	}
	info, found := pm.ProductForFile("ghl-revex-backend", "apps/iam/file.ts")
	if !found {
		t.Fatalf("expected found=true")
	}
	if info.Product != "Revex — IAM Adapter" {
		t.Errorf("Product = %q, want %q (repo isolation)", info.Product, "Revex — IAM Adapter")
	}
}

// TestProductMap_LoadFromYAML_MissingFile verifies that a missing YAML file
// returns a descriptive error instead of panicking.
func TestProductMap_LoadFromYAML_MissingFile(t *testing.T) {
	_, err := LoadProductMap("/tmp/this-file-does-not-exist-12345.yaml")
	if err == nil {
		t.Fatalf("expected error for missing file, got nil")
	}
}

// TestProductMap_LoadFromYAML_InvalidYAML verifies that malformed YAML returns
// an error instead of silently yielding an empty map.
func TestProductMap_LoadFromYAML_InvalidYAML(t *testing.T) {
	// Write a deliberately malformed YAML fixture into the test's temp dir.
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.yaml")
	if err := writeFile(path, "mappings: [invalid yaml here :::"); err != nil {
		t.Fatalf("writeFile: %v", err)
	}
	_, err := LoadProductMap(path)
	if err == nil {
		t.Fatalf("expected error for malformed YAML, got nil")
	}
}

// writeFile is a test helper kept here to avoid depending on os.WriteFile
// in a way that would clutter the test body. See product_map.go for the
// production file-reading code path.
func writeFile(path, content string) error {
	return writeFileBytes(path, []byte(content))
}
