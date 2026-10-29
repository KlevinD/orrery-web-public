#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SCRIPT_PATH="$SCRIPT_DIR/export-agency-agents-talent-market.sh"

usage() {
  cat <<'EOF'
Usage: scripts/test-agency-agents-import.sh [--script-path FILE]

Runs fixture-based checks for agency-agents import/export scripts.
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --script-path|-s)
      SCRIPT_PATH="${2:?--script-path requires a value}"
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "Unknown argument: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

WORK_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/orrery-agency-import-test-source-XXXXXX")"
OUT_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/orrery-agency-import-test-output-XXXXXX")"
EXTRACT_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/orrery-agency-import-test-extract-XXXXXX")"

cleanup() {
  rm -rf "$WORK_ROOT" "$OUT_ROOT" "$EXTRACT_ROOT"
}
trap cleanup EXIT

write_agent_markdown() {
  local path="$1"
  local frontmatter="$2"
  local body="$3"
  mkdir -p "$(dirname "$path")"
  cat > "$path" <<EOF
---
$frontmatter
---

$body
EOF
}

write_agent_markdown "$WORK_ROOT/engineering/plain-agent.md" \
"name: Plain Agent
description: Agent without explicit skills" \
"# Plain Agent

This agent intentionally has no machine-readable skill bindings."

write_agent_markdown "$WORK_ROOT/paid-media/section-skill-agent.md" \
"name: Section Skill Agent
description: Agent with a markdown skill section" \
"# Section Skill Agent

General agent context.

## 专项技能

- 账户结构审计
- 追踪与归因审计
- 高管摘要生成

## 技术交付物

This section belongs to the agent prompt, not the extracted skill."

write_agent_markdown "$WORK_ROOT/marketing/skilled-agent.md" \
"name: Skilled Agent
description: Agent with explicit skills
skills:
  - id: skill.market-research
    name: Market Research
    description: Research market signals and summarize evidence.
    level: senior
    content: |
      # Market Research

      Use primary evidence and list assumptions." \
"# Skilled Agent

This agent has an explicit skill definition in frontmatter."

write_agent_markdown "$WORK_ROOT/integrations/ignored-agent.md" \
"name: Ignored Integration Agent
description: Non-agent integration documentation should not be imported" \
"# Ignored Integration Agent

This file is documentation under an ignored root directory."

script_leaf="$(basename "$SCRIPT_PATH")"
if [[ "$script_leaf" == "build-agency-agents-import.sh" ]]; then
  output_json="$(bash "$SCRIPT_PATH" --source-root "$WORK_ROOT" --output-zip "$OUT_ROOT/agency-test.zip")"
else
  output_json="$(bash "$SCRIPT_PATH" --source-dir "$WORK_ROOT" --output-dir "$OUT_ROOT" --output-name "agency-test.zip")"
fi

python3 - "$output_json" "$EXTRACT_ROOT" <<'PY'
import json
import sys
import zipfile
from pathlib import Path

data = json.loads(sys.argv[1])
extract_root = Path(sys.argv[2])
zip_path = Path(data.get("OutputZip") or data.get("output_zip"))
agent_count = data.get("Count", data.get("count"))
skill_count = data.get("SkillCount", data.get("skill_count"))

if not zip_path.is_file():
    raise SystemExit(f"Expected output zip at {zip_path}")

with zipfile.ZipFile(zip_path) as archive:
    archive.extractall(extract_root)

def read_json(relative):
    return json.loads((extract_root / relative).read_text(encoding="utf-8"))

plain = read_json("agents/plain-agent.json")
if plain.get("skill_refs"):
    raise SystemExit("Agent without explicit skills must not get default skill_refs.")

section = read_json("agents/section-skill-agent.json")
if section.get("skill_refs"):
    raise SystemExit("Markdown skill-like sections must not become skill_refs.")
if "## 专项技能" not in section["source_prompt"]:
    raise SystemExit("Markdown skill-like section heading should remain in source_prompt.")
if "账户结构审计" not in section["persona"]["backstory"]:
    raise SystemExit("Markdown skill-like section content should remain in persona.backstory.")
if (extract_root / "skills/skill-section-skill-agent-specialized-skills.json").exists():
    raise SystemExit("Markdown skill-like sections must not create skill definition files.")

skilled = read_json("agents/skilled-agent.json")
if len(skilled.get("skill_refs", [])) != 1:
    raise SystemExit("Agent with explicit skills should get one skill_ref.")
if skilled["skill_refs"][0]["skill_id"] != "skill.market-research":
    raise SystemExit("Skill ref should use explicit skill id.")
if skilled["skill_refs"][0]["level"] != "senior":
    raise SystemExit("Skill ref should preserve explicit level.")

skill_path = extract_root / "skills/skill-market-research.json"
if not skill_path.is_file():
    raise SystemExit(f"Expected explicit skill definition file {skill_path}.")
skill_raw = skill_path.read_text(encoding="utf-8")
skill = json.loads(skill_raw)
if skill["skill_id"] != "skill.market-research":
    raise SystemExit("Skill definition should preserve explicit skill id.")
if not isinstance(skill.get("domain_tags"), list):
    raise SystemExit("Explicit skill domain_tags must be serialized as a JSON array.")
if not isinstance(skill.get("task_type_tags"), list):
    raise SystemExit("Explicit skill task_type_tags must be serialized as a JSON array.")
if len(skill.get("prompt_assets", [])) != 1:
    raise SystemExit("Skill definition should contain one prompt asset.")
if skill["prompt_assets"][0]["path"] != "SKILL.md":
    raise SystemExit("Prompt asset should materialize as SKILL.md.")
if skill["prompt_assets"][0]["kind"] != "markdown":
    raise SystemExit("Prompt asset kind should be markdown.")
if "Use primary evidence" not in skill["prompt_assets"][0]["content"]:
    raise SystemExit("Prompt asset should contain explicit skill content.")

manifest = read_json("manifest.json")
if (extract_root / "agents/ignored-agent.json").exists():
    raise SystemExit("Markdown under root integrations directory should be ignored.")
if manifest["count"] != 3:
    raise SystemExit("Manifest count should equal imported agent count.")
if agent_count != 3:
    raise SystemExit("Script JSON count should equal imported agent count.")
if skill_count != 1:
    raise SystemExit("Script JSON skill_count should include only explicit frontmatter skills.")

print(json.dumps({"status": "passed", "output_zip": str(zip_path), "agents": agent_count, "skills": skill_count}, ensure_ascii=False, indent=2))
PY
