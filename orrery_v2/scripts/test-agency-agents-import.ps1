param(
  [string]$ScriptPath = (Join-Path $PSScriptRoot "export-agency-agents-talent-market.ps1")
)

$ErrorActionPreference = "Stop"

function New-TestRoot {
  param([string]$Name)
  return Join-Path ([System.IO.Path]::GetTempPath()) ("orrery-agency-import-test-$Name-" + [guid]::NewGuid().ToString("N"))
}

function Write-AgentMarkdown {
  param(
    [string]$Path,
    [string]$Frontmatter,
    [string]$Body
  )
  $directory = Split-Path -Parent $Path
  New-Item -ItemType Directory -Force -Path $directory | Out-Null
  @"
---
$Frontmatter
---

$Body
"@ | Set-Content -LiteralPath $Path -Encoding UTF8
}

function Expand-OutputZip {
  param([string]$ZipPath)
  $extract = New-TestRoot "extract"
  Expand-Archive -LiteralPath $ZipPath -DestinationPath $extract -Force
  return $extract
}

function Assert {
  param(
    [bool]$Condition,
    [string]$Message
  )
  if (-not $Condition) {
    throw $Message
  }
}

$workRoot = New-TestRoot "source"
$outRoot = New-TestRoot "output"
$extractRoots = New-Object System.Collections.Generic.List[string]

try {
  Write-AgentMarkdown `
    -Path (Join-Path $workRoot "engineering\plain-agent.md") `
    -Frontmatter @"
name: Plain Agent
description: Agent without explicit skills
"@ `
    -Body @"
# Plain Agent

This agent intentionally has no machine-readable skill bindings.
"@

  Write-AgentMarkdown `
    -Path (Join-Path $workRoot "paid-media\section-skill-agent.md") `
    -Frontmatter @"
name: Section Skill Agent
description: Agent with a markdown skill section
"@ `
    -Body @"
# Section Skill Agent

General agent context.

## 专项技能

- 账户结构审计
- 追踪与归因审计
- 高管摘要生成

## 技术交付物

This section belongs to the agent prompt, not the extracted skill.
"@

  Write-AgentMarkdown `
    -Path (Join-Path $workRoot "marketing\skilled-agent.md") `
    -Frontmatter @"
name: Skilled Agent
description: Agent with explicit skills
skills:
  - id: skill.market-research
    name: Market Research
    description: Research market signals and summarize evidence.
    level: senior
    content: |
      # Market Research

      Use primary evidence and list assumptions.
"@ `
    -Body @"
# Skilled Agent

This agent has an explicit skill definition in frontmatter.
"@

  Write-AgentMarkdown `
    -Path (Join-Path $workRoot "integrations\ignored-agent.md") `
    -Frontmatter @"
name: Ignored Integration Agent
description: Non-agent integration documentation should not be imported
"@ `
    -Body @"
# Ignored Integration Agent

This file is documentation under an ignored root directory.
"@

  $scriptLeaf = [System.IO.Path]::GetFileName($ScriptPath)
  if ($scriptLeaf -eq "build-agency-agents-import.ps1") {
    $outputZip = Join-Path $outRoot "agency-test.zip"
    $json = & $ScriptPath -SourceRoot $workRoot -OutputZip $outputZip | ConvertFrom-Json
    $zipPath = $json.OutputZip
    $agentCount = $json.Count
    $skillCount = $json.SkillCount
  } else {
    $json = & $ScriptPath -SourceDir $workRoot -OutputDir $outRoot -OutputName "agency-test.zip" | ConvertFrom-Json
    $zipPath = $json.output_zip
    $agentCount = $json.count
    $skillCount = $json.skill_count
  }
  Assert (Test-Path -LiteralPath $zipPath) "Expected output zip at $zipPath."

  $extract = Expand-OutputZip $zipPath
  $extractRoots.Add($extract)

  $plain = Get-Content -Raw -LiteralPath (Join-Path $extract "agents\plain-agent.json") | ConvertFrom-Json
  Assert (($null -eq $plain.skill_refs) -or ($plain.skill_refs.Count -eq 0)) "Agent without explicit skills must not get default skill_refs."

  $sectionSkillAgent = Get-Content -Raw -LiteralPath (Join-Path $extract "agents\section-skill-agent.json") | ConvertFrom-Json
  Assert (($null -eq $sectionSkillAgent.skill_refs) -or ($sectionSkillAgent.skill_refs.Count -eq 0)) "Markdown skill-like sections must remain in the prompt and must not become skill_refs."
  Assert ($sectionSkillAgent.source_prompt -match "## 专项技能") "Markdown skill-like section heading should remain in source_prompt."
  Assert ($sectionSkillAgent.persona.backstory -match "账户结构审计") "Markdown skill-like section content should remain in persona.backstory."
  Assert ($sectionSkillAgent.persona.backstory -match "## 技术交付物") "Following markdown sections should remain in persona.backstory."

  $sectionSkillPath = Join-Path $extract "skills\skill-section-skill-agent-specialized-skills.json"
  Assert (-not (Test-Path -LiteralPath $sectionSkillPath)) "Markdown skill-like sections must not create skill definition files."

  $skilled = Get-Content -Raw -LiteralPath (Join-Path $extract "agents\skilled-agent.json") | ConvertFrom-Json
  Assert ($skilled.skill_refs.Count -eq 1) "Agent with explicit skills should get one skill_ref."
  Assert ($skilled.skill_refs[0].skill_id -eq "skill.market-research") "Skill ref should use explicit skill id."
  Assert ($skilled.skill_refs[0].level -eq "senior") "Skill ref should preserve explicit level."

  $skillPath = Join-Path $extract "skills\skill-market-research.json"
  Assert (Test-Path -LiteralPath $skillPath) "Expected explicit skill definition file $skillPath."
  $skillRaw = Get-Content -Raw -LiteralPath $skillPath
  $skill = Get-Content -Raw -LiteralPath $skillPath | ConvertFrom-Json
  Assert ($skill.skill_id -eq "skill.market-research") "Skill definition should preserve explicit skill id."
  Assert ($skillRaw -match '"domain_tags"\s*:\s*\[') "Explicit skill domain_tags must be serialized as a JSON array."
  Assert ($skillRaw -match '"task_type_tags"\s*:\s*\[') "Explicit skill task_type_tags must be serialized as a JSON array."
  Assert ($skill.prompt_assets.Count -eq 1) "Skill definition should contain one prompt asset."
  Assert ($skill.prompt_assets[0].path -eq "SKILL.md") "Prompt asset should materialize as SKILL.md."
  Assert ($skill.prompt_assets[0].kind -eq "markdown") "Prompt asset kind should be markdown."
  Assert ($skill.prompt_assets[0].content -match "Use primary evidence") "Prompt asset should contain explicit skill content."

  $manifest = Get-Content -Raw -LiteralPath (Join-Path $extract "manifest.json") | ConvertFrom-Json
  Assert (-not (Test-Path -LiteralPath (Join-Path $extract "agents\ignored-agent.json"))) "Markdown under root integrations directory should be ignored."
  Assert ($manifest.count -eq 3) "Manifest count should equal imported agent count."
  Assert ($agentCount -eq 3) "Script JSON count should equal imported agent count."
  Assert ($skillCount -eq 1) "Script JSON skill_count should include only explicit frontmatter skills."

  [ordered]@{
    status = "passed"
    output_zip = $zipPath
    agents = $agentCount
    skills = $skillCount
  } | ConvertTo-Json -Depth 5
} finally {
  foreach ($extractRoot in $extractRoots) {
    Remove-Item -LiteralPath $extractRoot -Recurse -Force -ErrorAction SilentlyContinue
  }
  Remove-Item -LiteralPath $workRoot -Recurse -Force -ErrorAction SilentlyContinue
  Remove-Item -LiteralPath $outRoot -Recurse -Force -ErrorAction SilentlyContinue
}
