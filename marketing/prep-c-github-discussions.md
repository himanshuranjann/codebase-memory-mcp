# Prep C: GitHub Discussions Seed Posts

Create these in the GitHub Discussions tab. Category: "Show and Tell" or "General".

---

## Discussion 1: "Using codebase-memory-mcp to find dead code in a Django project (49K nodes)"

**Body:**

Indexed a full Django project (fastapi-clean architecture, 335 nodes / 859 edges) and ran dead code detection:

```
search_graph(label="Function", relationship="CALLS", direction="inbound", max_degree=0, exclude_entry_points=true)
```

Found 12 functions with zero callers that aren't entry points (not route handlers, not `main()`). A few were genuinely dead — leftover utility functions from a refactor. Others were test helpers that only get called via pytest fixtures (which the graph doesn't track yet).

The key insight: `exclude_entry_points=true` filters out route handlers and framework-decorated functions automatically, so you don't get flooded with false positives. Without that flag, every route handler shows up as "dead code" because nothing in your codebase calls it directly — the framework does.

For larger codebases, you can scope it: `search_graph(..., project="my-project")` to avoid cross-project contamination if you have multiple repos indexed.

Anyone else finding interesting results with dead code detection? Curious what the false positive rate looks like in different frameworks.

---

## Discussion 2: "Tracing cross-service HTTP calls across a Go microservices codebase"

**Body:**

One of the features I find most useful is cross-service HTTP linking. If you have multiple services that call each other via REST, the graph discovers those connections automatically.

After indexing two services:

```
query_graph(query="MATCH (a)-[r:HTTP_CALLS]->(b) RETURN a.name, b.name, r.url_path, r.confidence ORDER BY r.confidence DESC LIMIT 20")
```

This shows which functions in Service A call endpoints in Service B, with a confidence score. High confidence (>0.8) means the URL path was found directly in the code. Lower confidence means it was inferred from config files or partial matches.

The trace goes deeper too:

```
trace_call_path(function_name="CreateOrder", direction="both", depth=3)
```

This shows not just the direct HTTP call, but the full chain: which handler receives the request → what business logic it calls → which downstream HTTP calls it makes.

For anyone with a microservices architecture: what's your experience with the HTTP linking accuracy? The confidence scoring helps filter noise, but I'm curious about edge cases.

---

## Discussion 3: "How the 120x token reduction works — benchmark methodology"

**Body:**

The "120x fewer tokens" claim comes from a controlled benchmark. Here's the methodology so you can verify it yourself.

**Setup**: 5 structural questions about a real codebase (function lookup, call tracing, dead code, route listing, architecture overview). Each question asked twice — once via codebase-memory-mcp graph queries, once via a Claude Code Explorer agent that uses grep/Glob/Read tools.

**Measurement**: Total input + output tokens consumed by all tool calls to answer each question.

**Results**:

| Question Type | Graph (tokens) | Explorer (tokens) | Ratio |
|--------------|---------------|-------------------|-------|
| Find function by pattern | ~200 | ~45,000 | 225x |
| Trace call chain (depth 3) | ~800 | ~120,000 | 150x |
| Dead code detection | ~500 | ~85,000 | 170x |
| List all routes | ~400 | ~62,000 | 155x |
| Architecture overview | ~1,500 | ~100,000 | 67x |
| **Total** | **~3,400** | **~412,000** | **121x** |

The Explorer agent has to: read file listings → grep for patterns → read matching files → parse the output → grep again for related files → read those. Each step is a tool call with full file contents in the response.

The graph query returns exactly the structural information in one call. No file contents, no noise, no irrelevant matches.

**Why it matters beyond "fitting in the context window"**: Cost ($3-15/M tokens adds up), latency (seconds of file reading vs <1ms graph query), and accuracy (LLMs lose track of details in large contexts).

Full benchmark data: See `BENCHMARK_REPORT.md` in the repo and the Performance section in the README.

---

## Discussion 4: "Architecture overview: Louvain community detection discovers hidden modules"

**Body:**

The `get_architecture` tool has a `clusters` aspect that runs Louvain community detection on the call graph. It groups functions that call each other frequently into communities — even across package boundaries.

```
get_architecture(aspects=["clusters"])
```

On a medium Go project (~1000 nodes), it found 8 communities. Most aligned with packages as expected, but two were interesting:

1. A cluster that spanned `handlers/`, `services/`, and `repository/` — all related to order processing. These functions called each other heavily but were spread across 3 packages by convention (handler → service → repo).

2. A cluster of utility functions from 4 different packages that all related to date/time formatting. Nobody planned a "datetime" module, but the call graph revealed they form a coherent functional unit.

This is useful for:
- Understanding unfamiliar codebases (the graph tells you the actual module structure, not just the directory structure)
- Refactoring decisions (if a cluster spans too many packages, maybe those functions should be co-located)
- Onboarding (show new developers which functions actually work together)

The clustering uses CALLS, HTTP_CALLS, and ASYNC_CALLS edges, so it captures both direct function calls and cross-service communication patterns.

What patterns are you seeing in your codebases?
