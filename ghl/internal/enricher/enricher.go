package enricher

import (
	"os"
	"path/filepath"
	"strings"
)

// RepoEnrichResult aggregates all NestJS + infra metadata extracted from
// a repository. Fields added post-initial-release are appended to keep the
// struct backward-compatible for any existing callers that construct it.
type RepoEnrichResult struct {
	Controllers      []NestJSMetadata
	Injectables      []NestJSMetadata
	InternalCalls    []InternalRequestCall
	EventPatterns    []EventPatternCall
	RepoPath         string
	ScheduledJobs    []ScheduledJob
	SignalEvents     []SignalEvent // cloudtask / gcp-pubsub / bullmq / redis / websocket
	HttpClientCalls  []HttpClientCall
	GrpcMethods      []GrpcMethod
	GraphQLOps       []GraphQLOp
}

var skipDirs = map[string]bool{
	"node_modules": true, ".git": true, "dist": true,
	"build": true, "coverage": true, ".next": true, ".nuxt": true,
}

// EnrichRepo walks the repo directory tree and extracts NestJS metadata from .ts files.
func EnrichRepo(repoPath string) (RepoEnrichResult, error) {
	result := RepoEnrichResult{RepoPath: repoPath}

	err := filepath.WalkDir(repoPath, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // silently skip unreadable files/dirs
		}
		if d.IsDir() {
			if skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".ts") ||
			strings.HasSuffix(d.Name(), ".d.ts") ||
			strings.HasSuffix(d.Name(), ".spec.ts") {
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return nil // silently skip unreadable files
		}
		source := string(data)

		hasNest := strings.Contains(source, "@Controller") ||
			strings.Contains(source, "@Injectable") ||
			strings.Contains(source, "InternalRequest.")
		hasEvents := strings.Contains(source, "@EventPattern") ||
			strings.Contains(source, "@MessagePattern") ||
			strings.Contains(source, "pubSub.publish") ||
			strings.Contains(source, ".emit(")
		hasExtras := strings.Contains(source, "@Cron") ||
			strings.Contains(source, "@Interval") ||
			strings.Contains(source, "@Timeout") ||
			strings.Contains(source, ".enqueue(") ||
			strings.Contains(source, ".topic(") ||
			strings.Contains(source, "@Processor") ||
			strings.Contains(source, "@Process(") ||
			strings.Contains(source, ".subscribe(") ||
			strings.Contains(source, "axios.") ||
			strings.Contains(source, "httpService.") ||
			strings.Contains(source, "fetch(") ||
			strings.Contains(source, "@SubscribeMessage") ||
			strings.Contains(source, "@GrpcMethod") ||
			strings.Contains(source, "@GrpcStreamMethod") ||
			strings.Contains(source, "@Query(") ||
			strings.Contains(source, "@Mutation(") ||
			strings.Contains(source, "@Subscription(")
		if !hasNest && !hasEvents && !hasExtras {
			return nil
		}

		relPath, _ := filepath.Rel(repoPath, path)

		meta, err := ExtractNestJSMetadata(source, relPath)
		if err != nil {
			return nil
		}

		if meta.ControllerPath != "" {
			result.Controllers = append(result.Controllers, meta)
		} else if meta.IsInjectable {
			result.Injectables = append(result.Injectables, meta)
		}

		calls, err := ExtractInternalRequests(source)
		if err != nil {
			return nil
		}
		result.InternalCalls = append(result.InternalCalls, calls...)

		// Extract event patterns
		eventPatterns := ExtractEventPatterns(source, relPath)
		result.EventPatterns = append(result.EventPatterns, eventPatterns...)

		// Tier 1/2/3 extractors.
		result.ScheduledJobs = append(result.ScheduledJobs, ExtractScheduledJobs(source, relPath)...)
		result.SignalEvents = append(result.SignalEvents, ExtractCloudTaskEnqueues(source, relPath)...)
		result.SignalEvents = append(result.SignalEvents, ExtractGcpPubSubPublishers(source, relPath)...)
		result.SignalEvents = append(result.SignalEvents, ExtractBullMQSignals(source, relPath)...)
		result.SignalEvents = append(result.SignalEvents, ExtractRedisPubSubSignals(source, relPath)...)
		result.SignalEvents = append(result.SignalEvents, ExtractWebSocketSignals(source, relPath)...)
		result.HttpClientCalls = append(result.HttpClientCalls, ExtractHttpClientCalls(source, relPath)...)
		result.GrpcMethods = append(result.GrpcMethods, ExtractGrpcMethods(source, relPath)...)
		result.GraphQLOps = append(result.GraphQLOps, ExtractGraphQLOps(source, relPath)...)

		return nil
	})

	return result, err
}
