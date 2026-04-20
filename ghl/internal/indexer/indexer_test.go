package indexer_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/GoHighLevel/codebase-memory-mcp/ghl/internal/indexer"
	"github.com/GoHighLevel/codebase-memory-mcp/ghl/internal/manifest"
)

// ── Fake MCP client ────────────────────────────────────────────

type fakeClient struct {
	indexCalls   atomic.Int64
	shouldFail   bool
	callDuration time.Duration
}

func (f *fakeClient) IndexRepository(ctx context.Context, repoPath, mode, projectName string) error {
	f.indexCalls.Add(1)
	if f.callDuration > 0 {
		select {
		case <-time.After(f.callDuration):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if f.shouldFail {
		return errors.New("fake index error")
	}
	return nil
}

// ── Fake cloner ────────────────────────────────────────────────

type fakeCloner struct {
	cloneCalls atomic.Int64
	shouldFail bool
}

func (f *fakeCloner) EnsureClone(ctx context.Context, githubURL, localPath string) error {
	f.cloneCalls.Add(1)
	if f.shouldFail {
		return errors.New("fake clone error")
	}
	return nil
}

// ── Tests ──────────────────────────────────────────────────────

func sampleRepos(n int) []manifest.Repo {
	repos := make([]manifest.Repo, n)
	for i := range repos {
		repos[i] = manifest.Repo{
			Name:      "repo-" + string(rune('a'+i)),
			GitHubURL: "https://github.com/GoHighLevel/repo-" + string(rune('a'+i)),
			Team:      "revex",
			Type:      "backend",
		}
	}
	return repos
}

func TestIndexer_IndexAll_AllReposIndexed(t *testing.T) {
	client := &fakeClient{}
	cloner := &fakeCloner{}
	repos := sampleRepos(5)

	idx := indexer.New(indexer.Config{
		Client:      client,
		Cloner:      cloner,
		CacheDir:    t.TempDir(),
		Concurrency: 2,
	})

	ctx := context.Background()
	result := idx.IndexAll(ctx, repos, false)

	if result.Total != 5 {
		t.Errorf("Total: want 5, got %d", result.Total)
	}
	if result.Succeeded != 5 {
		t.Errorf("Succeeded: want 5, got %d", result.Succeeded)
	}
	if result.Failed != 0 {
		t.Errorf("Failed: want 0, got %d", result.Failed)
	}
	if client.indexCalls.Load() != 5 {
		t.Errorf("IndexRepository calls: want 5, got %d", client.indexCalls.Load())
	}
	if cloner.cloneCalls.Load() != 5 {
		t.Errorf("EnsureClone calls: want 5, got %d", cloner.cloneCalls.Load())
	}
}

func TestIndexer_IndexAll_ContinuesOnError(t *testing.T) {
	client := &fakeClient{shouldFail: true}
	cloner := &fakeCloner{}
	repos := sampleRepos(3)

	idx := indexer.New(indexer.Config{
		Client:      client,
		Cloner:      cloner,
		CacheDir:    t.TempDir(),
		Concurrency: 1,
	})

	ctx := context.Background()
	result := idx.IndexAll(ctx, repos, false)

	// All failed, but all were attempted — must not stop on first error
	if result.Total != 3 {
		t.Errorf("Total: want 3, got %d", result.Total)
	}
	if result.Failed != 3 {
		t.Errorf("Failed: want 3, got %d", result.Failed)
	}
	if result.Succeeded != 0 {
		t.Errorf("Succeeded: want 0, got %d", result.Succeeded)
	}
	if len(result.Errors) != 3 {
		t.Errorf("Errors: want 3, got %d", len(result.Errors))
	}
}

func TestIndexer_IndexAll_ConcurrencyLimit(t *testing.T) {
	const concurrency = 3
	const totalRepos = 9

	var inFlight atomic.Int64
	var maxInFlight atomic.Int64

	client := &fakeClient{callDuration: 20 * time.Millisecond}
	cloner := &fakeCloner{}

	// Wrap the client to track in-flight count
	idx := indexer.New(indexer.Config{
		Client:      client,
		Cloner:      cloner,
		CacheDir:    t.TempDir(),
		Concurrency: concurrency,
		OnRepoStart: func(_ string) {
			cur := inFlight.Add(1)
			for {
				old := maxInFlight.Load()
				if cur <= old || maxInFlight.CompareAndSwap(old, cur) {
					break
				}
			}
		},
		OnRepoDone: func(_ string, _ error) {
			inFlight.Add(-1)
		},
	})

	ctx := context.Background()
	idx.IndexAll(ctx, sampleRepos(totalRepos), false)

	if got := maxInFlight.Load(); got > int64(concurrency) {
		t.Errorf("max in-flight: want <= %d, got %d (concurrency limit exceeded)", concurrency, got)
	}
}

func TestIndexer_IndexAll_ContextCancellation(t *testing.T) {
	client := &fakeClient{callDuration: 500 * time.Millisecond}
	cloner := &fakeCloner{}
	repos := sampleRepos(10)

	idx := indexer.New(indexer.Config{
		Client:      client,
		Cloner:      cloner,
		CacheDir:    t.TempDir(),
		Concurrency: 2,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	result := idx.IndexAll(ctx, repos, false)

	// With 500ms per repo and 50ms total timeout, we can't finish all 10
	if result.Succeeded == 10 {
		t.Error("expected context cancellation to stop indexing before all 10 repos complete")
	}
}

func TestIndexer_IndexRepo_SingleRepo(t *testing.T) {
	client := &fakeClient{}
	cloner := &fakeCloner{}

	idx := indexer.New(indexer.Config{
		Client:      client,
		Cloner:      cloner,
		CacheDir:    t.TempDir(),
		Concurrency: 1,
	})

	repo := manifest.Repo{
		Name:      "membership-backend",
		GitHubURL: "https://github.com/GoHighLevel/membership-backend",
	}

	ctx := context.Background()
	err := idx.IndexRepo(ctx, repo, false)
	if err != nil {
		t.Errorf("IndexRepo: unexpected error: %v", err)
	}
	if client.indexCalls.Load() != 1 {
		t.Errorf("IndexRepository calls: want 1, got %d", client.indexCalls.Load())
	}
}

func TestIndexer_IndexRepo_CloneFailure(t *testing.T) {
	client := &fakeClient{}
	cloner := &fakeCloner{shouldFail: true}

	idx := indexer.New(indexer.Config{
		Client:      client,
		Cloner:      cloner,
		CacheDir:    t.TempDir(),
		Concurrency: 1,
	})

	repo := manifest.Repo{
		Name:      "membership-backend",
		GitHubURL: "https://github.com/GoHighLevel/membership-backend",
	}

	ctx := context.Background()
	err := idx.IndexRepo(ctx, repo, false)
	if err == nil {
		t.Error("IndexRepo: expected error from clone failure, got nil")
	}
	// Should not have tried to index if clone failed
	if client.indexCalls.Load() != 0 {
		t.Errorf("IndexRepository: should not be called if clone fails, got %d calls", client.indexCalls.Load())
	}
}

func TestIndexer_EmptyRepoList(t *testing.T) {
	client := &fakeClient{}
	cloner := &fakeCloner{}

	idx := indexer.New(indexer.Config{
		Client:      client,
		Cloner:      cloner,
		CacheDir:    t.TempDir(),
		Concurrency: 5,
	})

	ctx := context.Background()
	result := idx.IndexAll(ctx, []manifest.Repo{}, false)

	if result.Total != 0 {
		t.Errorf("Total: want 0, got %d", result.Total)
	}
	if result.Succeeded != 0 {
		t.Errorf("Succeeded: want 0, got %d", result.Succeeded)
	}
}

func TestIndexer_IndexAll_CallsOnAllComplete(t *testing.T) {
	var gotResult indexer.IndexResult
	called := false

	client := &fakeClient{}
	cloner := &fakeCloner{}
	idx := indexer.New(indexer.Config{
		Client:      client,
		Cloner:      cloner,
		CacheDir:    t.TempDir(),
		Concurrency: 2,
		OnAllComplete: func(result indexer.IndexResult) {
			called = true
			gotResult = result
		},
	})

	repos := sampleRepos(3)
	idx.IndexAll(context.Background(), repos, false)

	if !called {
		t.Fatal("OnAllComplete was not called")
	}
	if gotResult.Total != 3 {
		t.Errorf("Total: got %d, want 3", gotResult.Total)
	}
	if gotResult.Succeeded != 3 {
		t.Errorf("Succeeded: got %d, want 3", gotResult.Succeeded)
	}
}

func TestIndexer_LocalCachePath(t *testing.T) {
	cacheDir := t.TempDir()
	var capturedPath string

	client := &fakeClient{}
	cloner := &fakeCloner{}

	idx := indexer.New(indexer.Config{
		Client:   client,
		Cloner:   cloner,
		CacheDir: cacheDir,
		OnClone: func(_, path string) {
			capturedPath = path
		},
		Concurrency: 1,
	})

	repo := manifest.Repo{
		Name:      "membership-backend",
		GitHubURL: "https://github.com/GoHighLevel/membership-backend",
	}

	ctx := context.Background()
	_ = idx.IndexRepo(ctx, repo, false)

	expected := cacheDir + "/membership-backend"
	if capturedPath != expected {
		t.Errorf("clone path: want %q, got %q", expected, capturedPath)
	}
}

// ── Activity checker tests ──────────────────────────────────────

type fakeActivityChecker struct {
	activeRepos map[string]bool
}

func (f *fakeActivityChecker) IsActive(_ context.Context, repoName string) bool {
	return f.activeRepos[repoName]
}

func TestIndexer_IndexAll_SkipsInactiveRepos(t *testing.T) {
	client := &fakeClient{}
	cloner := &fakeCloner{}
	repos := sampleRepos(5) // repo-a through repo-e

	checker := &fakeActivityChecker{
		activeRepos: map[string]bool{
			"repo-a": true,
			"repo-c": true,
			// repo-b, repo-d, repo-e are stale
		},
	}

	idx := indexer.New(indexer.Config{
		Client:          client,
		Cloner:          cloner,
		CacheDir:        t.TempDir(),
		Concurrency:     2,
		ActivityChecker: checker,
	})

	result := idx.IndexAll(context.Background(), repos, false)

	if result.Total != 5 {
		t.Errorf("Total: want 5, got %d", result.Total)
	}
	if result.Succeeded != 2 {
		t.Errorf("Succeeded: want 2, got %d", result.Succeeded)
	}
	if result.Skipped != 3 {
		t.Errorf("Skipped: want 3, got %d", result.Skipped)
	}
	if result.Failed != 0 {
		t.Errorf("Failed: want 0, got %d", result.Failed)
	}
	if client.indexCalls.Load() != 2 {
		t.Errorf("IndexRepository calls: want 2, got %d", client.indexCalls.Load())
	}
}

func TestIndexer_IndexAll_ForceIgnoresActivityChecker(t *testing.T) {
	client := &fakeClient{}
	cloner := &fakeCloner{}
	repos := sampleRepos(3)

	checker := &fakeActivityChecker{
		activeRepos: map[string]bool{}, // all repos are "stale"
	}

	idx := indexer.New(indexer.Config{
		Client:          client,
		Cloner:          cloner,
		CacheDir:        t.TempDir(),
		Concurrency:     2,
		ActivityChecker: checker,
	})

	result := idx.IndexAll(context.Background(), repos, true) // force=true

	if result.Succeeded != 3 {
		t.Errorf("Succeeded: want 3 (force=true overrides activity check), got %d", result.Succeeded)
	}
	if result.Skipped != 0 {
		t.Errorf("Skipped: want 0, got %d", result.Skipped)
	}
}

// ── Project name func tests ────────────────────────────────────

type projectNameCapture struct {
	fakeClient
	capturedNames []string
	mu            sync.Mutex
}

func (p *projectNameCapture) IndexRepository(ctx context.Context, repoPath, mode, projectName string) error {
	p.mu.Lock()
	p.capturedNames = append(p.capturedNames, projectName)
	p.mu.Unlock()
	return p.fakeClient.IndexRepository(ctx, repoPath, mode, projectName)
}

func TestIndexer_IndexRepo_PassesProjectName(t *testing.T) {
	client := &projectNameCapture{}
	cloner := &fakeCloner{}

	idx := indexer.New(indexer.Config{
		Client:   client,
		Cloner:   cloner,
		CacheDir: "/tmp/fleet-repos",
		ProjectNameFunc: func(slug string) string {
			return "data-fleet-cache-repos-" + slug
		},
		Concurrency: 1,
	})

	repo := manifest.Repo{
		Name:      "membership-backend",
		GitHubURL: "https://github.com/GoHighLevel/membership-backend",
	}

	if err := idx.IndexRepo(context.Background(), repo, false); err != nil {
		t.Fatalf("IndexRepo: %v", err)
	}

	if len(client.capturedNames) != 1 {
		t.Fatalf("expected 1 project name, got %d", len(client.capturedNames))
	}
	if client.capturedNames[0] != "data-fleet-cache-repos-membership-backend" {
		t.Errorf("project name: want %q, got %q", "data-fleet-cache-repos-membership-backend", client.capturedNames[0])
	}
}

func TestIndexer_IndexRepo_EmptyProjectNameWhenNoFunc(t *testing.T) {
	client := &projectNameCapture{}
	cloner := &fakeCloner{}

	idx := indexer.New(indexer.Config{
		Client:      client,
		Cloner:      cloner,
		CacheDir:    t.TempDir(),
		Concurrency: 1,
		// ProjectNameFunc is nil
	})

	repo := manifest.Repo{
		Name:      "membership-backend",
		GitHubURL: "https://github.com/GoHighLevel/membership-backend",
	}

	if err := idx.IndexRepo(context.Background(), repo, false); err != nil {
		t.Fatalf("IndexRepo: %v", err)
	}

	if len(client.capturedNames) != 1 || client.capturedNames[0] != "" {
		t.Errorf("project name: want empty, got %q", client.capturedNames[0])
	}
}
