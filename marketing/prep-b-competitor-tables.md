# Prep B: Competitor Comparison Tables

## Primary: vs GitNexus

| Feature | codebase-memory-mcp | GitNexus |
|---------|-------------------|----------|
| Languages | **35** | 8-11 |
| Runtime | **Single Go binary** | Node.js (npx) |
| Runtime dependency | **None** | Node.js required |
| Database | SQLite (embedded) | KuzuDB (embedded) |
| Embedded LLM | **No** (uses your existing MCP client) | Yes (requires API key + cost per query) |
| Published benchmarks | **Yes** (63 repos, Linux kernel, 120x measured) | No |
| Auto-sync | **Yes** (background polling) | No (manual re-analyze) |
| MCP tools | **12** | 7 |
| Cross-service HTTP linking | **Yes** (confidence scoring) | No |
| Cypher query language | **Yes** | No |
| Architecture Decision Records | **Yes** | No |
| Louvain community detection | **Yes** | No |
| Visual web UI | No | **Yes** (browser-based) |
| Graph RAG chat | No | **Yes** (built-in LLM chat) |
| Install | `tar xzf && install` | `npx gitnexus` |
| Binary size | ~30MB standalone | npm package tree |

### Pre-drafted "how is this different from GitNexus?" answer

GitNexus and codebase-memory-mcp solve the same core problem (code knowledge graphs for AI assistants) but make different trade-offs:

GitNexus: Great visual web UI, built-in Graph RAG chat, but requires Node.js, only supports 8-11 languages, and the embedded LLM adds API key requirements and per-query cost on top of what your AI assistant already costs.

codebase-memory-mcp: No visual UI, but covers 63 languages, ships as a single Go binary (no Node.js), has no LLM overhead (your MCP client IS the intelligence layer), auto-syncs on file changes, and has published benchmarks across 63 real repos including the Linux kernel. 12 MCP tools vs 7, plus Cypher queries, cross-service HTTP linking, and ADR management.

Think "visual dashboard" vs "production tooling." If you want to explore a codebase visually, GitNexus is great. If you want the most comprehensive, zero-dependency, benchmarked backend for your AI coding assistant, that's what we built.

---

## Secondary: vs Other Competitors

| Feature | codebase-memory-mcp | CodePathfinder | code-graph-mcp | Axon |
|---------|-------------------|----------------|-----------------|------|
| Languages | 35 | Python only | 25+ | Go-focused |
| Runtime | Single Go binary | Python | Python | Go binary |
| Infrastructure | None (SQLite) | None | None | None (SQLite) |
| Published benchmarks | Yes | No | No | No |
| License | MIT | AGPL | MIT | MIT |
