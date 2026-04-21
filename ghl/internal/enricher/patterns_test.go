package enricher

import (
	"testing"
)

// ---------------------------------------------------------------------------
// T1B — @Cron / @Interval / @Timeout scheduled jobs
// ---------------------------------------------------------------------------

func TestExtractScheduledJobs_Cron(t *testing.T) {
	source := `
import { Cron, CronExpression } from '@nestjs/schedule';

@Injectable()
export class BillingCronService {
  @Cron('0 0 * * *')
  async dailyBillingRun() {}

  @Cron(CronExpression.EVERY_5_MINUTES)
  async refreshQuotes() {}

  @Cron('0 */1 * * *', { name: 'hourly-sync' })
  async hourlySync() {}
}
`
	jobs := ExtractScheduledJobs(source, "src/billing.cron.ts")
	if len(jobs) != 3 {
		t.Fatalf("want 3 @Cron jobs, got %d: %+v", len(jobs), jobs)
	}

	// Literal cron expression
	if jobs[0].Kind != "cron" || jobs[0].Schedule != "0 0 * * *" {
		t.Errorf("job[0]: got kind=%q schedule=%q, want kind=cron schedule='0 0 * * *'", jobs[0].Kind, jobs[0].Schedule)
	}
	// CronExpression enum reference — stored as-is (consumer can resolve)
	if jobs[1].Schedule != "CronExpression.EVERY_5_MINUTES" {
		t.Errorf("job[1] schedule: got %q, want CronExpression.EVERY_5_MINUTES", jobs[1].Schedule)
	}
	// File path propagated
	if jobs[0].FilePath != "src/billing.cron.ts" {
		t.Errorf("FilePath: got %q", jobs[0].FilePath)
	}
}

func TestExtractScheduledJobs_IntervalAndTimeout(t *testing.T) {
	source := `
@Injectable()
export class HealthPollerService {
  @Interval(30000)
  pingUpstream() {}

  @Timeout(5000)
  initialPing() {}
}
`
	jobs := ExtractScheduledJobs(source, "health.ts")
	if len(jobs) != 2 {
		t.Fatalf("want 2 jobs, got %d: %+v", len(jobs), jobs)
	}

	found := map[string]ScheduledJob{}
	for _, j := range jobs {
		found[j.Kind] = j
	}
	if found["interval"].Schedule != "30000" {
		t.Errorf("interval schedule: got %q, want 30000", found["interval"].Schedule)
	}
	if found["timeout"].Schedule != "5000" {
		t.Errorf("timeout schedule: got %q, want 5000", found["timeout"].Schedule)
	}
}

func TestExtractScheduledJobs_NoMatches(t *testing.T) {
	source := `export class PlainService { doWork() {} }`
	if jobs := ExtractScheduledJobs(source, "plain.ts"); len(jobs) != 0 {
		t.Errorf("want 0 jobs, got %d", len(jobs))
	}
}

// ---------------------------------------------------------------------------
// T1C — cloudTasks.enqueue + GHL CloudTasks wrapper patterns
// ---------------------------------------------------------------------------

func TestExtractCloudTaskEnqueues(t *testing.T) {
	source := `
@Injectable()
export class WorkflowProcessor {
  constructor(private readonly cloudTasks: CloudTasksService) {}

  async triggerRecompute() {
    await this.cloudTasks.enqueue('workflow.recompute', { workflowId: '123' });
    await cloudTasks.enqueue("billing.retry", payload);
    await this.tasks.enqueue('generic.task', {});
  }
}
`
	calls := ExtractCloudTaskEnqueues(source, "workflow.service.ts")
	if len(calls) != 3 {
		t.Fatalf("want 3 cloudTasks.enqueue calls, got %d: %+v", len(calls), calls)
	}
	topics := map[string]bool{}
	for _, c := range calls {
		topics[c.Topic] = true
	}
	for _, want := range []string{"workflow.recompute", "billing.retry", "generic.task"} {
		if !topics[want] {
			t.Errorf("missing cloud-task topic: %q", want)
		}
	}
	for _, c := range calls {
		if c.EventType != "cloudtask" {
			t.Errorf("event_type: got %q, want cloudtask", c.EventType)
		}
		if c.Role != "producer" {
			t.Errorf("role: got %q, want producer", c.Role)
		}
	}
}

// ---------------------------------------------------------------------------
// T2D — GCP Pub/Sub SDK publisher
// ---------------------------------------------------------------------------

func TestExtractGcpPubSubPublishers(t *testing.T) {
	source := `
import { PubSub } from '@google-cloud/pubsub';
const pubsub = new PubSub();

export class ContactEventsPublisher {
  async publishCreated(contact: Contact) {
    await pubsub.topic('contact.created').publish(Buffer.from(JSON.stringify(contact)));
    await pubsub.topic(TOPIC_NAME).publishMessage({ data });
  }
}
`
	pubs := ExtractGcpPubSubPublishers(source, "publisher.ts")
	if len(pubs) != 2 {
		t.Fatalf("want 2 gcp pubsub publish calls, got %d: %+v", len(pubs), pubs)
	}
	// Literal string topic
	if pubs[0].Topic != "contact.created" {
		t.Errorf("pubs[0]: got topic=%q, want contact.created", pubs[0].Topic)
	}
	// Variable topic — preserved as the symbol so downstream can resolve
	if pubs[1].Topic != "TOPIC_NAME" {
		t.Errorf("pubs[1]: got topic=%q, want TOPIC_NAME", pubs[1].Topic)
	}
	for _, p := range pubs {
		if p.Role != "producer" || p.EventType != "pubsub" {
			t.Errorf("pub metadata: got role=%q type=%q, want producer/pubsub", p.Role, p.EventType)
		}
	}
}

// ---------------------------------------------------------------------------
// T2E — BullMQ queue.add + @Processor
// ---------------------------------------------------------------------------

func TestExtractBullMQSignals(t *testing.T) {
	source := `
import { Process, Processor } from '@nestjs/bullmq';
import { Queue } from 'bullmq';

@Processor('reports')
export class ReportsProcessor {
  @Process('generate')
  async generate(job) {}
}

@Injectable()
export class ReportsEnqueuer {
  constructor(@InjectQueue('reports') private reports: Queue) {}
  async queueReport(id: string) {
    await this.reports.add('generate', { id });
    await otherQueue.add('noop-job', {}, { delay: 1000 });
  }
}
`
	signals := ExtractBullMQSignals(source, "reports.ts")
	// Expect 2 consumers (@Processor('reports') + @Process('generate'))
	// and 2 producers (queue.add)
	consumers := 0
	producers := 0
	for _, s := range signals {
		if s.EventType != "bullmq" {
			t.Errorf("event_type: got %q, want bullmq", s.EventType)
		}
		switch s.Role {
		case "consumer":
			consumers++
		case "producer":
			producers++
		}
	}
	if consumers < 2 {
		t.Errorf("want >=2 bullmq consumer signals (@Processor + @Process), got %d: %+v", consumers, signals)
	}
	if producers < 2 {
		t.Errorf("want >=2 bullmq producer signals (queue.add x2), got %d: %+v", producers, signals)
	}
}

// ---------------------------------------------------------------------------
// T2F — Redis pub/sub
// ---------------------------------------------------------------------------

func TestExtractRedisPubSubSignals(t *testing.T) {
	source := `
@Injectable()
export class RealtimeBridge {
  constructor(private readonly redis: RedisClient) {}

  async fanOut(payload) {
    await this.redis.publish('location.updated', JSON.stringify(payload));
    await redisClient.publish("contact.updated", payload);
  }

  async listen() {
    await this.redis.subscribe('location.updated', (msg) => {});
    await redisClient.subscribe("contact.updated", handle);
  }
}
`
	signals := ExtractRedisPubSubSignals(source, "bridge.ts")
	// 2 publish (producer) + 2 subscribe (consumer) = 4
	if len(signals) != 4 {
		t.Fatalf("want 4 redis pubsub signals, got %d: %+v", len(signals), signals)
	}
	producers := 0
	consumers := 0
	for _, s := range signals {
		if s.EventType != "redis" {
			t.Errorf("event_type: got %q, want redis", s.EventType)
		}
		if s.Role == "producer" {
			producers++
		} else if s.Role == "consumer" {
			consumers++
		}
	}
	if producers != 2 || consumers != 2 {
		t.Errorf("want 2p/2c, got %dp/%dc", producers, consumers)
	}
}

// ---------------------------------------------------------------------------
// T2G — HTTP client call sites (axios / httpService / fetch)
// ---------------------------------------------------------------------------

func TestExtractHttpClientCalls(t *testing.T) {
	source := `
import axios from 'axios';

@Injectable()
export class ExternalApiClient {
  constructor(private readonly httpService: HttpService) {}

  async fetchPricing() {
    const r = await axios.get('https://api.stripe.com/v1/prices');
    const q = await this.httpService.post('https://api.twilio.com/v1/messages', payload).toPromise();
    const f = await fetch('https://api.example.com/data');
  }
}
`
	calls := ExtractHttpClientCalls(source, "client.ts")
	if len(calls) != 3 {
		t.Fatalf("want 3 http client calls, got %d: %+v", len(calls), calls)
	}
	methods := map[string]string{}
	for _, c := range calls {
		methods[c.URL] = c.Method
	}
	if methods["https://api.stripe.com/v1/prices"] != "GET" {
		t.Errorf("stripe call method: got %q, want GET", methods["https://api.stripe.com/v1/prices"])
	}
	if methods["https://api.twilio.com/v1/messages"] != "POST" {
		t.Errorf("twilio call method: got %q, want POST", methods["https://api.twilio.com/v1/messages"])
	}
	if methods["https://api.example.com/data"] != "GET" {
		t.Errorf("fetch call method: got %q, want GET", methods["https://api.example.com/data"])
	}
}

func TestExtractHttpClientCalls_SkipsNonLiteralURLs(t *testing.T) {
	// Both calls use dynamic expressions (property access / template
	// literal) — extractor must only capture literal-string URLs to avoid
	// polluting the graph with unresolvable placeholders.
	source := "const r = await axios.get(this.config.apiUrl);\n" +
		"const q = await this.httpService.get(`" + "${this.base}/users" + "`);"
	if calls := ExtractHttpClientCalls(source, "dynamic.ts"); len(calls) != 0 {
		t.Errorf("want 0 calls for dynamic URLs, got %d: %+v", len(calls), calls)
	}
}

// ---------------------------------------------------------------------------
// T3H — WebSocket gateways
// ---------------------------------------------------------------------------

func TestExtractWebSocketSignals(t *testing.T) {
	source := `
import { WebSocketGateway, SubscribeMessage } from '@nestjs/websockets';

@WebSocketGateway({ namespace: '/notifications' })
export class NotificationsGateway {
  @SubscribeMessage('notification.ack')
  handleAck(client, payload) {}

  @SubscribeMessage('notification.subscribe')
  handleSubscribe(client, payload) {}
}
`
	signals := ExtractWebSocketSignals(source, "gateway.ts")
	if len(signals) != 2 {
		t.Fatalf("want 2 @SubscribeMessage signals, got %d: %+v", len(signals), signals)
	}
	topics := map[string]bool{}
	for _, s := range signals {
		topics[s.Topic] = true
		if s.EventType != "websocket" || s.Role != "consumer" {
			t.Errorf("metadata: got role=%q type=%q, want consumer/websocket", s.Role, s.EventType)
		}
	}
	for _, want := range []string{"notification.ack", "notification.subscribe"} {
		if !topics[want] {
			t.Errorf("missing websocket topic: %q", want)
		}
	}
}

// ---------------------------------------------------------------------------
// T3I — gRPC methods
// ---------------------------------------------------------------------------

func TestExtractGrpcMethods(t *testing.T) {
	source := `
import { GrpcMethod, GrpcStreamMethod } from '@nestjs/microservices';

@Controller()
export class ContactsGrpcController {
  @GrpcMethod('ContactsService', 'Get')
  findContact(req) {}

  @GrpcMethod('ContactsService', 'List')
  listContacts(req) {}

  @GrpcStreamMethod('ContactsService', 'Watch')
  watch(req$) {}
}
`
	methods := ExtractGrpcMethods(source, "contacts.grpc.ts")
	if len(methods) != 3 {
		t.Fatalf("want 3 grpc methods, got %d: %+v", len(methods), methods)
	}
	hasStreaming := false
	for _, m := range methods {
		if m.Service != "ContactsService" {
			t.Errorf("service: got %q, want ContactsService", m.Service)
		}
		if m.Streaming {
			hasStreaming = true
		}
	}
	if !hasStreaming {
		t.Error("expected at least one streaming method")
	}
}

// ---------------------------------------------------------------------------
// T3J — GraphQL resolvers
// ---------------------------------------------------------------------------

func TestExtractGraphQLOps(t *testing.T) {
	source := `
import { Query, Mutation, Subscription, Resolver, Args } from '@nestjs/graphql';

@Resolver(() => Contact)
export class ContactResolver {
  @Query(() => [Contact], { name: 'contacts' })
  list() {}

  @Query()
  contact(@Args('id') id: string) {}

  @Mutation(() => Contact)
  createContact(@Args('input') input: CreateContactInput) {}

  @Subscription(() => Contact)
  contactChanged() {}
}
`
	ops := ExtractGraphQLOps(source, "contact.resolver.ts")
	if len(ops) != 4 {
		t.Fatalf("want 4 GraphQL ops (2 query + 1 mutation + 1 subscription), got %d: %+v", len(ops), ops)
	}
	kinds := map[string]int{}
	for _, op := range ops {
		kinds[op.Kind]++
	}
	if kinds["query"] != 2 || kinds["mutation"] != 1 || kinds["subscription"] != 1 {
		t.Errorf("kind counts: got %v, want map[query:2 mutation:1 subscription:1]", kinds)
	}
}

// T3J — must NOT mistake the NestJS HTTP @Query() decorator (from
// @nestjs/common) for a GraphQL @Query. Without this guard the extractor
// will flood every HTTP controller with bogus GraphQL ops.
func TestExtractGraphQLOps_RejectsHttpQueryDecorator(t *testing.T) {
	source := `
import { Controller, Get, Query } from '@nestjs/common';

@Controller('contacts')
export class ContactsController {
  @Get('search')
  search(@Query('q') q: string) {}
}
`
	ops := ExtractGraphQLOps(source, "contacts.controller.ts")
	if len(ops) != 0 {
		t.Errorf("HTTP @Query must not count as GraphQL op; got %d: %+v", len(ops), ops)
	}
}
