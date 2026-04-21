// Package pipeline wires the enricher and orgdb into the indexer pipeline.
// It keeps main.go clean and makes the enrichment flow testable.
package pipeline

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/GoHighLevel/codebase-memory-mcp/ghl/internal/enricher"
	"github.com/GoHighLevel/codebase-memory-mcp/ghl/internal/infra"
	"github.com/GoHighLevel/codebase-memory-mcp/ghl/internal/manifest"
	"github.com/GoHighLevel/codebase-memory-mcp/ghl/internal/orgdb"
)

// PopulateRepoData runs enrichment on a single repo and writes results to org.db.
// It clears stale data first, then inserts fresh repo metadata, dependencies,
// and API contracts (both provider and consumer sides).
func PopulateRepoData(db *orgdb.DB, repo manifest.Repo, cloneDir string) error {
	repoPath := filepath.Join(cloneDir, repo.Name)

	// 1. Clear old enrichment data for this repo
	if err := db.ClearRepoData(repo.Name); err != nil {
		return fmt.Errorf("pipeline: clear repo data %q: %w", repo.Name, err)
	}

	// 2. Upsert repo record
	if err := db.UpsertRepo(orgdb.RepoRecord{
		Name:      repo.Name,
		GitHubURL: repo.GitHubURL,
		Team:      repo.Team,
		Type:      repo.Type,
	}); err != nil {
		return fmt.Errorf("pipeline: upsert repo %q: %w", repo.Name, err)
	}

	// 3. Upsert team ownership
	if err := db.UpsertTeamOwnership(repo.Name, repo.Team, ""); err != nil {
		return fmt.Errorf("pipeline: upsert team ownership %q: %w", repo.Name, err)
	}

	// 4. Parse package.json dependencies (skip if missing)
	pkgPath := filepath.Join(repoPath, "package.json")
	if deps, err := orgdb.ParsePackageJSON(pkgPath); err == nil {
		for _, dep := range deps {
			if err := db.UpsertPackageDep(repo.Name, dep); err != nil {
				return fmt.Errorf("pipeline: upsert dep %q: %w", dep.Name, err)
			}
		}
	}

	// 4b. If this repo IS a GHL-internal package, set it as the provider
	if scope, name, err := orgdb.ParsePackageName(pkgPath); err == nil && scope != "" {
		if err := db.SetPackageProvider(scope, name, repo.Name); err != nil {
			return fmt.Errorf("pipeline: set package provider %s/%s: %w", scope, name, err)
		}
	}

	// 5. Run NestJS enricher
	result, err := enricher.EnrichRepo(repoPath)
	if err != nil {
		return fmt.Errorf("pipeline: enrich %q: %w", repo.Name, err)
	}

	// 6. Store controller routes as provider-side API contracts
	for _, ctrl := range result.Controllers {
		for _, route := range ctrl.Routes {
			path := buildPath(ctrl.ControllerPath, route.Path)
			if err := db.InsertAPIContract(orgdb.APIContract{
				ProviderRepo:   repo.Name,
				Method:         strings.ToUpper(route.Method),
				Path:           path,
				ProviderSymbol: ctrl.ClassName + "." + route.Path,
				Confidence:     0.2, // provider-only, no consumer match yet
			}); err != nil {
				return fmt.Errorf("pipeline: insert provider contract %s %s: %w", route.Method, path, err)
			}
		}
	}

	// 7. Store InternalRequest calls as consumer-side contracts
	for _, call := range result.InternalCalls {
		path := buildPath(call.ServiceName, call.Route)
		if err := db.InsertAPIContract(orgdb.APIContract{
			ConsumerRepo:   repo.Name,
			Method:         strings.ToUpper(call.Method),
			Path:           path,
			ConsumerSymbol: call.ServiceName + "." + call.Route,
			Confidence:     0.5, // consumer-only
		}); err != nil {
			return fmt.Errorf("pipeline: insert consumer contract %s %s: %w", call.Method, path, err)
		}
	}

	// 8. Store event patterns as event contracts
	for _, ep := range result.EventPatterns {
		contract := orgdb.EventContract{
			Topic:     ep.Topic,
			EventType: "pubsub",
		}
		if ep.Role == "producer" {
			contract.ProducerRepo = repo.Name
			contract.ProducerSymbol = ep.Symbol
		} else {
			contract.ConsumerRepo = repo.Name
			contract.ConsumerSymbol = ep.Symbol
		}
		if err := db.InsertEventContract(contract); err != nil {
			return fmt.Errorf("pipeline: insert event contract %q: %w", ep.Topic, err)
		}
	}

	// 9. Tier 1/2/3 signals — scheduled jobs, signal events, HTTP calls,
	//    gRPC methods, GraphQL ops. Errors on individual rows log-and-
	//    continue so one malformed entry doesn't abort the whole repo.
	for _, j := range result.ScheduledJobs {
		if err := db.InsertScheduledJob(orgdb.ScheduledJob{
			RepoName: repo.Name, Kind: j.Kind, Schedule: j.Schedule, Symbol: j.Symbol, FilePath: j.FilePath,
		}); err != nil {
			slog.Warn("pipeline: insert scheduled_job", "repo", repo.Name, "err", err)
		}
	}
	for _, s := range result.SignalEvents {
		contract := orgdb.EventContract{Topic: s.Topic, EventType: s.EventType}
		if s.Role == "producer" {
			contract.ProducerRepo = repo.Name
			contract.ProducerSymbol = s.Symbol
		} else {
			contract.ConsumerRepo = repo.Name
			contract.ConsumerSymbol = s.Symbol
		}
		if err := db.InsertEventContract(contract); err != nil {
			slog.Warn("pipeline: insert signal event", "repo", repo.Name, "type", s.EventType, "err", err)
		}
	}
	for _, h := range result.HttpClientCalls {
		if err := db.InsertHttpClientCall(orgdb.HttpClientCall{
			RepoName: repo.Name, Method: h.Method, URL: h.URL, Symbol: h.Symbol, FilePath: h.FilePath,
		}); err != nil {
			slog.Warn("pipeline: insert http_client_call", "repo", repo.Name, "err", err)
		}
	}
	for _, g := range result.GrpcMethods {
		if err := db.InsertGrpcMethod(orgdb.GrpcMethodRow{
			RepoName: repo.Name, Service: g.Service, Method: g.Method, Streaming: g.Streaming, Symbol: g.Symbol, FilePath: g.FilePath,
		}); err != nil {
			slog.Warn("pipeline: insert grpc_method", "repo", repo.Name, "err", err)
		}
	}
	for _, op := range result.GraphQLOps {
		if err := db.InsertGraphQLOp(orgdb.GraphQLOpRow{
			RepoName: repo.Name, Kind: op.Kind, Name: op.Name, Symbol: op.Symbol, FilePath: op.FilePath,
		}); err != nil {
			slog.Warn("pipeline: insert graphql_op", "repo", repo.Name, "err", err)
		}
	}

	// 10. Infra signals — Helm deployments, Istio VirtualServices, data
	//     access. These walk YAML/TS files outside the NestJS enricher.
	if deploys, err := infra.ExtractDeployments(repoPath); err == nil {
		for _, dep := range deploys {
			if err := db.InsertDeployment(orgdb.DeploymentRow{
				RepoName: repo.Name, AppName: dep.AppName, DeployType: dep.DeployType,
				Env: dep.Env, Namespace: dep.Namespace, HelmChart: dep.HelmChart,
			}); err != nil {
				slog.Warn("pipeline: insert deployment", "repo", repo.Name, "err", err)
			}
		}
	}
	if vs, err := infra.ExtractIstioVirtualServices(repoPath); err == nil {
		for _, v := range vs {
			if err := db.InsertVirtualService(orgdb.VirtualServiceRow{
				SourceRepo: repo.Name, SourceApp: v.SourceApp, TargetFQDN: v.TargetFQDN,
				TargetRepo: "", Env: v.Env,
			}); err != nil {
				slog.Warn("pipeline: insert virtual_service", "repo", repo.Name, "err", err)
			}
		}
	}
	if das, err := infra.ExtractDataAccess(repoPath); err == nil {
		for _, da := range das {
			if err := db.InsertSharedDatabase(orgdb.SharedDatabaseRow{
				ConnectionID: da.ConnectionID, DBType: da.DBType, RepoName: repo.Name,
				AccessType: da.AccessType, Collection: da.Collection,
			}); err != nil {
				slog.Warn("pipeline: insert shared_database", "repo", repo.Name, "err", err)
			}
		}
	}

	return nil
}

// PopulateOrgFromSourceClones re-enriches org.db from real source clones when
// they are available locally. This path is slower than project-db extraction
// but materially more reliable for NestJS routes, InternalRequest consumers,
// package providers, and event patterns.
func PopulateOrgFromSourceClones(ctx context.Context, db *orgdb.DB, repos []manifest.Repo, cloneDir string, workers int) (int, error) {
	if cloneDir == "" {
		return 0, nil
	}
	if workers <= 0 {
		workers = 4
	}
	if workers > 8 {
		workers = 8
	}

	type job struct {
		repo manifest.Repo
	}

	jobs := make(chan job, len(repos))
	for _, repo := range repos {
		jobs <- job{repo: repo}
	}
	close(jobs)

	var refreshed atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				select {
				case <-ctx.Done():
					return
				default:
				}

				repoPath := filepath.Join(cloneDir, j.repo.Name)
				if !hasCloneSource(repoPath) {
					continue
				}
				if err := PopulateRepoData(db, j.repo, cloneDir); err != nil {
					slog.Warn("source refresh: repo enrichment failed", "repo", j.repo.Name, "err", err)
					continue
				}
				refreshed.Add(1)
			}
		}()
	}
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return int(refreshed.Load()), err
	}

	if refreshed.Load() == 0 {
		return 0, nil
	}
	if providerCount, err := db.InferPackageProviders(); err != nil {
		slog.Warn("source refresh: infer package providers failed", "err", err)
	} else {
		slog.Info("source refresh: inferred package providers", "count", providerCount)
	}
	if matched, err := db.CrossReferenceContracts(); err != nil {
		slog.Warn("source refresh: cross-reference contracts failed", "err", err)
	} else {
		slog.Info("source refresh: cross-referenced API contracts", "matched", matched)
	}
	if matched, err := db.CrossReferenceEventContracts(); err != nil {
		slog.Warn("source refresh: cross-reference event contracts failed", "err", err)
	} else {
		slog.Info("source refresh: cross-referenced event contracts", "matched", matched)
	}
	return int(refreshed.Load()), nil
}

func hasCloneSource(repoPath string) bool {
	info, err := os.Stat(repoPath)
	if err != nil || !info.IsDir() {
		return false
	}
	entries, err := os.ReadDir(repoPath)
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if entry.Name() == ".git" {
			continue
		}
		return true
	}
	return false
}

// buildPath joins a base and suffix with a leading slash, avoiding double slashes.
func buildPath(base, suffix string) string {
	base = strings.TrimPrefix(base, "/")
	suffix = strings.TrimPrefix(suffix, "/")
	if suffix == "" {
		return "/" + base
	}
	return "/" + base + "/" + suffix
}
