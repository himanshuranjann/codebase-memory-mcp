# Prep E: Response Playbook

Quick-reference for common objections and questions across all channels.

---

## "Context windows are 200K-1M now, why does this matter?"

Token reduction isn't about fitting in the window — it's about cost, latency, and accuracy. At $3/M input tokens (Sonnet) or $15/M (Opus), 412K tokens per exploration session adds up fast across a workday. More importantly, long-context accuracy degrades: studies show LLMs lose track of relevant details in large contexts (the "lost in the middle" problem). Session compaction in Claude Code discards earlier context — so the tokens you spent grepping through files are the first to disappear.

A graph query returns exactly the structural information needed in ~500 tokens. No noise, no irrelevant file contents, no lost context. Faster, cheaper, and more accurate — regardless of context window size.

---

## "How is this different from GitNexus?"

GitNexus and codebase-memory-mcp solve the same core problem but make different trade-offs:

GitNexus: Great visual web UI, built-in Graph RAG chat, but requires Node.js, only supports 8-11 languages, and the embedded LLM adds API key requirements and per-query cost on top of what your AI assistant already costs.

codebase-memory-mcp: No visual UI, but covers 63 languages, ships as a single Go binary (no Node.js), has no LLM overhead (your MCP client IS the intelligence layer), auto-syncs on file changes, and has published benchmarks across 63 real repos including the Linux kernel. 12 MCP tools vs 7, plus Cypher queries, cross-service HTTP linking, and ADR management.

Think "visual dashboard" vs "production tooling."

---

## "63 languages but how good is each one?"

We published the full benchmark: BENCHMARK_REPORT.md. Honest tier system:
- **Tier A (production-ready, 75%+)**: Go, Java, C, Bash, YAML, HTML, CSS, TOML, SQL
- **Tier B (usable with known gaps, 50-74%)**: Python, Kotlin, PHP, Lua, Ruby, TypeScript, C++, Scala, Swift
- **Tier C (structural only, <50%)**: TSX, JavaScript, C#, Rust, Zig, Dart, Groovy
- **Tier D (needs work)**: Perl, Haskell, Erlang, Elixir, R, Objective-C, OCaml

We're upfront about gaps. The issues are tracked and several have "good first issue" labels.

---

## "Why not just use ctags / LSP?"

ctags gives you symbol locations, not call graphs or cross-service links. LSP gives you per-file analysis but not codebase-wide graph traversal or cross-service HTTP linking. This builds a persistent graph with relationships — callers, callees, HTTP links between services, dead code, community clusters. Different tool for a different job. They complement each other.

---

## "Why Go?"

Single binary distribution (no runtime dependencies), CGO for tree-sitter bindings, goroutines for concurrent file parsing. The binary is ~30MB and runs anywhere without installing a runtime. For a developer tool that needs to "just work" on any machine, Go is the right choice.

---

## "SQLite won't scale"

Stress tested on Linux kernel drivers (20K nodes, 67K edges) with zero timeouts. SQLite in WAL mode handles concurrent reads efficiently. The largest benchmark repo (Groovy/Spock) indexed 10K nodes / 24K edges in 11 seconds. For single-user developer tooling, SQLite is the right choice — no server process, no config, ACID guarantees, and the entire database is one file you can copy/backup.

---

## "Why no visual UI?"

Deliberate trade-off. A visual UI adds a web server dependency, increases attack surface, and duplicates functionality that browser-based tools like GitNexus already do well. We focused on being the best backend: most languages, fastest queries, lowest friction install. If you want visual graph exploration, GitNexus is great and can coexist with this.

---

## "Is this just a wrapper around tree-sitter?"

Tree-sitter gives us the AST. We add:
1. Multi-pass pipeline (structure → definitions → calls → HTTP links → communities)
2. Import-aware call resolution across files and packages
3. Cross-service HTTP linking with confidence scoring
4. Cypher-like query engine
5. Auto-sync with adaptive polling
6. Louvain community detection
7. Architecture Decision Records
8. Git diff impact analysis with risk classification

Tree-sitter is the parsing layer. The value is in everything built on top.

---

## "How does auto-sync work?"

Background goroutine polls indexed project directories for file changes (mtime + file size). When changes are detected, it triggers an incremental reindex — only changed files are re-parsed (content-hash based skip for unchanged files). Polling interval adapts: 1 second for small repos, up to 60 seconds for large ones. Non-blocking — never interrupts active queries.

---

## Channel-Specific Response Rules

**HN**: Respond to every comment within 30 min for first 3 hours. Technical depth. Acknowledge limitations honestly. Link to benchmark report.

**Reddit**: Engage every comment for 2-3 hours. Conversational, not salesy. Different tone per subreddit (r/ClaudeAI = user perspective, r/programming = benchmark analysis, r/neovim = tree-sitter nerd).

**LinkedIn**: Reply to every comment within 1 hour. Meaningful comments drive 5x more reach than reactions. Professional tone.

**Discord**: Monitor for 24 hours. Answer setup questions directly. Link to troubleshooting table in README.

**Twitter**: Reply to quote tweets and mentions. Retweet with comment on good discussions.
