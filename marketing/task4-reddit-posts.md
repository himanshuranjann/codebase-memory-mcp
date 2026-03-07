# Task 4: Reddit Posts

## Day 1: r/ClaudeAI

**Title:** I built an MCP server that gives Claude Code a knowledge graph of your codebase — 120x fewer tokens for code exploration

**Body:**

I've been using Claude Code daily and kept running into the same issue: every time I ask a structural question about my codebase ("what calls this function?", "find dead code", "show me the API routes"), Claude greps through files one at a time. It works, but it burns through tokens and takes forever.

So I built an MCP server that indexes your codebase into a persistent knowledge graph. Tree-sitter parses 63 languages into a SQLite-backed graph — functions, classes, call chains, HTTP routes, cross-service links. When Claude Code asks a structural question, it queries the graph instead of grepping through files.

**The difference**: 5 structural questions consumed ~412,000 tokens via file-by-file exploration vs ~3,400 tokens via graph queries. That's 120x fewer tokens — which means lower cost, faster responses, and more accurate answers (less "lost in the middle" noise).

It's a single Go binary. No Docker, no external databases, no API keys. `codebase-memory-mcp install` auto-configures Claude Code. Say "Index this project" and you're done. It auto-syncs when you edit files so the graph stays fresh.

**Key features:**
- 63 languages (Python, Go, JS, TS, Rust, Java, C++, and 28 more)
- Call graph tracing: "what calls ProcessOrder?" returns the full chain in <1ms
- Dead code detection (with smart entry point filtering)
- Cross-service HTTP linking (finds REST calls between services)
- Cypher-like query language for ad-hoc exploration
- Architecture overview with Louvain community detection
- Architecture Decision Records that persist across sessions
- 12 MCP tools (also works with Codex CLI, Cursor, Windsurf)
- CLI mode for direct terminal use without an MCP client

Benchmarked across 35 real open-source repos (78 to 49K nodes) including the Linux kernel. Open source, MIT licensed.

GitHub: https://github.com/DeusData/codebase-memory-mcp

Happy to answer questions about the architecture, benchmarks, or how it compares to similar tools.

---

## Day 1: r/LocalLLaMA

**Title:** MCP server that indexes codebases into a knowledge graph — 120x token reduction benchmarked across 63 repos

**Body:**

Built an MCP server for AI coding assistants that replaces file-by-file code exploration with graph queries. The key metric: 120x fewer tokens for the same structural questions, benchmarked across 35 real-world repos.

**The problem**: When AI coding tools (Claude Code, Cursor, Codex, or local setups) need to understand code structure, they grep through files. "What calls this function?" becomes: list files → grep for pattern → read matching files → grep for related patterns → read those files. Each step dumps file contents into the context.

**The solution**: Parse the codebase with tree-sitter into a persistent knowledge graph (SQLite). Functions, classes, call relationships, HTTP routes, cross-service links — all stored as nodes and edges. When the AI asks "what calls ProcessOrder?", it gets a precise call chain in one graph query (~500 tokens) instead of reading dozens of files (~80K tokens).

**Why this matters for local LLM setups**: If you're running models with smaller context windows (8K-32K), every token counts even more. The graph returns exactly the structural information needed. Works as an MCP server with any MCP-compatible client, or via CLI mode for direct terminal use.

**Specs:**
- Single Go binary, zero infrastructure (no Docker, no databases, no API keys)
- 63 languages, sub-ms queries
- Auto-syncs on file changes (background polling)
- Cypher-like query language for complex graph patterns
- Benchmarked: 78 to 49K node repos, Linux kernel stress test (20K nodes, 67K edges, zero timeouts)

MIT licensed: https://github.com/DeusData/codebase-memory-mcp

---

## Day 2: r/programming

**Title:** Benchmarked: AI code exploration via knowledge graph vs file-by-file grep — 120x token reduction across 63 real repos

**Body:**

I've been measuring the token cost of how AI coding assistants explore codebases — specifically, structural questions like "what calls this function?", "find dead code", and "show API routes."

**Method**: Same 5 questions, same codebases. One approach: Claude Code's Explorer agent (grep/Glob/Read tools, file-by-file). Other approach: pre-built knowledge graph (tree-sitter AST → SQLite graph → MCP tool queries).

**Results**:

| Question | Graph (tokens) | File-by-file (tokens) | Ratio |
|----------|---------------|----------------------|-------|
| Find function by pattern | ~200 | ~45,000 | 225x |
| Trace call chain (depth 3) | ~800 | ~120,000 | 150x |
| Dead code detection | ~500 | ~85,000 | 170x |
| List routes | ~400 | ~62,000 | 155x |
| Architecture overview | ~1,500 | ~100,000 | 67x |
| **Total** | **~3,400** | **~412,000** | **121x** |

The file-by-file approach reads entire files to find one piece of information, then needs more files for context. The graph returns exactly the structural result — no file contents, no noise.

Beyond tokens: graph queries return in <1ms vs seconds of file I/O. And in long contexts, LLMs lose accuracy on relevant details ("lost in the middle" problem) — so less noise means better answers.

Benchmarked across 63 repos (78 to 49K nodes) including Linux kernel drivers. The tool is a single Go binary using tree-sitter + SQLite. 63 languages, MIT licensed.

Full benchmark with per-language scoring: https://github.com/DeusData/codebase-memory-mcp/blob/main/BENCHMARK_REPORT.md

Code: https://github.com/DeusData/codebase-memory-mcp

---

## Day 2: r/neovim

**Title:** Open-source tool using tree-sitter grammars to build a persistent code knowledge graph — 63 languages, Go + SQLite

**Body:**

If you're into tree-sitter (which, being on r/neovim, you probably are), this might interest you.

I built a tool that uses tree-sitter to parse source code into a persistent knowledge graph stored in SQLite. It extracts functions, classes, modules, call relationships, cross-service HTTP routes — then exposes the graph through both MCP tools (for AI assistants) and a CLI.

The interesting part from a tree-sitter perspective: it supports 35 language grammars and handles the edge cases that make multi-language parsing hard:
- Languages where functions are assigned to variables (Lua, R, JS) need custom name resolution
- Languages with macros-as-definitions (Elixir `def`/`defp`, Rust proc-macros) need special handling
- Languages where the grammar lives at a different org than expected need `replace` directives in go.mod
- External scanner grammars (Erlang, R) have linker issues on macOS arm64

The call resolution is import-aware and type-inferred — not just string matching. A call to `service.ProcessOrder()` resolves to the actual function through import analysis.

Built in Go with CGO bindings to tree-sitter. Single binary, no dependencies.

You can use it as:
- An MCP server for Claude Code / Cursor / Codex / Windsurf
- A CLI tool: `codebase-memory-mcp cli search_graph '{"name_pattern": ".*Handler.*"}'`
- Cypher-like queries: `MATCH (f:Function)-[:CALLS]->(g) WHERE f.name = 'main' RETURN g.name`

MIT licensed: https://github.com/DeusData/codebase-memory-mcp

Curious if anyone has opinions on the tree-sitter grammar coverage — the benchmark report has per-language accuracy data.

---

## Day 3: r/SideProject

**Title:** I built an MCP server that gives AI coding assistants a knowledge graph instead of file-by-file grep — 120x fewer tokens

**Body:**

**What**: An MCP server that indexes your codebase into a persistent knowledge graph. When an AI coding assistant (Claude Code, Cursor, Codex) needs to understand code structure, it queries the graph instead of grepping through files.

**Why**: AI assistants explore code by reading files one at a time. "What calls this function?" becomes 10+ file reads and pattern searches. With a pre-built graph, it's one query — 120x fewer tokens, sub-millisecond response, more accurate results.

**How**: Tree-sitter parses 63 languages into a SQLite-backed graph. Functions, classes, call chains, HTTP routes, cross-service links. Queryable via structured search, Cypher-like syntax, or natural language through the AI assistant.

**Traction**: 80+ stars in the first week, first LinkedIn post got 25K views / 200 reactions.

**Stack**: Go, tree-sitter (CGO), SQLite, MCP protocol. Single binary, zero infrastructure.

**What worked for launch**: LinkedIn (25K views on first post), being honest about limitations in the benchmark report (not every language is perfect — the report shows per-language accuracy with a tier system).

MIT licensed: https://github.com/DeusData/codebase-memory-mcp

---

## Day 3 (optional): r/cursor

**Title:** MCP server that gives Cursor a knowledge graph of your codebase — 120x fewer tokens for code exploration

**Body:**

Built an MCP server that indexes your codebase into a persistent knowledge graph, specifically designed to work with MCP-compatible editors like Cursor.

Instead of the AI reading through files one by one to understand code structure, it queries a pre-built graph. Tree-sitter parses 63 languages into a SQLite-backed knowledge graph — functions, classes, call chains, HTTP routes, cross-service links.

**Setup with Cursor:**
1. Download the binary from releases
2. Run `codebase-memory-mcp install` (auto-detects Cursor)
3. Say "Index this project"
4. Ask structural questions: "what calls ProcessOrder?", "find dead code", "show me the API routes"

12 MCP tools: `search_graph`, `trace_call_path`, `detect_changes`, `query_graph`, `get_architecture`, and more.

Key features: auto-sync (graph updates when you edit files), dead code detection, Cypher-like queries, cross-service HTTP linking, architecture overview with community detection.

Benchmarked: 120x fewer tokens for structural queries vs file-by-file exploration, tested across 63 real repos including Linux kernel.

MIT licensed: https://github.com/DeusData/codebase-memory-mcp
