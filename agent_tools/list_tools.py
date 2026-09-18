#!/usr/bin/env python3
"""Scan agent_tools/ for executable scripts and print their docstrings.

Reads every .py, .sh, and .ts file in the agent_tools/ directory (excluding
this file), extracts the first docstring/comment block, and prints a JSON
array of {name, path, docstring, language} objects.

Usage:
    python3 agent_tools/list_tools.py          # human-readable table
    python3 agent_tools/list_tools.py --json   # JSON for programmatic use
"""

import argparse
import json
import os
import re
import sys
from pathlib import Path


AGENT_TOOLS_DIR = Path(__file__).resolve().parent

# File extensions we recognize as tools
TOOL_EXTENSIONS = {
    ".py": "python",
    ".sh": "shell",
    ".bash": "shell",
    ".ts": "typescript",
    ".js": "javascript",
}


def extract_docstring(filepath: Path, language: str) -> str:
    """Extract the first docstring/comment block from a tool file."""
    try:
        content = filepath.read_text()
    except Exception:
        return ""

    if language == "python":
        # Match module-level """...""" or '''...'''
        m = re.search(r'(?s)^\s*"""(.*?)"""', content)
        if m:
            return m.group(1).strip()
        m = re.search(r"(?s)^\s*'''(.*?)'''", content)
        if m:
            return m.group(1).strip()

    elif language in ("shell",):
        # Match ## comments at the top of the file (after shebang)
        lines = content.split("\n")
        doc_lines = []
        for line in lines:
            stripped = line.strip()
            if stripped.startswith("##"):
                doc_lines.append(stripped.lstrip("#").strip())
            elif stripped and not stripped.startswith("#!"):
                break  # first non-comment-non-shebang line ends the doc
        return "\n".join(doc_lines)

    elif language in ("typescript", "javascript"):
        # Match /** ... */ JSDoc block
        m = re.search(r"(?s)/\*\*(.*?)\*/", content)
        if m:
            # Clean up leading * on each line
            doc = m.group(1)
            doc = re.sub(r"(?m)^\s*\*\s?", "", doc)
            return doc.strip()

    return ""


def list_tools() -> list[dict]:
    """Scan agent_tools/ and return a list of tool descriptors."""
    tools = []
    for entry in sorted(AGENT_TOOLS_DIR.iterdir()):
        if entry.name == os.path.basename(__file__):
            continue  # skip self
        suffix = entry.suffix.lower()
        if suffix not in TOOL_EXTENSIONS:
            continue
        language = TOOL_EXTENSIONS[suffix]
        docstring = extract_docstring(entry, language)
        tools.append(
            {
                "name": entry.stem,
                "path": str(entry.relative_to(AGENT_TOOLS_DIR.parent)),
                "docstring": docstring.split("\n")[0] if docstring else "",
                "full_docstring": docstring,
                "language": language,
            }
        )
    return tools


def main():
    parser = argparse.ArgumentParser(
        description="List agent tools with docstrings"
    )
    parser.add_argument(
        "--json",
        action="store_true",
        help="Output as JSON",
    )
    args = parser.parse_args()

    tools = list_tools()

    if not tools:
        if args.json:
            print("[]")
        else:
            print("No tools found in agent_tools/")
        return

    if args.json:
        json.dump(tools, sys.stdout, indent=2)
        sys.stdout.write("\n")
    else:
        # Human-readable table
        for tool in tools:
            doc = tool["docstring"] or "(no docstring)"
            if len(doc) > 80:
                doc = doc[:77] + "..."
            print(f"  {tool['name']:<30} [{tool['language']:<10}] {doc}")


if __name__ == "__main__":
    main()
