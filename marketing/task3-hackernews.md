# Task 3: Hacker News "Show HN" Post

## Title

Show HN: I replaced grep-based code exploration with a knowledge graph – 120x fewer tokens

## URL

https://github.com/DeusData/codebase-memory-mcp

## Timing

Tuesday-Thursday, 12-14 UTC

---

## First Comment (post immediately after submission)

I built this because AI coding assistants (Claude Code, Cursor, Codex) explore codebases by grepping through files one at a time. Five structural questions about a codebase consumed ~412,000 tokens via file-by-file search.

The same five questions via a knowledge graph query: ~3,400 tokens. That's a 120x reduction — and it's not about fitting in the context window. It's about cost ($3-15/M tokens adds up), latency (graph queries return in <1ms vs seconds of file reading), and accuracy (LLMs lose track of relevant details in large contexts — the "lost in the middle" problem).

It's a single Go binary: tree-sitter parses your code into a SQLite-backed knowledge graph. Functions, call chains, routes, cross-service HTTP links — all queryable via Cypher-like syntax or structured search. You say "Index this project" and then ask things like "what calls ProcessOrder?" or "find dead code."

No Docker, no external databases, no API keys. 63 languages. Auto-syncs when you edit files. Benchmarked against real repos (78 to 49K nodes) including the Linux kernel (20K nodes, 67K edges, zero timeouts).

There are other code graph MCP servers — GitNexus being the biggest (7K+ stars, great visual UI). Key differences: we support 63 languages vs 8-11, ship as a single Go binary (no Node.js/npm), have no embedded LLM (your MCP client IS the intelligence layer — no extra API keys or per-query cost), auto-sync on file changes, and are the only one with published benchmarks across real repos.

GitNexus is great for visual exploration. We're building production tooling.

Happy to discuss architecture, benchmarks, or trade-offs.

---

## Response Playbook

**"Context windows are huge now, why does this matter?"**
→ Paste Prep A counterargument (cost, latency, accuracy — not window size)

**"How is this different from GitNexus / X?"**
→ Paste Prep B comparison table + pre-drafted answer

**"63 languages but how good is each one?"**
→ Link to BENCHMARK_REPORT.md, mention tier system (9 production-ready, 13 usable, 12 need fixes), be honest about gaps

**"Why not just use ctags / Language Server Protocol?"**
→ ctags gives you symbol locations, not call graphs or cross-service links. LSP gives you per-file analysis but not codebase-wide traversal. This builds a persistent graph with relationships — callers, callees, HTTP links, dead code, communities. Different tool for a different job.

**"Why Go?"**
→ Single binary distribution (no runtime), CGO for tree-sitter bindings, goroutines for concurrent parsing. The binary is ~30MB and runs anywhere without dependencies.

**"SQLite won't scale"**
→ Stress tested on Linux kernel (20K nodes, 67K edges) with zero timeouts. SQLite in WAL mode handles concurrent reads fine. The largest benchmark repo (Groovy/Spock) indexed 10K nodes / 24K edges in 11 seconds. For single-user developer tooling, SQLite is the right choice — no server process, no config, ACID guarantees.
