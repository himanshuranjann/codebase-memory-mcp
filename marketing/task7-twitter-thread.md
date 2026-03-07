# Task 7: Twitter/X Thread

Tag @AnthropicAI. Use #MCP #ClaudeCode #AI hashtags.

---

**Tweet 1:**
AI coding assistants explore codebases by reading files one at a time.

5 structural questions = ~412,000 tokens.
Same questions via a knowledge graph = ~3,400 tokens.

That's a 120x reduction. Here's how 🧵

**Tweet 2:**
At Opus pricing ($15/M tokens), that's $6.18 per exploration session vs $0.05 via graph queries.

Multiply by 20 sessions/day and you're looking at $120/day vs $1/day.

But it's not just cost — graph queries return in <1ms vs seconds of file I/O. And less noise means more accurate answers (no "lost in the middle" problem).

**Tweet 3:**
How it works:

Tree-sitter parses your code → functions, classes, call relationships extracted → stored in a SQLite graph → queried via MCP tools or CLI

You say "Index this project" → ask "what calls ProcessOrder?" → get the exact call chain in one query. No file reading.

63 languages. Single Go binary. No Docker, no API keys.

**Tweet 4:**
Benchmarked across 35 real open-source repos:
- 78 to 49,000 nodes per repo
- Linux kernel stress test: 20K nodes, 67K edges, zero timeouts
- Sub-ms query times
- Auto-sync keeps the graph fresh when you edit files

Full per-language benchmark report in the repo.

**Tweet 5:**
How it compares to GitNexus (7K+ stars):

GitNexus: great visual web UI, built-in chat, 8-11 languages, needs Node.js + API keys for embedded LLM

codebase-memory-mcp: 63 languages, single Go binary, no LLM overhead, auto-sync, published benchmarks. 12 MCP tools vs 7.

Think "visual dashboard" vs "production tooling."

**Tweet 6:**
Open source, MIT licensed. Works with Claude Code, Codex CLI, Cursor, and Windsurf.

Install in one command, say "Index this project" — done.

https://github.com/DeusData/codebase-memory-mcp

#MCP #ClaudeCode #AI #DevTools
