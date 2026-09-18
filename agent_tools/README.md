# Agent Tools

> Reusable scripts built by the agent during development. Each tool is a
> small, self-contained script with a docstring that explains what it does,
> its inputs, and its outputs. Tools are meant to be tweaked and reused
> across sessions — not thrown away after one use.

## Quick Start

```bash
# List all available tools with docstrings
python3 agent_tools/list_tools.py

# List as JSON (for the extension)
python3 agent_tools/list_tools.py --json
```

## Tool Catalog

*(No tools yet. The agent will add them here as they are created.)*

---

## Conventions

- **Every tool MUST have a docstring.** Python: `"""..."""`. Shell: `##`
  block comment at the top describing inputs, outputs, and side effects.
  TypeScript: `/** ... */`.
- **Tools are stateless.** They read inputs, produce outputs, and exit.
- **One tool = one file.** `snake_case` verb-noun naming.
- **Add new tools to this README** when you create them. Keep the catalog
  in sync.
