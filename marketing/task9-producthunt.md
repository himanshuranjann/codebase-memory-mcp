# Task 9: Product Hunt Launch

## Timing
Tuesday-Thursday, Week 2. Create "upcoming" page 1 week before.

## Tagline
Code knowledge graph that gives AI assistants 120x fewer tokens

## Description (short)
codebase-memory-mcp indexes your codebase into a persistent knowledge graph using tree-sitter. 63 languages, sub-ms queries, single Go binary. No Docker, no API keys. Works with Claude Code, Codex CLI, Cursor, Windsurf.

## Description (long)
AI coding assistants explore codebases by reading files one at a time. Five structural questions consume ~412,000 tokens via file-by-file search.

codebase-memory-mcp builds a persistent knowledge graph from your code — the same five questions cost ~3,400 tokens. That's a 120x reduction.

Tree-sitter parses 63 languages into a SQLite-backed graph. Functions, classes, call chains, HTTP routes, cross-service links — all queryable via structured search or Cypher-like syntax. Auto-sync keeps the graph fresh when you edit files.

12 MCP tools for: call graph tracing, dead code detection, cross-service HTTP linking, architecture overview with Louvain community detection, git diff impact analysis, and more.

Single Go binary. No Docker, no external databases, no API keys. Install in one command.

Benchmarked across 35 real open-source repositories including the Linux kernel.

## First Comment (<800 chars)
I built this because I was spending $6/session on AI code exploration at Opus pricing. Graph queries do the same work for $0.05. The 120x token reduction means lower cost, faster responses, and more accurate answers — less context noise means the AI doesn't "lose" the relevant information among thousands of irrelevant lines.

63 languages via tree-sitter, single Go binary, MIT licensed. Works with Claude Code, Codex CLI, Cursor, Windsurf. Full benchmark data in the repo.

## Topics/Tags
- Developer Tools
- Artificial Intelligence
- Open Source
- Productivity
- Code Review

## Screenshots/GIFs needed
1. Claude Code conversation showing "Index this project" → graph result
2. Side-by-side token comparison (graph vs explorer)
3. Call graph trace output
4. Architecture overview output
5. CLI mode usage
