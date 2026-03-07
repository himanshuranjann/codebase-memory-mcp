# Prep A: Context Window Counterargument

Use this when someone says "context windows are 200K-1M now, why does token reduction matter?"

---

Token reduction isn't about fitting in the window — it's about cost, latency, and accuracy. At $3/M input tokens (Sonnet) or $15/M (Opus), 412K tokens per exploration session adds up fast across a workday. More importantly, long-context accuracy degrades: studies show LLMs lose track of relevant details in large contexts (the "lost in the middle" problem). Session compaction in Claude Code discards earlier context — so the tokens you spent grepping through files are the first to disappear.

A graph query returns exactly the structural information needed in ~500 tokens. No noise, no irrelevant file contents, no lost context. The AI assistant gets precise answers instead of having to reason over thousands of lines of code to find one call chain. It's faster, cheaper, and more accurate — regardless of how large the context window is.
