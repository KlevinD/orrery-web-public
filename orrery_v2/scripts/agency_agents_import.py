#!/usr/bin/env python3
"""Convert agency-agents-zh Markdown agents into an Orrery Talent Market ZIP."""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import re
import shutil
import tempfile
import zipfile
from dataclasses import dataclass
from datetime import datetime, timezone
from pathlib import Path
from typing import Any


IGNORED_NAMES = {
    "readme.md",
    "readme.zh-tw.md",
    "catalog.md",
    "agent-list.md",
    "upstream.md",
    "contributing.md",
    "license.md",
}
IGNORED_DIRS = {"examples", "strategy", "integrations", "assets", "scripts"}


@dataclass
class ParsedMarkdown:
    frontmatter: str
    fields: dict[str, str]
    body: str


def safe_slug(value: str, fallback: str = "agent", ascii_only: bool = False) -> str:
    value = value.removesuffix(".md").strip().lower()
    if ascii_only:
        slug = re.sub(r"[^a-z0-9]+", "-", value)
    else:
        slug = re.sub(r"[^a-z0-9\u4e00-\u9fff]+", "-", value)
    slug = slug.strip("-")
    return slug or fallback


def frontmatter_scalar(value: str) -> str:
    value = value.strip()
    if len(value) >= 2 and value[0] == value[-1] and value[0] in {"'", '"'}:
        return value[1:-1]
    return value


def parse_markdown(content: str) -> ParsedMarkdown | None:
    match = re.match(r"(?s)^---\s*\r?\n(?P<frontmatter>.*?)\r?\n---\s*\r?\n?(?P<body>.*)$", content)
    if not match:
        return None

    frontmatter = match.group("frontmatter")
    fields: dict[str, str] = {}
    for line in re.split(r"\r?\n", frontmatter):
        field = re.match(r"^\s*([A-Za-z0-9_-]+):\s*(.*?)\s*$", line)
        if not field:
            continue
        fields[field.group(1)] = frontmatter_scalar(field.group(2))

    return ParsedMarkdown(frontmatter=frontmatter, fields=fields, body=match.group("body").strip())


def get_frontmatter_block(frontmatter: str, keys: set[str]) -> list[str]:
    lines = re.split(r"\r?\n", frontmatter)
    capture = False
    captured: list[str] = []
    for line in lines:
        top_level = re.match(r"^(?P<key>[A-Za-z0-9_-]+):\s*(?P<value>.*)$", line)
        if top_level:
            if capture:
                break
            key = top_level.group("key")
            if key in keys:
                capture = True
                value = top_level.group("value")
                if value.strip():
                    captured.append(value)
            continue
        if capture:
            captured.append(line)
    return captured


def read_indented_block(lines: list[str], start: int, parent_indent: int) -> tuple[str, int]:
    block: list[str] = []
    index = start
    while index < len(lines):
        line = lines[index]
        if re.match(r"^\s*-\s+", line):
            break
        prop = re.match(r"^(?P<indent>\s+)(?P<key>[A-Za-z0-9_-]+):\s*", line)
        if prop and len(prop.group("indent")) <= parent_indent:
            break
        if not line.strip():
            block.append("")
        else:
            trim_width = min(len(line), parent_indent + 2)
            block.append(line[trim_width:])
        index += 1
    return "\n".join(block).strip(), index


def parse_frontmatter_list_of_maps(lines: list[str]) -> list[dict[str, str]]:
    if not lines:
        return []

    if len(lines) == 1 and not re.match(r"^\s*-", lines[0]):
        scalar = frontmatter_scalar(lines[0])
        return [{"id": item.strip()} for item in scalar.split(",") if item.strip()]

    items: list[dict[str, str]] = []
    index = 0
    while index < len(lines):
        line = lines[index]
        item_start = re.match(r"^\s*-\s*(?P<rest>.*)$", line)
        if not item_start:
            index += 1
            continue

        item: dict[str, str] = {}
        rest = item_start.group("rest").strip()
        if rest:
            inline = re.match(r"^(?P<key>[A-Za-z0-9_-]+):\s*(?P<value>.*)$", rest)
            if inline:
                item[inline.group("key")] = frontmatter_scalar(inline.group("value"))
            else:
                item["id"] = frontmatter_scalar(rest)

        index += 1
        while index < len(lines):
            line = lines[index]
            if re.match(r"^\s*-\s+", line):
                break
            field = re.match(r"^(?P<indent>\s+)(?P<key>[A-Za-z0-9_-]+):\s*(?P<value>.*)$", line)
            if not field:
                index += 1
                continue
            indent = len(field.group("indent"))
            key = field.group("key")
            value = field.group("value").strip()
            if value in {"|", ">"}:
                block, index = read_indented_block(lines, index + 1, indent)
                item[key] = block
                continue
            item[key] = frontmatter_scalar(value)
            index += 1

        if item:
            items.append(item)

    return items


def get_map_value(item: dict[str, str], keys: tuple[str, ...]) -> str:
    for key in keys:
        value = item.get(key, "")
        if str(value).strip():
            return str(value)
    return ""


def string_list(value: Any) -> list[str]:
    if value is None:
        return []
    if isinstance(value, list):
        items = value
    else:
        items = str(value).split(",")
    return [str(item).strip() for item in items if str(item).strip()]


def explicit_skill_data(frontmatter: str, agent_slug: str, source_path: str) -> tuple[list[dict[str, Any]], list[dict[str, Any]]]:
    lines = get_frontmatter_block(frontmatter, {"skills", "skill_refs"})
    items = parse_frontmatter_list_of_maps(lines)
    skill_refs: list[dict[str, Any]] = []
    skill_definitions: list[dict[str, Any]] = []

    for item in items:
        skill_id = get_map_value(item, ("skill_id", "skillId", "id")).strip()
        name = get_map_value(item, ("name", "alias")).strip()
        if not skill_id:
            if not name:
                continue
            skill_id = f"skill.{agent_slug}.{safe_slug(name)}"

        alias = get_map_value(item, ("alias", "name")).strip() or skill_id
        version_constraint = get_map_value(item, ("version_constraint", "versionConstraint", "constraint")).strip()
        level = get_map_value(item, ("level",)).strip()
        evidence_refs = string_list(get_map_value(item, ("evidence_refs", "evidenceRefs")))
        skill_refs.append(
            {
                "skill_id": skill_id,
                "version_constraint": version_constraint,
                "alias": alias,
                "level": level,
                "evidence_refs": evidence_refs,
            }
        )

        content = get_map_value(item, ("content", "prompt", "skill_md", "skillMd")).strip()
        description = get_map_value(item, ("description",)).strip()
        version = get_map_value(item, ("version",)).strip() or "1.0.0"
        has_definition = bool(content or description or name)
        if not has_definition:
            continue
        name = name or alias
        description = description or name
        content = content or f"# {name}\n\n{description}"
        skill_definitions.append(
            {
                "skill_id": skill_id,
                "name": name,
                "version": version,
                "description": description,
                "domain_tags": string_list(get_map_value(item, ("domain_tags", "domainTags", "domains", "tags"))),
                "task_type_tags": string_list(get_map_value(item, ("task_type_tags", "taskTypeTags", "task_types"))),
                "prompt_assets": [
                    {
                        "path": "SKILL.md",
                        "content_ref": "",
                        "content": content,
                        "kind": "markdown",
                    }
                ],
                "required_tools": [],
                "evidence_refs": evidence_refs,
                "enabled": True,
                "source_ref": source_path,
            }
        )

    return skill_refs, skill_definitions


def markdown_section(body: str, names: tuple[str, ...], max_length: int) -> str:
    collecting = False
    captured: list[str] = []
    lowered_names = tuple(name.lower() for name in names)
    for line in re.split(r"\r?\n", body):
        header = re.match(r"^\s*#{2,}\s*(?P<title>.+?)\s*$", line)
        if header:
            if collecting:
                break
            title = header.group("title").lower()
            if any(name in title for name in lowered_names):
                collecting = True
            continue
        if collecting:
            captured.append(line)
    text = "\n".join(captured).strip()
    if len(text) > max_length:
        return text[:max_length].strip()
    return text


def content_hash(content: str) -> str:
    return "sha256:" + hashlib.sha256(content.encode("utf-8")).hexdigest()


def should_skip_markdown(relative: str) -> bool:
    normalized = relative.replace("\\", "/").lower()
    name = normalized.rsplit("/", 1)[-1]
    if name in IGNORED_NAMES:
        return True
    parts = [part for part in normalized.split("/") if part]
    return any(part in IGNORED_DIRS for part in parts[:-1])


def candidate_from_markdown(path: Path, source_root: Path, mode: str) -> tuple[dict[str, Any], list[dict[str, Any]]] | None:
    content = path.read_text(encoding="utf-8")
    parsed = parse_markdown(content)
    if parsed is None:
        return None

    name = parsed.fields.get("name", "").strip()
    description = parsed.fields.get("description", "").strip()
    if not name or not description:
        return None

    relative = path.relative_to(source_root).as_posix()
    category = relative.split("/", 1)[0] if "/" in relative else ""
    slug = safe_slug(path.stem)
    body = parsed.body
    skill_refs, skill_definitions = explicit_skill_data(parsed.frontmatter, slug, relative)

    role_name = description[:120].strip() if len(description) > 120 else description

    if mode == "build":
        candidate = {
            "agent_profile_id": f"profile-market-{slug}",
            "real_name": name,
            "display_name": name,
            "role_name": description,
            "agent_kind": "ai",
            "description": description,
            "category": category,
            "source_slug": slug,
            "source_path": relative,
            "source_hash": content_hash(content),
            "source_prompt": content.strip(),
            "runtime_kind": "managed",
            "persona": {
                "backstory": body or description,
                "responsibility_boundary": f"负责{description}，并按 BaseAgent 审计要求提交过程、结果和证据。",
                "behavior_boundary": markdown_section(body, ("规则", "原则", "rules", "guidelines"), 800),
            },
            "skill_refs": skill_refs,
            "tool_refs": [],
            "qa_enabled": True,
            "enabled": True,
        }
    else:
        candidate = {
            "agent_profile_id": f"profile-market-{slug}",
            "real_name": name,
            "display_name": name,
            "role_name": role_name,
            "agent_kind": "ai",
            "description": description,
            "category": category,
            "source_slug": slug,
            "source_path": relative,
            "source_hash": content_hash(content),
            "source_prompt": content,
            "runtime_kind": "managed",
            "persona": {
                "backstory": body,
                "responsibility_boundary": (
                    f"Responsible for {description}. Submit process, result, and evidence according "
                    "to BaseAgent audit requirements."
                ),
                "behavior_boundary": markdown_section(body, ("rules", "guidelines", "checklist", "principles"), 900),
            },
            "personal_memory_ref": {
                "memory_profile_id": f"memory-{slug}",
                "memory_kind": "personal",
                "access_scope": "agent",
            },
            "collective_memory_refs": [],
            "skill_refs": skill_refs,
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

    return candidate, skill_definitions


def write_json(path: Path, data: Any) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(data, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")


def convert(source_root: Path, output_zip: Path, mode: str) -> dict[str, Any]:
    source_root = source_root.resolve()
    if not source_root.is_dir():
        raise SystemExit(f"Source directory does not exist: {source_root}")

    output_zip = output_zip.resolve()
    output_zip.parent.mkdir(parents=True, exist_ok=True)

    temp_root = Path(tempfile.mkdtemp(prefix="orrery-agency-agents-"))
    try:
        agents_dir = temp_root / "agents"
        skills_dir = temp_root / "skills"
        agents_dir.mkdir(parents=True, exist_ok=True)

        agents: list[dict[str, Any]] = []
        skills: list[dict[str, Any]] = []
        seen_ids: set[str] = set()
        seen_skill_ids: set[str] = set()

        files = sorted(source_root.rglob("*.md"), key=lambda item: str(item).lower())
        for file in files:
            relative = file.relative_to(source_root).as_posix()
            if should_skip_markdown(relative):
                continue
            item = candidate_from_markdown(file, source_root, mode)
            if item is None:
                continue
            candidate, skill_definitions = item
            base_id = candidate["agent_profile_id"]
            dedupe = 2
            while candidate["agent_profile_id"] in seen_ids:
                candidate["agent_profile_id"] = f"{base_id}-{dedupe}"
                dedupe += 1
            seen_ids.add(candidate["agent_profile_id"])
            agents.append(candidate)
            for skill in skill_definitions:
                skill_id = str(skill.get("skill_id", "")).strip()
                if not skill_id or skill_id in seen_skill_ids:
                    continue
                seen_skill_ids.add(skill_id)
                skills.append(skill)

        if not agents:
            raise SystemExit(f"No importable agents found in {source_root}")

        manifest = {
            "format": "orrery.talent-market",
            "version": 1,
            "count": len(agents),
            "exported_at": datetime.now(timezone.utc).isoformat().replace("+00:00", "Z"),
            "source_app": "agency-agents-zh" if mode == "build" else "jnMetaCode/agency-agents-zh",
            "description": (
                "All agency-agents-zh markdown agents converted to Orrery Talent Market import candidates."
                if mode == "build"
                else "Converted from jnMetaCode/agency-agents-zh markdown agents for Orrery Talent Market."
            ),
        }
        write_json(temp_root / "manifest.json", manifest)

        for agent in agents:
            write_json(agents_dir / f"{safe_slug(agent['source_slug'])}.json", agent)

        if skills:
            skills_dir.mkdir(parents=True, exist_ok=True)
            for skill in skills:
                write_json(skills_dir / f"{safe_slug(skill['skill_id'], fallback='skill', ascii_only=True)}.json", skill)

        if output_zip.exists():
            output_zip.unlink()
        with zipfile.ZipFile(output_zip, "w", compression=zipfile.ZIP_DEFLATED) as archive:
            for file in sorted(temp_root.rglob("*")):
                if file.is_file():
                    archive.write(file, file.relative_to(temp_root).as_posix())

        if mode == "build":
            return {
                "SourceRoot": str(source_root),
                "OutputZip": str(output_zip),
                "Count": len(agents),
                "SkillCount": len(skills),
            }
        return {
            "source_dir": str(source_root),
            "output_zip": str(output_zip),
            "count": len(agents),
            "skill_count": len(skills),
        }
    finally:
        shutil.rmtree(temp_root, ignore_errors=True)


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--mode", choices=("build", "export"), required=True)
    parser.add_argument("--source-root", required=True)
    parser.add_argument("--output-zip", required=True)
    args = parser.parse_args()

    result = convert(Path(args.source_root), Path(args.output_zip), args.mode)
    print(json.dumps(result, ensure_ascii=False, indent=2))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
