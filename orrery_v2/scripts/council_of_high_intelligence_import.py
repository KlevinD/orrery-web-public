#!/usr/bin/env python3
"""Build an Orrery Talent Market package from Council of High Intelligence."""

from __future__ import annotations

import argparse
import json
import re
import zipfile
from datetime import datetime, timezone
from pathlib import Path
from typing import Any

from agency_agents_import import content_hash, frontmatter_scalar, parse_markdown, safe_slug


SOURCE_APP = "0xNyk/council-of-high-intelligence"
SOURCE_URL = "https://github.com/0xNyk/council-of-high-intelligence"
CATEGORY = "council-of-high-intelligence"


def frontmatter_mapping_block(frontmatter: str, key: str) -> dict[str, Any]:
    lines = frontmatter.splitlines()
    start = next((index for index, line in enumerate(lines) if line == f"{key}:"), None)
    if start is None:
        return {}

    values: dict[str, Any] = {}
    for line in lines[start + 1 :]:
        if line and not line.startswith((" ", "\t")):
            break
        field = re.match(r"^\s{2}(?P<key>[A-Za-z0-9_-]+):\s*(?P<value>.*?)\s*$", line)
        if not field:
            continue
        value = field.group("value")
        if value.startswith("[") and value.endswith("]"):
            values[field.group("key")] = [
                frontmatter_scalar(item.strip())
                for item in value[1:-1].split(",")
                if item.strip()
            ]
        else:
            values[field.group("key")] = frontmatter_scalar(value)
    return values


def markdown_section(body: str, heading: str) -> str:
    match = re.search(
        rf"(?ms)^##\s+{re.escape(heading)}(?:\s+[^\r\n]*)?\s*\r?\n(?P<body>.*?)(?=^##\s+|\Z)",
        body,
    )
    return match.group("body").strip() if match else ""


def common_candidate_fields(slug: str) -> dict[str, Any]:
    return {
        "agent_profile_id": f"profile-market-{slug}",
        "agent_kind": "ai",
        "category": CATEGORY,
        "source_slug": slug,
        "runtime_kind": "managed",
        "personal_memory_ref": {
            "memory_profile_id": f"memory-{slug}",
            "memory_kind": "personal",
            "access_scope": "agent",
        },
        "collective_memory_refs": [],
        "skill_refs": [],
        "tool_refs": [],
        "permission_policy_ref": "policy.agent.default",
        "context_injection_policy": "task-scoped",
        "summary_contract_ref": "summary.agent.default",
        "deliverable_contract_ref": "deliverable.task.default",
        "evidence_contract_ref": "evidence.task.required",
        "qa_contract_ref": "qa.agent.standard",
        "qa_enabled": True,
        "enabled": True,
    }


def member_candidate(path: Path, source_root: Path) -> dict[str, Any]:
    content = path.read_text(encoding="utf-8")
    parsed = parse_markdown(content)
    if parsed is None:
        raise ValueError(f"Council member is missing Markdown frontmatter: {path}")

    metadata = frontmatter_mapping_block(parsed.frontmatter, "council")
    figure = str(metadata.get("figure", "")).strip()
    domain = str(metadata.get("domain", "")).strip()
    method = str(metadata.get("reasoning_method", "")).strip()
    if not figure or not domain or not method:
        raise ValueError(f"Council member is missing figure, domain, or reasoning_method: {path}")

    slug = safe_slug(path.stem, ascii_only=True)
    grounding = markdown_section(parsed.body, "Grounding Protocol")
    blind_spots = markdown_section(parsed.body, "What You Tend to Miss")
    behavior = "\n\n".join(
        part
        for part in (
            "将该人物作为分析视角使用，不得声称或暗示自己就是该人物本人。",
            grounding,
            blind_spots,
        )
        if part
    )

    candidate = common_candidate_fields(slug)
    candidate.update(
        {
            "real_name": f"议会分析席·{figure}",
            "display_name": figure,
            "role_name": domain,
            "description": (
                f"Council analytical lens for {domain}. Uses {method} with explicit grounding and "
                "blind-spot checks; this is an analytical instrument, not an impersonation claim."
            ),
            "source_path": path.relative_to(source_root).as_posix(),
            "source_hash": content_hash(content),
            "source_prompt": content.strip(),
            "persona": {
                "backstory": markdown_section(parsed.body, "Identity") or parsed.body,
                "responsibility_boundary": (
                    f"负责运用「{domain}」分析视角，以 {method} 方法审议复杂决策；明确区分事实、推断、"
                    "假设与未知，给出置信度、盲点和可审计依据。"
                ),
                "behavior_boundary": behavior,
            },
        }
    )
    return candidate


def coordinator_candidate(path: Path, source_root: Path) -> dict[str, Any]:
    content = path.read_text(encoding="utf-8")
    parsed = parse_markdown(content)
    if parsed is None:
        raise ValueError(f"Council coordinator is missing Markdown frontmatter: {path}")

    slug = "council-coordinator"
    description = parsed.fields.get("description", "").strip()
    candidate = common_candidate_fields(slug)
    candidate.update(
        {
            "real_name": "高智议会协调官",
            "display_name": "Council Coordinator",
            "role_name": "Structured Multi-Perspective Deliberation",
            "description": description,
            "source_path": path.relative_to(source_root).as_posix(),
            "source_hash": content_hash(content),
            "source_prompt": content.strip(),
            "persona": {
                "backstory": (
                    "Coordinates deliberately different analytical lenses for consequential decisions, "
                    "then returns a verdict that preserves dissent, unknowns, kill criteria, and next actions."
                ),
                "responsibility_boundary": (
                    "负责选择最小充分分析席阵容，确保首轮观点独立，组织直接交叉质询与加权立场记录，"
                    "并在不制造虚假共识的前提下综合裁决。"
                ),
                "behavior_boundary": (
                    "Do not use a council for factual lookup or cheap reversible choices. Label facts, inferences, "
                    "assumptions, and unknowns. Preserve unresolved disagreement and escalate split verdicts."
                ),
            },
        }
    )
    return candidate


def json_bytes(value: Any) -> bytes:
    return (json.dumps(value, ensure_ascii=False, indent=2) + "\n").encode("utf-8")


def build_package(source_root: Path, output_zip: Path, source_commit: str) -> dict[str, Any]:
    source_root = source_root.resolve()
    agents_root = source_root / "agents"
    coordinator_path = source_root / "SKILL.md"
    if not agents_root.is_dir() or not coordinator_path.is_file():
        raise SystemExit(f"Not a Council of High Intelligence checkout: {source_root}")

    member_paths = sorted(agents_root.glob("council-*.md"), key=lambda item: item.name)
    if len(member_paths) != 18:
        raise SystemExit(f"Expected 18 council member prompts, found {len(member_paths)} in {agents_root}")

    candidates = [coordinator_candidate(coordinator_path, source_root)]
    candidates.extend(member_candidate(path, source_root) for path in member_paths)
    ids = [candidate["agent_profile_id"] for candidate in candidates]
    if len(ids) != len(set(ids)):
        raise SystemExit("Council package contains duplicate agent_profile_id values")

    manifest = {
        "format": "orrery.talent-market",
        "version": 1,
        "count": len(candidates),
        "exported_at": datetime.now(timezone.utc).isoformat().replace("+00:00", "Z"),
        "source_app": SOURCE_APP,
        "description": (
            "Council of High Intelligence coordinator and 18 analytical lenses, converted from the upstream "
            f"MIT-licensed prompts at {SOURCE_URL}."
        ),
    }
    source_metadata = {
        "repository": SOURCE_URL,
        "commit": source_commit.strip(),
        "license": "MIT",
        "coordinator_prompt": "SKILL.md",
        "member_prompt_glob": "agents/council-*.md",
    }

    output_zip = output_zip.resolve()
    output_zip.parent.mkdir(parents=True, exist_ok=True)
    with zipfile.ZipFile(output_zip, "w", compression=zipfile.ZIP_DEFLATED) as archive:
        archive.writestr("manifest.json", json_bytes(manifest))
        for candidate in candidates:
            archive.writestr(f"agents/{candidate['source_slug']}.json", json_bytes(candidate))
        archive.writestr("SOURCE.json", json_bytes(source_metadata))
        license_path = source_root / "LICENSE"
        if license_path.is_file():
            archive.writestr("LICENSE", license_path.read_bytes())

    return {
        "source_root": str(source_root),
        "source_commit": source_commit.strip(),
        "output_zip": str(output_zip),
        "count": len(candidates),
        "coordinator_count": 1,
        "member_count": len(member_paths),
    }


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--source-root", required=True, help="Council of High Intelligence checkout")
    parser.add_argument("--output-zip", required=True, help="Orrery Talent Market ZIP path")
    parser.add_argument("--source-commit", default="", help="Upstream Git commit used for attribution")
    args = parser.parse_args()
    result = build_package(Path(args.source_root), Path(args.output_zip), args.source_commit)
    print(json.dumps(result, ensure_ascii=False, indent=2))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
