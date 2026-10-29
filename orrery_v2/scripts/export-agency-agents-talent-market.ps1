param(
  [string]$SourceDir,
  [string]$OutputDir,
  [string]$OutputName = "agency-agents-zh-orrery-talent-market.zip"
)

$ErrorActionPreference = "Stop"

if ($PSVersionTable.PSVersion.Major -lt 7) {
  $pwsh = Get-Command pwsh -ErrorAction SilentlyContinue
  if ($pwsh) {
    $forwardArgs = @("-NoProfile", "-File", $PSCommandPath)
    if (-not [string]::IsNullOrWhiteSpace($SourceDir)) {
      $forwardArgs += @("-SourceDir", $SourceDir)
    }
    if (-not [string]::IsNullOrWhiteSpace($OutputDir)) {
      $forwardArgs += @("-OutputDir", $OutputDir)
    }
    if (-not [string]::IsNullOrWhiteSpace($OutputName)) {
      $forwardArgs += @("-OutputName", $OutputName)
    }
    & $pwsh.Source @forwardArgs
    exit $LASTEXITCODE
  }
}

function Resolve-DefaultSourceDir {
  $candidate = Join-Path $PSScriptRoot "..\..\..\_refs\agency-agents-zh"
  if (Test-Path -LiteralPath $candidate) {
    return (Resolve-Path -LiteralPath $candidate).Path
  }
  throw "SourceDir is required. Clone jnMetaCode/agency-agents-zh first or pass -SourceDir."
}

function Convert-ToSlug([string]$Value) {
  $slug = $Value.Trim().ToLowerInvariant()
  $slug = [regex]::Replace($slug, "[^a-z0-9\p{IsCJKUnifiedIdeographs}]+", "-")
  $slug = $slug.Trim("-")
  if ([string]::IsNullOrWhiteSpace($slug)) {
    return "agent"
  }
  return $slug
}

function Convert-ToAsciiSlug {
  param(
    [string]$Value,
    [string]$Fallback = "item"
  )
  $slug = $Value.Trim().ToLowerInvariant()
  $slug = [regex]::Replace($slug, "[^a-z0-9]+", "-")
  $slug = $slug.Trim("-")
  if ([string]::IsNullOrWhiteSpace($slug)) {
    return $Fallback
  }
  return $slug
}

function Get-FrontmatterValue([string]$Frontmatter, [string]$Key) {
  $pattern = "(?m)^" + [regex]::Escape($Key) + ":\s*(?<value>.+?)\s*$"
  $match = [regex]::Match($Frontmatter, $pattern)
  if (-not $match.Success) {
    return ""
  }
  return $match.Groups["value"].Value.Trim().Trim('"').Trim("'")
}

function Convert-FrontmatterScalar([string]$Value) {
  $value = $Value.Trim()
  if (($value.StartsWith('"') -and $value.EndsWith('"')) -or ($value.StartsWith("'") -and $value.EndsWith("'"))) {
    return $value.Substring(1, $value.Length - 2)
  }
  return $value
}

function Get-FrontmatterBlock([string]$Frontmatter, [string[]]$Keys) {
  $keySet = @{}
  foreach ($key in $Keys) {
    $keySet[$key] = $true
  }
  $lines = $Frontmatter -split "`r?`n"
  $capture = $false
  $captured = New-Object System.Collections.Generic.List[string]
  foreach ($line in $lines) {
    $topLevel = [regex]::Match($line, "^(?<key>[A-Za-z0-9_-]+):\s*(?<value>.*)$")
    if ($topLevel.Success) {
      if ($capture) {
        break
      }
      $key = $topLevel.Groups["key"].Value
      if ($keySet.ContainsKey($key)) {
        $capture = $true
        $value = $topLevel.Groups["value"].Value
        if (-not [string]::IsNullOrWhiteSpace($value)) {
          $captured.Add($value)
        }
      }
      continue
    }
    if ($capture) {
      $captured.Add($line)
    }
  }
  return $captured.ToArray()
}

function ConvertTo-StringList([object]$Value) {
  if ($null -eq $Value) {
    return @()
  }
  if ($Value -is [System.Array]) {
    $items = $Value
  } else {
    $items = ([string]$Value) -split ","
  }
  $out = New-Object System.Collections.Generic.List[string]
  foreach ($item in $items) {
    $text = ([string]$item).Trim()
    if (-not [string]::IsNullOrWhiteSpace($text)) {
      $out.Add($text)
    }
  }
  return $out.ToArray()
}

function Get-MapValue($Map, [string[]]$Keys) {
  foreach ($key in $Keys) {
    if ($Map.Contains($key) -and -not [string]::IsNullOrWhiteSpace([string]$Map[$key])) {
      return [string]$Map[$key]
    }
  }
  return ""
}

function Set-MapValue($Map, [string]$Key, [string]$Value) {
  $Map[$Key] = Convert-FrontmatterScalar $Value
}

function Read-IndentedBlock([string[]]$Lines, [int]$StartIndex, [int]$ParentIndent, [ref]$NextIndex) {
  $block = New-Object System.Collections.Generic.List[string]
  $i = $StartIndex
  while ($i -lt $Lines.Count) {
    $line = $Lines[$i]
    if ($line -match "^\s*-\s+") {
      break
    }
    $property = [regex]::Match($line, "^(?<indent>\s+)(?<key>[A-Za-z0-9_-]+):\s*")
    if ($property.Success -and $property.Groups["indent"].Value.Length -le $ParentIndent) {
      break
    }
    if ([string]::IsNullOrWhiteSpace($line)) {
      $block.Add("")
    } else {
      $trimWidth = [Math]::Min($line.Length, $ParentIndent + 2)
      $block.Add($line.Substring($trimWidth))
    }
    $i++
  }
  $NextIndex.Value = $i
  return ($block -join "`n").Trim()
}

function Parse-FrontmatterListOfMaps([string[]]$Lines) {
  $items = New-Object System.Collections.Generic.List[object]
  if ($Lines.Count -eq 0) {
    return @()
  }
  if ($Lines.Count -eq 1 -and -not ($Lines[0] -match "^\s*-")) {
    $scalar = Convert-FrontmatterScalar $Lines[0]
    if ([string]::IsNullOrWhiteSpace($scalar)) {
      return @()
    }
    foreach ($item in ($scalar -split ",")) {
      $id = $item.Trim()
      if (-not [string]::IsNullOrWhiteSpace($id)) {
        $items.Add([ordered]@{ id = $id })
      }
    }
    return $items.ToArray()
  }
  $i = 0
  while ($i -lt $Lines.Count) {
    $line = $Lines[$i]
    $itemStart = [regex]::Match($line, "^\s*-\s*(?<rest>.*)$")
    if (-not $itemStart.Success) {
      $i++
      continue
    }
    $item = [ordered]@{}
    $rest = $itemStart.Groups["rest"].Value.Trim()
    if (-not [string]::IsNullOrWhiteSpace($rest)) {
      $inlineField = [regex]::Match($rest, "^(?<key>[A-Za-z0-9_-]+):\s*(?<value>.*)$")
      if ($inlineField.Success) {
        Set-MapValue $item $inlineField.Groups["key"].Value $inlineField.Groups["value"].Value
      } else {
        $item["id"] = Convert-FrontmatterScalar $rest
      }
    }
    $i++
    while ($i -lt $Lines.Count) {
      $line = $Lines[$i]
      if ($line -match "^\s*-\s+") {
        break
      }
      $field = [regex]::Match($line, "^(?<indent>\s+)(?<key>[A-Za-z0-9_-]+):\s*(?<value>.*)$")
      if (-not $field.Success) {
        $i++
        continue
      }
      $indent = $field.Groups["indent"].Value.Length
      $key = $field.Groups["key"].Value
      $value = $field.Groups["value"].Value.Trim()
      if ($value -eq "|" -or $value -eq ">") {
        $next = 0
        $block = Read-IndentedBlock $Lines ($i + 1) $indent ([ref]$next)
        $item[$key] = $block
        $i = $next
        continue
      }
      Set-MapValue $item $key $value
      $i++
    }
    if ($item.Count -gt 0) {
      $items.Add($item)
    }
  }
  return $items.ToArray()
}

function Get-ExplicitSkillData([string]$Frontmatter, [string]$AgentSlug, [string]$SourcePath, [string]$Category) {
  $lines = Get-FrontmatterBlock $Frontmatter @("skills", "skill_refs")
  $items = Parse-FrontmatterListOfMaps $lines
  $skillRefs = New-Object System.Collections.Generic.List[object]
  $skillDefinitions = New-Object System.Collections.Generic.List[object]
  foreach ($item in $items) {
    $skillID = (Get-MapValue $item @("skill_id", "skillId", "id")).Trim()
    $name = (Get-MapValue $item @("name", "alias")).Trim()
    if ([string]::IsNullOrWhiteSpace($skillID)) {
      if ([string]::IsNullOrWhiteSpace($name)) {
        continue
      }
      $skillID = "skill.$AgentSlug.$(Convert-ToSlug $name)"
    }
    $alias = (Get-MapValue $item @("alias", "name")).Trim()
    if ([string]::IsNullOrWhiteSpace($alias)) {
      $alias = $skillID
    }
    $versionConstraint = (Get-MapValue $item @("version_constraint", "versionConstraint", "constraint")).Trim()
    $level = (Get-MapValue $item @("level")).Trim()
    $evidenceRefs = ConvertTo-StringList (Get-MapValue $item @("evidence_refs", "evidenceRefs"))
    $skillRefs.Add([ordered]@{
      skill_id = $skillID
      version_constraint = $versionConstraint
      alias = $alias
      level = $level
      evidence_refs = $evidenceRefs
    })

    $content = (Get-MapValue $item @("content", "prompt", "skill_md", "skillMd")).Trim()
    $description = (Get-MapValue $item @("description")).Trim()
    $version = (Get-MapValue $item @("version")).Trim()
    if ([string]::IsNullOrWhiteSpace($version)) {
      $version = "1.0.0"
    }
    $hasDefinition = -not [string]::IsNullOrWhiteSpace($content) -or -not [string]::IsNullOrWhiteSpace($description) -or -not [string]::IsNullOrWhiteSpace($name)
    if (-not $hasDefinition) {
      continue
    }
    if ([string]::IsNullOrWhiteSpace($name)) {
      $name = $alias
    }
    if ([string]::IsNullOrWhiteSpace($description)) {
      $description = $name
    }
    if ([string]::IsNullOrWhiteSpace($content)) {
      $content = "# $name`n`n$description"
    }
    $skillDefinitions.Add([ordered]@{
      skill_id = $skillID
      name = $name
      version = $version
      description = $description
      domain_tags = @(ConvertTo-StringList (Get-MapValue $item @("domain_tags", "domainTags", "domains", "tags")))
      task_type_tags = @(ConvertTo-StringList (Get-MapValue $item @("task_type_tags", "taskTypeTags", "task_types")))
      prompt_assets = @(
        [ordered]@{
          path = "SKILL.md"
          content_ref = ""
          content = $content
          kind = "markdown"
        }
      )
      required_tools = @()
      evidence_refs = $evidenceRefs
      enabled = $true
      source_ref = $SourcePath
    })
  }
  return [pscustomobject]@{
    Refs = $skillRefs.ToArray()
    Definitions = $skillDefinitions.ToArray()
  }
}

function Get-MarkdownSection([string]$Body, [string[]]$Names) {
  $lines = $Body -split "`r?`n"
  $capture = $false
  $captured = New-Object System.Collections.Generic.List[string]
  foreach ($line in $lines) {
    if ($line -match "^\s*#{2,}\s*(?<title>.+?)\s*$") {
      if ($capture) {
        break
      }
      $title = $matches.title
      foreach ($name in $Names) {
        if ($title -like "*$name*") {
          $capture = $true
          break
        }
      }
      continue
    }
    if ($capture) {
      $captured.Add($line)
    }
  }
  $text = ($captured -join "`n").Trim()
  if ($text.Length -gt 900) {
    return $text.Substring(0, 900).Trim()
  }
  return $text
}

function Get-ContentHash([string]$Content) {
  $sha = [System.Security.Cryptography.SHA256]::Create()
  try {
    $bytes = [System.Text.Encoding]::UTF8.GetBytes($Content)
    $hash = $sha.ComputeHash($bytes)
    return "sha256:" + ([System.BitConverter]::ToString($hash).Replace("-", "").ToLowerInvariant())
  } finally {
    $sha.Dispose()
  }
}

function Get-RelativePathCompat([string]$Root, [string]$Path) {
  $rootFull = [System.IO.Path]::GetFullPath($Root).TrimEnd("\", "/") + [System.IO.Path]::DirectorySeparatorChar
  $pathFull = [System.IO.Path]::GetFullPath($Path)
  $rootUri = [System.Uri]::new($rootFull)
  $pathUri = [System.Uri]::new($pathFull)
  return [System.Uri]::UnescapeDataString($rootUri.MakeRelativeUri($pathUri).ToString()).Replace("/", [System.IO.Path]::DirectorySeparatorChar)
}

function New-CandidateFromMarkdown($File, [string]$Root) {
  $content = Get-Content -LiteralPath $File.FullName -Raw -Encoding UTF8
  $match = [regex]::Match($content, "(?s)^---\r?\n(?<frontmatter>.*?)\r?\n---\r?\n(?<body>.*)$")
  if (-not $match.Success) {
    return $null
  }
  $frontmatter = $match.Groups["frontmatter"].Value
  $body = $match.Groups["body"].Value.Trim()
  $name = Get-FrontmatterValue $frontmatter "name"
  $description = Get-FrontmatterValue $frontmatter "description"
  if ([string]::IsNullOrWhiteSpace($name) -or [string]::IsNullOrWhiteSpace($description)) {
    return $null
  }
  $relative = (Get-RelativePathCompat $Root $File.FullName).Replace("\", "/")
  $parts = $relative -split "/"
  $category = if ($parts.Length -gt 1) { $parts[0] } else { "" }
  $fileStem = [System.IO.Path]::GetFileNameWithoutExtension($File.Name)
  $slug = Convert-ToSlug $fileStem
  $roleName = $description
  if ($roleName.Length -gt 120) {
    $roleName = $roleName.Substring(0, 120).Trim()
  }
  $responsibility = "Responsible for $description. Submit process, result, and evidence according to BaseAgent audit requirements."
  $skillData = Get-ExplicitSkillData $frontmatter $slug $relative $category
  $skillRefs = @($skillData.Refs)
  $skillDefinitions = @($skillData.Definitions)
  $candidate = [ordered]@{
    agent_profile_id = "profile-market-$slug"
    real_name = $name
    display_name = $name
    role_name = $roleName
    agent_kind = "ai"
    description = $description
    category = $category
    source_slug = $slug
    source_path = $relative
    source_hash = Get-ContentHash $content
    source_prompt = $content
    runtime_kind = "managed"
    persona = [ordered]@{
      backstory = $body
      responsibility_boundary = $responsibility
      behavior_boundary = Get-MarkdownSection $body @("Rules", "Guidelines", "Checklist", "Principles")
    }
    personal_memory_ref = [ordered]@{
      memory_profile_id = "memory-$slug"
      memory_kind = "personal"
      access_scope = "agent"
    }
    collective_memory_refs = @()
    skill_refs = $skillRefs
    tool_refs = @()
    permission_policy_ref = "policy.agent.default"
    context_injection_policy = "task-scoped"
    summary_contract_ref = "summary.agent.default"
    deliverable_contract_ref = "deliverable.task.default"
    evidence_contract_ref = "evidence.task.required"
    qa_contract_ref = "qa.agent.standard"
    qa_enabled = $true
    enabled = $true
  }
  return [pscustomobject]@{
    Candidate = $candidate
    Skills = $skillDefinitions
  }
}

if ([string]::IsNullOrWhiteSpace($SourceDir)) {
  $SourceDir = Resolve-DefaultSourceDir
} else {
  $SourceDir = (Resolve-Path -LiteralPath $SourceDir).Path
}
if ([string]::IsNullOrWhiteSpace($OutputDir)) {
  $OutputDir = Join-Path $PSScriptRoot "..\data\imports"
}
if ([System.IO.Path]::IsPathRooted($OutputDir)) {
  $OutputDir = [System.IO.Path]::GetFullPath($OutputDir)
} else {
  $OutputDir = [System.IO.Path]::GetFullPath((Join-Path (Get-Location).Path $OutputDir))
}
New-Item -ItemType Directory -Force -Path $OutputDir | Out-Null

$ignoredNames = @("README.md", "README.zh-TW.md", "CATALOG.md", "AGENT-LIST.md", "UPSTREAM.md", "CONTRIBUTING.md", "LICENSE.md")
$ignoredDirs = "\\(\.github|examples|strategy|integrations|assets|scripts)(\\|$)"
$files = Get-ChildItem -LiteralPath $SourceDir -Recurse -File -Filter "*.md" |
  Where-Object { $_.FullName -notmatch $ignoredDirs -and $_.Name -notin $ignoredNames } |
  Sort-Object FullName

$seenIDs = @{}
$seenSkillIDs = @{}
$agents = New-Object System.Collections.Generic.List[object]
$skills = New-Object System.Collections.Generic.List[object]
foreach ($file in $files) {
  $importItem = New-CandidateFromMarkdown $file $SourceDir
  if ($null -eq $importItem) {
    continue
  }
  $candidate = $importItem.Candidate
  $baseID = $candidate.agent_profile_id
  $dedupe = 2
  while ($seenIDs.ContainsKey($candidate.agent_profile_id)) {
    $candidate.agent_profile_id = "$baseID-$dedupe"
    $dedupe++
  }
  $seenIDs[$candidate.agent_profile_id] = $true
  $agents.Add($candidate)
  foreach ($skill in @($importItem.Skills)) {
    if ($null -eq $skill -or [string]::IsNullOrWhiteSpace([string]$skill.skill_id)) {
      continue
    }
    if ($seenSkillIDs.ContainsKey($skill.skill_id)) {
      continue
    }
    $seenSkillIDs[$skill.skill_id] = $true
    $skills.Add($skill)
  }
}

if ($agents.Count -eq 0) {
  throw "No importable agents found in $SourceDir"
}

$tempRoot = Join-Path ([System.IO.Path]::GetTempPath()) ("orrery-agency-agents-" + [guid]::NewGuid().ToString("N"))
$agentsDir = Join-Path $tempRoot "agents"
New-Item -ItemType Directory -Force -Path $agentsDir | Out-Null
if ($skills.Count -gt 0) {
  $skillsDir = Join-Path $tempRoot "skills"
  New-Item -ItemType Directory -Force -Path $skillsDir | Out-Null
}

$manifest = [ordered]@{
  format = "orrery.talent-market"
  version = 1
  count = $agents.Count
  exported_at = (Get-Date).ToUniversalTime().ToString("o")
  source_app = "jnMetaCode/agency-agents-zh"
  description = "Converted from jnMetaCode/agency-agents-zh markdown agents for Orrery Talent Market."
}
$manifest | ConvertTo-Json -Depth 20 | Set-Content -LiteralPath (Join-Path $tempRoot "manifest.json") -Encoding UTF8

foreach ($agent in $agents) {
  $stem = Convert-ToSlug $agent.source_slug
  $path = Join-Path $agentsDir "$stem.json"
  $agent | ConvertTo-Json -Depth 30 | Set-Content -LiteralPath $path -Encoding UTF8
}
foreach ($skill in $skills) {
  $stem = Convert-ToAsciiSlug -Value $skill.skill_id -Fallback "skill"
  $path = Join-Path $skillsDir "$stem.json"
  $skill | ConvertTo-Json -Depth 30 | Set-Content -LiteralPath $path -Encoding UTF8
}

$zipPath = Join-Path $OutputDir $OutputName
if (Test-Path -LiteralPath $zipPath) {
  Remove-Item -LiteralPath $zipPath -Force
}
Add-Type -AssemblyName System.IO.Compression.FileSystem
[System.IO.Compression.ZipFile]::CreateFromDirectory($tempRoot, $zipPath, [System.IO.Compression.CompressionLevel]::Optimal, $false)
Remove-Item -LiteralPath $tempRoot -Recurse -Force

[ordered]@{
  source_dir = $SourceDir
  output_zip = $zipPath
  count = $agents.Count
  skill_count = $skills.Count
} | ConvertTo-Json -Depth 5
