# Task 5: Discord Posts

## Anthropic Claude Developers (64K+ members)

**Channel:** #mcp or #tools

**Post:**

**codebase-memory-mcp** — MCP server that indexes your codebase into a persistent knowledge graph

Instead of Claude Code grepping through files one at a time for structural questions, this builds a tree-sitter-based knowledge graph in SQLite. One graph query returns what would take dozens of file reads.

**The numbers:** 5 structural queries consumed ~3,400 tokens via graph vs ~412,000 via file-by-file exploration — 120x reduction. Tested across 63 real repos including Linux kernel.

**What it does:**
- 63 languages, sub-ms queries, auto-sync on file changes
- Call graph tracing, dead code detection, cross-service HTTP linking
- Cypher-like queries for complex graph patterns
- Architecture overview with Louvain community detection
- 12 MCP tools + CLI mode for direct terminal use

**Setup:** Single Go binary, no Docker/databases/API keys.
```
codebase-memory-mcp install   # auto-configures Claude Code
```
Then say "Index this project" — done.

Also works with Codex CLI, Cursor, and Windsurf.

MIT licensed: https://github.com/DeusData/codebase-memory-mcp

---

## MCP Community Discord (11K+ members)

**Channel:** #showcase

**Post:**

**codebase-memory-mcp** — code knowledge graph MCP server (12 tools, 63 languages, single Go binary)

Built an MCP server that parses codebases with tree-sitter into a persistent SQLite knowledge graph. Functions, classes, call relationships, HTTP routes, cross-service links — all queryable via structured search or Cypher-like syntax.

**12 tools:** `index_repository`, `search_graph`, `trace_call_path`, `detect_changes`, `query_graph`, `get_graph_schema`, `get_code_snippet`, `get_architecture`, `manage_adr`, `search_code`, `list_projects`, `delete_project`

**Key specs:**
- 63 languages via tree-sitter grammars
- Sub-ms graph queries, <10s cold index for most repos
- Auto-sync: background polling detects file changes, triggers incremental re-index
- Cross-service HTTP linking with confidence scoring (discovers REST calls between services)
- Louvain community detection for codebase architecture
- Architecture Decision Records that persist across sessions
- CLI mode for use outside MCP clients

**Benchmark:** 120x fewer tokens than file-by-file exploration across 63 real repos (78 to 49K nodes). Stress tested on Linux kernel (20K nodes, 67K edges).

Compatible with Claude Code, Codex CLI, Cursor, Windsurf. MIT licensed.

https://github.com/DeusData/codebase-memory-mcp
