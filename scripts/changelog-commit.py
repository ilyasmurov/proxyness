#!/usr/bin/env python3
"""Parse CHANGELOG.new.md and append entries to changelog.json."""
import json
import os
import sys
import uuid
from datetime import datetime, timezone

CHANGELOG_NEW = "CHANGELOG.new.md"
CHANGELOG_JSON = "changelog.json"
MAX_ENTRIES = 200
VALID_TYPES = {"feature", "fix", "improvement"}


def parse_new_entries(text: str) -> list[dict]:
    entries = []
    blocks = text.strip().split("## ")
    for block in blocks:
        block = block.strip()
        if not block:
            continue
        lines = block.split("\n")
        entry_type = lines[0].strip().lower()
        if entry_type not in VALID_TYPES:
            print(f"\033[31mError: unknown type '{entry_type}'. Allowed: {', '.join(VALID_TYPES)}\033[0m")
            sys.exit(1)

        title = lines[1].strip() if len(lines) > 1 else ""
        if not title:
            print(f"\033[31mError: entry of type '{entry_type}' has no title\033[0m")
            sys.exit(1)

        description = "\n".join(lines[2:]).strip() or None

        entries.append({
            "id": str(uuid.uuid4()),
            "title": title,
            "description": description,
            "type": entry_type,
            "createdAt": datetime.now(timezone.utc).isoformat(),
        })
    return entries


def main():
    if not os.path.exists(CHANGELOG_NEW):
        print(f"\033[31mError: {CHANGELOG_NEW} not found. Create it before committing.\033[0m")
        print(f"\033[33mFormat:\033[0m")
        print(f"  ## feature")
        print(f"  Title of the change")
        print(f"  Optional description")
        print(f"  ")
        print(f"  ## fix")
        print(f"  Another change")
        sys.exit(1)

    with open(CHANGELOG_NEW) as f:
        text = f.read()

    if not text.strip():
        print(f"\033[31mError: {CHANGELOG_NEW} is empty\033[0m")
        sys.exit(1)

    new_entries = parse_new_entries(text)
    if not new_entries:
        print(f"\033[31mError: no valid entries found in {CHANGELOG_NEW}\033[0m")
        sys.exit(1)

    # Load existing changelog
    existing = []
    if os.path.exists(CHANGELOG_JSON):
        with open(CHANGELOG_JSON) as f:
            existing = json.load(f)

    # Prepend new entries, keep MAX_ENTRIES
    combined = new_entries + existing
    combined = combined[:MAX_ENTRIES]

    with open(CHANGELOG_JSON, "w") as f:
        json.dump(combined, f, indent=2, ensure_ascii=False)

    # Remove the new file
    os.remove(CHANGELOG_NEW)

    for e in new_entries:
        color = {"feature": "32", "fix": "31", "improvement": "33"}[e["type"]]
        print(f"  \033[{color}m[{e['type']}]\033[0m {e['title']}")

    print(f"\n\033[32mAdded {len(new_entries)} changelog entry(ies)\033[0m")


if __name__ == "__main__":
    main()
