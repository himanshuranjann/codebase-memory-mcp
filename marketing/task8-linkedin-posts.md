# Task 8: LinkedIn Posts

---

## Post 2: Benchmark Comparison (within 5 days of Post 1)

Format: Carousel/PDF if possible, or text with embedded table.

---

AI coding assistants are expensive when they explore your codebase.

I measured the token cost of 5 common structural questions — "what calls this function?", "find dead code", "show API routes" — using two approaches:

📊 File-by-file exploration (grep/read): ~412,000 tokens
📊 Knowledge graph queries: ~3,400 tokens

That's a 120x reduction. Here's the per-question breakdown:

| Question | Graph | File-by-file | Savings |
|----------|-------|-------------|---------|
| Find function | ~200 | ~45,000 | 225x |
| Trace call chain | ~800 | ~120,000 | 150x |
| Dead code detection | ~500 | ~85,000 | 170x |
| List routes | ~400 | ~62,000 | 155x |
| Architecture overview | ~1,500 | ~100,000 | 67x |

At Opus pricing ($15/M tokens), that's $6.18 vs $0.05 per session. Across a full workday, it compounds fast.

But the bigger win isn't cost — it's accuracy. When an AI assistant reads 50 files to answer one question, the answer gets buried in noise. A graph query returns exactly the structural information needed. No "lost in the middle" problem.

We benchmarked this across 35 real open-source repos (78 to 49K nodes), including a Linux kernel stress test (20K nodes, 67K edges, zero timeouts).

The tool: codebase-memory-mcp — a single Go binary that parses your code with tree-sitter into a persistent knowledge graph. 63 languages, sub-ms queries, auto-sync. Works with Claude Code, Codex CLI, Cursor, Windsurf.

Open source, MIT licensed → https://github.com/DeusData/codebase-memory-mcp

#DeveloperTools #AI #CodingAssistants #OpenSource

---

## Post 3: Milestone Update (when hitting 100 / 250 / 500 stars)

---

[X] GitHub stars in [Y] days.

When I posted about codebase-memory-mcp last week, I wasn't sure how many developers would care about token efficiency for AI coding assistants. Turns out — quite a few.

The thing that resonated most: it's not about fitting in the context window. It's about cost, speed, and accuracy. A knowledge graph query returns the exact structural information in ~500 tokens. File-by-file exploration dumps entire files into context — 80K+ tokens of noise for one answer.

Since launch:
- [N] GitHub stars / [N] forks
- [N] issues opened (including [N] community contributions)
- Listed in [N] MCP directories
- Most popular feature request: [whatever it is]

The benchmark report (63 languages, per-question scoring) seems to be what gives people confidence to try it — transparency about what works and what doesn't.

Next up: [whatever you're working on — e.g., "improving arrow function support for JS/TS" or "visual architecture diagrams"].

https://github.com/DeusData/codebase-memory-mcp

#OpenSource #DeveloperTools #AI

---

## Post 4: Article Share (after Dev.to article is live)

---

I wrote up the full technical deep-dive on how we reduced AI code exploration from 412K tokens to 3.4K — and why context window size doesn't make this irrelevant.

The article covers:
→ The actual cost calculation ($6.18 vs $0.05 per session at Opus pricing)
→ Why "lost in the middle" makes large contexts actively harmful for code exploration
→ How tree-sitter + SQLite gives you a persistent code knowledge graph
→ Honest comparison with GitNexus (7K+ stars) — visual dashboard vs production tooling
→ Full benchmark data across 63 real repos

The counterintuitive insight: bigger context windows make token efficiency MORE important, not less. More capacity means more sessions, which means costs compound faster.

Read the full article → [Dev.to link]

Code → https://github.com/DeusData/codebase-memory-mcp

#AI #DeveloperTools #SoftwareEngineering #OpenSource
