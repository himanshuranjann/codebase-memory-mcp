---
title: "How I Reduced AI Code Exploration from 412K Tokens to 3.4K with a Knowledge Graph"
published: false
tags: mcp, ai, golang, devtools
---

# How I Reduced AI Code Exploration from 412K Tokens to 3.4K with a Knowledge Graph

AI coding assistants are powerful, but they have a dirty secret: they explore your codebase by reading files one at a time. Every "what calls this function?" question triggers a cascade of grep → read file → grep again → read more files. It works, but it's brutally expensive.

I measured it. Five structural questions about a real codebase consumed **~412,000 tokens** via file-by-file exploration. The same five questions via a knowledge graph: **~3,400 tokens**. A 120x reduction.

I built [codebase-memory-mcp](https://github.com/DeusData/codebase-memory-mcp) to fix this.

## But wait — context windows are 200K-1M tokens now. Does this matter?

This is the first objection I hear, so let me address it upfront.

Token reduction isn't about fitting in the window — it's about **cost**, **latency**, and **accuracy**.

**Cost**: At $3/M input tokens (Sonnet) or $15/M (Opus), 412K tokens per exploration session is $1.24-$6.18. Multiply by 20 sessions/day across a team and it adds up fast. Graph queries cost $0.01-$0.05 for the same answers.

**Latency**: File-by-file exploration takes seconds per question (read files, parse, grep, read more). Graph queries return in <1ms.

**Accuracy**: This is the big one. Studies show LLMs lose track of relevant details in large contexts — the "lost in the middle" problem. When you dump 50 files into context to answer one structural question, the actual answer gets buried in noise. Session compaction in tools like Claude Code discards earlier context — so the tokens you spent reading files are the first to disappear.

A graph query returns exactly the structural information needed in ~500 tokens. No noise, no irrelevant file contents, no lost context.

## The problem in numbers

Here's what happens when an AI assistant answers "what calls `ProcessOrder`?" via file-by-file exploration:

1. **List project files** → tool call, directory listing in response (~2K tokens)
2. **Grep for "ProcessOrder"** → tool call, matching lines from multiple files (~5K tokens)
3. **Read first matching file** → full file contents (~8K tokens)
4. **Read second matching file** → full file contents (~6K tokens)
5. **Grep for import statements** → more context (~3K tokens)
6. **Read the importing files** → full file contents (~15K tokens)
7. **Synthesize answer** → the AI processes all of this to extract the call chain

Total: ~40K tokens for one question. And the AI had to reason over thousands of lines of irrelevant code to find 3-4 actual callers.

Now the same question via graph query:

1. **`trace_call_path(function_name="ProcessOrder", direction="inbound", depth=3)`** → returns the exact call chain with function names, files, line numbers, and edge types (~800 tokens)

Done. One tool call, precise result, no noise.

## How it works

[codebase-memory-mcp](https://github.com/DeusData/codebase-memory-mcp) is a structural analysis backend that builds a persistent code knowledge graph:

1. **Tree-sitter** parses your source code into an AST (63 languages supported)
2. **Multi-pass pipeline** extracts functions, classes, modules, call relationships, HTTP routes, and cross-service links
3. **SQLite** stores the graph with nodes (functions, classes, routes) and edges (CALLS, HTTP_CALLS, IMPORTS, INHERITS, etc.)
4. **12 MCP tools** expose the graph to AI assistants (Claude Code, Codex CLI, Cursor, Windsurf)
5. **Auto-sync** keeps the graph fresh via background file-change polling

It ships as a **single Go binary**. No Docker, no external databases, no API keys. Install and say "Index this project" — done.

The key architectural decision: **no embedded LLM**. Other code graph tools embed an LLM to translate natural language into graph queries. This means extra API keys, extra cost per query, and another model to configure. With MCP, the AI assistant you're already talking to IS the query translator. No duplication needed.

## The benchmark

We benchmarked across **35 real open-source repositories** (78 to 49,000 nodes each), running the same 5 structural questions via both approaches:

| Question Type | Graph (tokens) | Explorer (tokens) | Ratio |
|--------------|---------------|-------------------|-------|
| Find function by pattern | ~200 | ~45,000 | 225x |
| Trace call chain (depth 3) | ~800 | ~120,000 | 150x |
| Dead code detection | ~500 | ~85,000 | 170x |
| List all routes | ~400 | ~62,000 | 155x |
| Architecture overview | ~1,500 | ~100,000 | 67x |
| **Total** | **~3,400** | **~412,000** | **121x** |

**Stress test**: Linux kernel `drivers/net/ethernet/intel/` — 20,000 nodes, 67,000 edges, 129K-character deep traces, zero timeouts.

**Performance**: Sub-millisecond graph queries, ~6 second cold index for most repos, incremental reindex 4x faster.

Full per-language benchmark with accuracy scoring: [BENCHMARK_REPORT.md](https://github.com/DeusData/codebase-memory-mcp/blob/main/BENCHMARK_REPORT.md)

## What you can do with it

Beyond basic function lookup, here are some things the graph enables:

**Dead code detection** — find functions with zero callers, excluding entry points (route handlers, `main()`, framework-decorated functions):
```
search_graph(label="Function", relationship="CALLS", direction="inbound",
             max_degree=0, exclude_entry_points=true)
```

**Cross-service HTTP linking** — discover which functions in Service A call endpoints in Service B, with confidence scoring:
```
query_graph(query="MATCH (a)-[r:HTTP_CALLS]->(b)
                   RETURN a.name, b.name, r.url_path, r.confidence
                   ORDER BY r.confidence DESC LIMIT 20")
```

**Architecture overview** — languages, packages, entry points, routes, hotspots, and hidden functional modules via Louvain community detection:
```
get_architecture(aspects=["all"])
```

**Git diff impact analysis** — map uncommitted changes to affected symbols + blast radius with risk classification:
```
detect_changes(scope="staged", depth=3)
```

**Cypher-like queries** for anything the structured tools don't cover:
```
query_graph(query="MATCH (f:Function)-[:CALLS*1..3]->(g:Function)
                   WHERE f.name = 'main'
                   RETURN g.name, g.file_path LIMIT 20")
```

## How it compares to GitNexus

[GitNexus](https://github.com/nicepkg/gitnexus) (7K+ stars) is the biggest project in this space. It went viral on Twitter in February and hit GitHub trending. Fair to compare directly.

**GitNexus strengths:**
- Beautiful visual web UI — browser-based interactive graph exploration
- Built-in Graph RAG Agent — chat directly with the graph
- Easy install (`npx gitnexus`)

**codebase-memory-mcp strengths:**
- **63 languages vs 8-11** — 3-4x language coverage
- **Single Go binary vs Node.js** — zero runtime dependencies
- **No embedded LLM** — no API keys needed, no per-query cost overhead
- **Published benchmarks** — 63 repos, per-language accuracy, Linux kernel stress test. GitNexus has none
- **Auto-sync** — graph updates automatically when you edit files
- **12 MCP tools vs 7** — more tool coverage
- **Cross-service HTTP linking** with confidence scoring
- **Cypher query language** for ad-hoc exploration

The positioning: GitNexus is a **visual dashboard** — great for exploring a codebase interactively in a browser. codebase-memory-mcp is **production tooling** — the most comprehensive, zero-dependency, benchmarked backend for your AI coding assistant.

Different tools for different workflows. Both can coexist.

## Try it

```bash
# Download from releases
tar xzf codebase-memory-mcp-*.tar.gz
mv codebase-memory-mcp ~/.local/bin/

# Auto-configure your editor
codebase-memory-mcp install

# Restart your editor, then:
# "Index this project"
# "What calls ProcessOrder?"
# "Find dead code"
# "Show me the architecture"
```

Works with Claude Code, Codex CLI, Cursor, and Windsurf. Also has a CLI mode for direct terminal use without any MCP client.

Open source, MIT licensed: **[github.com/DeusData/codebase-memory-mcp](https://github.com/DeusData/codebase-memory-mcp)**

---

*Discuss on [Hacker News](#) | [GitHub Discussions](https://github.com/DeusData/codebase-memory-mcp/discussions)*
