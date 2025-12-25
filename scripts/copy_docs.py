#!/usr/bin/env python3
"""
Script to copy generated Go documentation to the docs repository.

Usage:
    python scripts/copy_docs.py <sdk_root> <docs_root>

Example:
    python scripts/copy_docs.py . ../fish-docs
    python scripts/copy_docs.py sdk docs  # In CI context
"""

import argparse
import re
from pathlib import Path


FRONTMATTER = """---
title: "API Reference"
description: "Complete reference for Fish Audio Go SDK"
icon: "book"
---

"""


def copy_docs(sdk_root: Path, docs_root: Path) -> None:
    """Copy generated documentation to the docs repository."""
    source_file = sdk_root / "build" / "docs" / "fishaudio.md"

    if not source_file.exists():
        print(f"Error: {source_file} does not exist. Run gomarkdoc first.")
        return

    dest_dir = docs_root / "api-reference" / "sdk" / "go"
    dest_dir.mkdir(parents=True, exist_ok=True)
    dest_file = dest_dir / "api-reference.mdx"

    print(f"Reading {source_file}")
    content = source_file.read_text(encoding="utf-8")

    # Remove HTML comments that cause MDX parsing errors
    content = re.sub(r"<!--.*?-->", "", content, flags=re.DOTALL)

    # Remove the Index section (## Index ... until next ##)
    content = re.sub(r"## Index\n.*?(?=\n## )", "", content, flags=re.DOTALL)

    # Add frontmatter
    content = FRONTMATTER + content

    print(f"Writing to {dest_file}")
    dest_file.write_text(content, encoding="utf-8")

    print("Documentation copy completed successfully!")


def main() -> None:
    parser = argparse.ArgumentParser(
        description="Copy generated Go documentation to the docs repository"
    )
    parser.add_argument(
        "sdk_root",
        type=Path,
        help="Root directory of the fish-audio-go SDK",
    )
    parser.add_argument(
        "docs_root",
        type=Path,
        help="Root directory of the docs repository",
    )

    args = parser.parse_args()

    if not args.sdk_root.exists():
        parser.error(f"SDK root directory does not exist: {args.sdk_root}")

    if not args.docs_root.exists():
        parser.error(f"Docs root directory does not exist: {args.docs_root}")

    copy_docs(args.sdk_root, args.docs_root)


if __name__ == "__main__":
    main()