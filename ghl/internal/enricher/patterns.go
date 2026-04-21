package enricher

import (
	"regexp"
	"strings"
)

// ScheduledJob is an @Cron / @Interval / @Timeout declaration. These are
// produced by the enricher but don't live in api_contracts or
// event_contracts — they get their own scheduled_jobs table.
type ScheduledJob struct {
	Kind     string // "cron", "interval", "timeout"
	Schedule string // literal cron expr, interval ms, or a CronExpression.* reference
	Symbol   string // class name (best-effort)
	FilePath string
}

// SignalEvent is a generic producer/consumer signal for a messaging channel
// that isn't a classic NestJS @EventPattern / @MessagePattern. It covers
// cloudtask enqueues, GCP PubSub SDK publishers, BullMQ queue add/process,
// Redis pub/sub, and WebSocket handlers — all of which collapse onto the
// existing event_contracts table via the EventType discriminator.
type SignalEvent struct {
	Topic     string
	Role      string // "producer" or "consumer"
	EventType string // "cloudtask" | "pubsub" | "bullmq" | "redis" | "websocket"
	Symbol    string
	FilePath  string
}

// HttpClientCall is an outbound HTTP call with a literal URL. Stored into
// api_contracts as a consumer row with the URL in the Path column and no
// provider_repo; the cross-ref pass may still match if a known internal
// service's base path appears in the URL.
type HttpClientCall struct {
	Method   string
	URL      string
	Symbol   string
	FilePath string
}

// GrpcMethod represents a @GrpcMethod / @GrpcStreamMethod handler.
type GrpcMethod struct {
	Service   string
	Method    string
	Streaming bool
	Symbol    string
	FilePath  string
}

// GraphQLOp represents an @Query / @Mutation / @Subscription handler.
type GraphQLOp struct {
	Kind     string // "query" | "mutation" | "subscription"
	Name     string // field name (may be empty — inferred from method name by NestJS default)
	Symbol   string
	FilePath string
}

// ---------------------------------------------------------------------------
// Regexes — shared. All use multiline/dotall flags via (?s).
// ---------------------------------------------------------------------------

var (
	// T1B scheduled jobs. Captures schedule string or identifier expression.
	reCronDecorator     = regexp.MustCompile(`@Cron\(\s*(?:['"]([^'"]+)['"]|([A-Za-z_][A-Za-z0-9_.]*))`)
	reIntervalDecorator = regexp.MustCompile(`@Interval\(\s*(\d+)\s*\)`)
	reTimeoutDecorator  = regexp.MustCompile(`@Timeout\(\s*(\d+)\s*\)`)

	// T1C Cloud Tasks. Matches any identifier ending in ".enqueue('name', ...)"
	// where the receiver looks like a cloud-tasks client.
	reCloudTaskEnqueue = regexp.MustCompile(
		`(?:cloudTasks|cloud_tasks|tasks|taskService|taskClient|CloudTasksService|cloudTasksClient)\s*\.\s*enqueue\(\s*['"]([^'"]+)['"]`,
	)

	// T2D GCP Pub/Sub SDK. pubsub.topic('X').publish OR .publishMessage
	// Also captures the variable-reference form for downstream resolution.
	reGcpPubSubTopic = regexp.MustCompile(
		`(?:pubsub|pubSub|pubSubClient|pubsubClient|pubSubService)\s*\.\s*topic\(\s*(?:['"]([^'"]+)['"]|([A-Za-z_][A-Za-z0-9_.]*))\s*\)\s*\.\s*publish(?:Message)?\(`,
	)

	// T2E BullMQ.
	reBullMQProcessor = regexp.MustCompile(`@Processor\(\s*['"]([^'"]+)['"]\s*\)`)
	reBullMQProcess   = regexp.MustCompile(`@Process\(\s*['"]([^'"]+)['"]\s*\)`)
	// queue.add('job', ...) — accepts any `<ident>.add('name', ...)` call that
	// doesn't look like an array/list .add (caller context filters those).
	reBullMQAdd = regexp.MustCompile(`\b([A-Za-z_][A-Za-z0-9_]*)\s*\.\s*add\(\s*['"]([^'"]+)['"]`)

	// T2F Redis pub/sub. Captures redis or redisClient or <ident>Redis etc.
	reRedisPublish   = regexp.MustCompile(`(?:redis|redisClient|redisService|redisPublisher|redisPubSub|[A-Za-z_][A-Za-z0-9_]*Redis)\s*\.\s*publish\(\s*['"]([^'"]+)['"]`)
	reRedisSubscribe = regexp.MustCompile(`(?:redis|redisClient|redisService|redisSubscriber|redisPubSub|[A-Za-z_][A-Za-z0-9_]*Redis)\s*\.\s*subscribe\(\s*['"]([^'"]+)['"]`)

	// T2G HTTP client calls (literal URL only).
	reAxiosCall       = regexp.MustCompile(`axios\s*\.\s*(get|post|put|delete|patch)\(\s*['"](https?://[^'"\s]+)['"]`)
	reHttpServiceCall = regexp.MustCompile(`httpService\s*\.\s*(get|post|put|delete|patch)\(\s*['"](https?://[^'"\s]+)['"]`)
	reFetchCall       = regexp.MustCompile(`\bfetch\(\s*['"](https?://[^'"\s]+)['"]`)

	// T3H WebSocket.
	reSubscribeMessage = regexp.MustCompile(`@SubscribeMessage\(\s*['"]([^'"]+)['"]\s*\)`)

	// T3I gRPC.
	reGrpcMethod       = regexp.MustCompile(`@GrpcMethod\(\s*['"]([^'"]+)['"]\s*,\s*['"]([^'"]+)['"]\s*\)`)
	reGrpcStreamMethod = regexp.MustCompile(`@GrpcStreamMethod\(\s*['"]([^'"]+)['"]\s*,\s*['"]([^'"]+)['"]\s*\)`)

	// T3J GraphQL — @Query, @Mutation, @Subscription (each optionally parameterized).
	reGraphqlQuery        = regexp.MustCompile(`@Query\(`)
	reGraphqlMutation     = regexp.MustCompile(`@Mutation\(`)
	reGraphqlSubscription = regexp.MustCompile(`@Subscription\(`)
)

// firstClassName returns the first `export class X` name found in source,
// falling back to "".
func firstClassName(source string) string {
	m := reClassName.FindStringSubmatch(source)
	if m == nil {
		return ""
	}
	return m[1]
}

// ExtractScheduledJobs finds @Cron/@Interval/@Timeout decorators. The
// schedule field preserves either the literal string or the identifier
// expression (e.g. "CronExpression.EVERY_5_MINUTES") so the consumer can
// decide how to resolve it.
func ExtractScheduledJobs(source, filePath string) []ScheduledJob {
	className := firstClassName(source)
	var jobs []ScheduledJob

	for _, m := range reCronDecorator.FindAllStringSubmatch(source, -1) {
		sched := m[1]
		if sched == "" {
			sched = m[2]
		}
		jobs = append(jobs, ScheduledJob{Kind: "cron", Schedule: sched, Symbol: className, FilePath: filePath})
	}
	for _, m := range reIntervalDecorator.FindAllStringSubmatch(source, -1) {
		jobs = append(jobs, ScheduledJob{Kind: "interval", Schedule: m[1], Symbol: className, FilePath: filePath})
	}
	for _, m := range reTimeoutDecorator.FindAllStringSubmatch(source, -1) {
		jobs = append(jobs, ScheduledJob{Kind: "timeout", Schedule: m[1], Symbol: className, FilePath: filePath})
	}
	return jobs
}

// ExtractCloudTaskEnqueues finds cloudTasks.enqueue('name', ...) calls and
// returns producer signals.
func ExtractCloudTaskEnqueues(source, filePath string) []SignalEvent {
	className := firstClassName(source)
	var out []SignalEvent
	for _, m := range reCloudTaskEnqueue.FindAllStringSubmatch(source, -1) {
		out = append(out, SignalEvent{
			Topic:     m[1],
			Role:      "producer",
			EventType: "cloudtask",
			Symbol:    className,
			FilePath:  filePath,
		})
	}
	return out
}

// ExtractGcpPubSubPublishers finds pubsub.topic('name').publish|publishMessage
// calls. If the topic argument is a variable reference (not a literal) we
// still capture the identifier — consumers often want to grep those later.
func ExtractGcpPubSubPublishers(source, filePath string) []SignalEvent {
	className := firstClassName(source)
	var out []SignalEvent
	for _, m := range reGcpPubSubTopic.FindAllStringSubmatch(source, -1) {
		topic := m[1]
		if topic == "" {
			topic = m[2]
		}
		out = append(out, SignalEvent{
			Topic:     topic,
			Role:      "producer",
			EventType: "pubsub",
			Symbol:    className,
			FilePath:  filePath,
		})
	}
	return out
}

// ExtractBullMQSignals finds BullMQ queue.add producer calls plus
// @Processor / @Process consumer decorators.
func ExtractBullMQSignals(source, filePath string) []SignalEvent {
	className := firstClassName(source)
	var out []SignalEvent
	// Consumers
	for _, m := range reBullMQProcessor.FindAllStringSubmatch(source, -1) {
		out = append(out, SignalEvent{
			Topic:     m[1],
			Role:      "consumer",
			EventType: "bullmq",
			Symbol:    className,
			FilePath:  filePath,
		})
	}
	for _, m := range reBullMQProcess.FindAllStringSubmatch(source, -1) {
		out = append(out, SignalEvent{
			Topic:     m[1],
			Role:      "consumer",
			EventType: "bullmq",
			Symbol:    className,
			FilePath:  filePath,
		})
	}
	// Producers — filter out false-positive non-queue .add calls by requiring
	// the receiver to reference "queue" or the source to import from bullmq.
	isBullMQCtx := strings.Contains(source, "bullmq") ||
		strings.Contains(source, "@nestjs/bull") ||
		strings.Contains(source, "InjectQueue") ||
		strings.Contains(source, "@Processor")
	if isBullMQCtx {
		for _, m := range reBullMQAdd.FindAllStringSubmatch(source, -1) {
			receiver := strings.ToLower(m[1])
			if !strings.Contains(receiver, "queue") &&
				!strings.Contains(receiver, "reports") &&
				!strings.Contains(receiver, "jobs") &&
				!strings.Contains(receiver, "tasks") {
				// conservative — heuristically skip non-queue .add calls
				continue
			}
			out = append(out, SignalEvent{
				Topic:     m[2],
				Role:      "producer",
				EventType: "bullmq",
				Symbol:    className,
				FilePath:  filePath,
			})
		}
	}
	return out
}

// ExtractRedisPubSubSignals finds redis.publish / redis.subscribe calls.
func ExtractRedisPubSubSignals(source, filePath string) []SignalEvent {
	className := firstClassName(source)
	var out []SignalEvent
	for _, m := range reRedisPublish.FindAllStringSubmatch(source, -1) {
		out = append(out, SignalEvent{
			Topic:     m[1],
			Role:      "producer",
			EventType: "redis",
			Symbol:    className,
			FilePath:  filePath,
		})
	}
	for _, m := range reRedisSubscribe.FindAllStringSubmatch(source, -1) {
		out = append(out, SignalEvent{
			Topic:     m[1],
			Role:      "consumer",
			EventType: "redis",
			Symbol:    className,
			FilePath:  filePath,
		})
	}
	return out
}

// ExtractHttpClientCalls finds axios / httpService / fetch calls with a
// literal URL. Dynamic URLs (template literals, property access) are
// deliberately skipped — they pollute the graph with unresolvable
// placeholders.
func ExtractHttpClientCalls(source, filePath string) []HttpClientCall {
	className := firstClassName(source)
	var out []HttpClientCall

	for _, m := range reAxiosCall.FindAllStringSubmatch(source, -1) {
		out = append(out, HttpClientCall{Method: strings.ToUpper(m[1]), URL: m[2], Symbol: className, FilePath: filePath})
	}
	for _, m := range reHttpServiceCall.FindAllStringSubmatch(source, -1) {
		out = append(out, HttpClientCall{Method: strings.ToUpper(m[1]), URL: m[2], Symbol: className, FilePath: filePath})
	}
	// fetch defaults to GET when called with a single literal URL arg.
	for _, m := range reFetchCall.FindAllStringSubmatch(source, -1) {
		out = append(out, HttpClientCall{Method: "GET", URL: m[1], Symbol: className, FilePath: filePath})
	}
	return out
}

// ExtractWebSocketSignals finds @SubscribeMessage('event') handlers in a
// @WebSocketGateway class. Each row becomes an event_contracts consumer
// with event_type='websocket'.
func ExtractWebSocketSignals(source, filePath string) []SignalEvent {
	className := firstClassName(source)
	var out []SignalEvent
	for _, m := range reSubscribeMessage.FindAllStringSubmatch(source, -1) {
		out = append(out, SignalEvent{
			Topic:     m[1],
			Role:      "consumer",
			EventType: "websocket",
			Symbol:    className,
			FilePath:  filePath,
		})
	}
	return out
}

// ExtractGrpcMethods finds @GrpcMethod and @GrpcStreamMethod handlers.
func ExtractGrpcMethods(source, filePath string) []GrpcMethod {
	className := firstClassName(source)
	var out []GrpcMethod
	for _, m := range reGrpcMethod.FindAllStringSubmatch(source, -1) {
		out = append(out, GrpcMethod{Service: m[1], Method: m[2], Streaming: false, Symbol: className, FilePath: filePath})
	}
	for _, m := range reGrpcStreamMethod.FindAllStringSubmatch(source, -1) {
		out = append(out, GrpcMethod{Service: m[1], Method: m[2], Streaming: true, Symbol: className, FilePath: filePath})
	}
	return out
}

// ExtractGraphQLOps finds @Query / @Mutation / @Subscription resolver
// methods. Crucially it requires the file to import from @nestjs/graphql
// (or a related GraphQL module) — without this guard the extractor
// false-positives on the very common @Query() HTTP-query-parameter
// decorator from @nestjs/common, which is NOT GraphQL. Name is best-effort
// (empty if the decorator has no explicit { name: 'x' } option).
func ExtractGraphQLOps(source, filePath string) []GraphQLOp {
	if !isGraphQLContext(source) {
		return nil
	}
	className := firstClassName(source)
	var out []GraphQLOp

	addOps := func(kind string, re *regexp.Regexp) {
		for _, idx := range re.FindAllStringIndex(source, -1) {
			// Scan forward from the decorator for an optional `{ name: '...' }`
			tail := source[idx[1]:]
			// Limit search window to 200 chars so we don't read across methods.
			if len(tail) > 200 {
				tail = tail[:200]
			}
			name := ""
			if m := regexp.MustCompile(`name\s*:\s*['"]([^'"]+)['"]`).FindStringSubmatch(tail); m != nil {
				name = m[1]
			}
			out = append(out, GraphQLOp{Kind: kind, Name: name, Symbol: className, FilePath: filePath})
		}
	}
	addOps("query", reGraphqlQuery)
	addOps("mutation", reGraphqlMutation)
	addOps("subscription", reGraphqlSubscription)
	return out
}

// isGraphQLContext returns true when the source file looks like it
// actually uses the @nestjs/graphql / type-graphql modules, not just the
// common @Query HTTP decorator.
func isGraphQLContext(source string) bool {
	return strings.Contains(source, "@nestjs/graphql") ||
		strings.Contains(source, "type-graphql") ||
		strings.Contains(source, "@Resolver(")
}
