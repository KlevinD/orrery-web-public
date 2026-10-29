param(
  [string]$SourceRoot = "D:\Python\projects\_refs\agency-agents-zh",
  [string]$OutputZip = ""
)

$ErrorActionPreference = "Stop"

if ([string]::IsNullOrWhiteSpace($OutputZip)) {
  $repoRoot = Resolve-Path (Join-Path $PSScriptRoot "..")
  $OutputZip = Join-Path $repoRoot "data\imports\agency-agents-zh-orrery-talent-market.zip"
}

function ConvertTo-SafeStem {
  param([string]$Value)
  $stem = $Value.Trim().ToLowerInvariant() -replace '\.md$', ''
  $stem = $stem -replace '[^a-z0-9\p{IsCJKUnifiedIdeographs}]+', '-'
  $stem = $stem.Trim('-')
  if ([string]::IsNullOrWhiteSpace($stem)) {
    return "agent"
  }
  return $stem
}

function ConvertTo-SafeAsciiStem {
  param(
    [string]$Value,
    [string]$Fallback = "item"
  )
  $stem = $Value.Trim().ToLowerInvariant() -replace '\.md$', ''
  $stem = $stem -replace '[^a-z0-9]+', '-'
  $stem = $stem.Trim('-')
  if ([string]::IsNullOrWhiteSpace($stem)) {
    return $Fallback
  }
  return $stem
}

function Get-Sha256Text {
  param([string]$Value)
  $sha = [System.Security.Cryptography.SHA256]::Create()
  try {
    $bytes = [System.Text.Encoding]::UTF8.GetBytes($Value)
    $hash = $sha.ComputeHash($bytes)
    return "sha256:" + (($hash | ForEach-Object { $_.ToString("x2") }) -join "")
  } finally {
    $sha.Dispose()
  }
}

function Parse-Frontmatter {
  param([string]$Content)
  $match = [regex]::Match($Content, '(?s)^---\s*\r?\n(.*?)\r?\n---\s*\r?\n?(.*)$')
  if (-not $match.Success) {
    return $null
  }

  $fields = @{}
  foreach ($line in ($match.Groups[1].Value -split "\r?\n")) {
    $field = [regex]::Match($line, '^\s*([A-Za-z0-9_-]+):\s*(.*?)\s*$')
    if (-not $field.Success) {
      continue
    }
    $key = $field.Groups[1].Value.Trim()
    $value = $field.Groups[2].Value.Trim()
    if (($value.StartsWith('"') -and $value.EndsWith('"')) -or ($value.StartsWith("'") -and $value.EndsWith("'"))) {
      $value = $value.Substring(1, $value.Length - 2)
    }
    $fields[$key] = $value
  }

  return [pscustomobject]@{
    Fields = $fields
    Body = $match.Groups[2].Value.Trim()
  }
}

function Get-MarkdownSection {
  param(
    [string]$Body,
    [string[]]$Names
  )
  $collecting = $false
  $lines = New-Object System.Collections.Generic.List[string]
  foreach ($line in ($Body -split "\r?\n")) {
    $header = [regex]::Match($line.Trim(), '^#{2,4}\s+(.+)$')
    if ($header.Success) {
      if ($collecting) {
        break
      }
      foreach ($name in $Names) {
        if ($header.Groups[1].Value.ToLowerInvariant().Contains($name.ToLowerInvariant())) {
          $collecting = $true
          break
        }
      }
      continue
    }
    if ($collecting) {
      $lines.Add($line)
    }
  }
  $section = ($lines -join "`n").Trim()
  if ($section.Length -gt 800) {
    return $section.Substring(0, 800)
  }
  return $section
}

function Convert-FrontmatterScalar {
  param([string]$Value)
  $value = $Value.Trim()
  if (($value.StartsWith('"') -and $value.EndsWith('"')) -or ($value.StartsWith("'") -and $value.EndsWith("'"))) {
    return $value.Substring(1, $value.Length - 2)
  }
  return $value
}

function Get-FrontmatterBlock {
  param(
    [string]$Frontmatter,
    [string[]]$Keys
  )
  $keySet = @{}
  foreach ($key in $Keys) {
    $keySet[$key] = $true
  }
  $lines = $Frontmatter -split "\r?\n"
  $capture = $false
  $captured = New-Object System.Collections.Generic.List[string]
  foreach ($line in $lines) {
    $topLevel = [regex]::Match($line, '^(?<key>[A-Za-z0-9_-]+):\s*(?<value>.*)$')
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

function ConvertTo-StringList {
  param([object]$Value)
  if ($null -eq $Value) {
    return @()
  }
  if ($Value -is [System.Array]) {
    $items = $Value
  } else {
    $items = ([string]$Value) -split ','
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

function Get-MapValue {
  param(
    $Map,
    [string[]]$Keys
  )
  foreach ($key in $Keys) {
    if ($Map.Contains($key) -and -not [string]::IsNullOrWhiteSpace([string]$Map[$key])) {
      return [string]$Map[$key]
    }
  }
  return ""
}

function Set-MapValue {
  param(
    $Map,
    [string]$Key,
    [string]$Value
  )
  $Map[$Key] = Convert-FrontmatterScalar $Value
}

function Read-IndentedBlock {
  param(
    [string[]]$Lines,
    [int]$StartIndex,
    [int]$ParentIndent,
    [ref]$NextIndex
  )
  $block = New-Object System.Collections.Generic.List[string]
  $i = $StartIndex
  while ($i -lt $Lines.Count) {
    $line = $Lines[$i]
    if ($line -match '^\s*-\s+') {
      break
    }
    $property = [regex]::Match($line, '^(?<indent>\s+)(?<key>[A-Za-z0-9_-]+):\s*')
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

function Parse-FrontmatterListOfMaps {
  param([string[]]$Lines)
  $items = New-Object System.Collections.Generic.List[object]
  if ($Lines.Count -eq 0) {
    return @()
  }
  if ($Lines.Count -eq 1 -and -not ($Lines[0] -match '^\s*-')) {
    $scalar = Convert-FrontmatterScalar $Lines[0]
    if ([string]::IsNullOrWhiteSpace($scalar)) {
      return @()
    }
    foreach ($item in ($scalar -split ',')) {
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
    $itemStart = [regex]::Match($line, '^\s*-\s*(?<rest>.*)$')
    if (-not $itemStart.Success) {
      $i++
      continue
    }
    $item = [ordered]@{}
    $rest = $itemStart.Groups["rest"].Value.Trim()
    if (-not [string]::IsNullOrWhiteSpace($rest)) {
      $inlineField = [regex]::Match($rest, '^(?<key>[A-Za-z0-9_-]+):\s*(?<value>.*)$')
      if ($inlineField.Success) {
        Set-MapValue $item $inlineField.Groups["key"].Value $inlineField.Groups["value"].Value
      } else {
        $item["id"] = Convert-FrontmatterScalar $rest
      }
    }
    $i++
    while ($i -lt $Lines.Count) {
      $line = $Lines[$i]
      if ($line -match '^\s*-\s+') {
        break
      }
      $field = [regex]::Match($line, '^(?<indent>\s+)(?<key>[A-Za-z0-9_-]+):\s*(?<value>.*)$')
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

function Get-ExplicitSkillData {
  param(
    [string]$Frontmatter,
    [string]$AgentSlug,
    [string]$SourcePath
  )
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
      $skillID = "skill.$AgentSlug.$(ConvertTo-SafeStem $name)"
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

function Should-SkipMarkdown {
  param([string]$RelativePath)
  $name = [System.IO.Path]::GetFileName($RelativePath).ToLowerInvariant()
  if (@("readme.md", "catalog.md", "agent-list.md", "upstream.md", "contributing.md", "license.md") -contains $name) {
    return $true
  }
  $normalized = $RelativePath.Replace('\', '/').ToLowerInvariant()
  return $normalized.StartsWith('examples/') -or $normalized.Contains('/examples/') -or
    $normalized.StartsWith('strategy/') -or $normalized.Contains('/strategy/') -or
    $normalized.StartsWith('integrations/') -or $normalized.Contains('/integrations/') -or
    $normalized.StartsWith('assets/') -or $normalized.Contains('/assets/') -or
    $normalized.StartsWith('scripts/') -or $normalized.Contains('/scripts/')
}

$source = Resolve-Path $SourceRoot
$outputFullPath = [System.IO.Path]::GetFullPath($OutputZip)
$outputDir = [System.IO.Path]::GetDirectoryName($outputFullPath)
New-Item -ItemType Directory -Force -Path $outputDir | Out-Null

$tempBase = [System.IO.Path]::GetTempPath()
$tempRoot = Join-Path $tempBase ("orrery-agency-import-" + [guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Force -Path $tempRoot | Out-Null
New-Item -ItemType Directory -Force -Path (Join-Path $tempRoot "agents") | Out-Null

try {
  $agents = New-Object System.Collections.Generic.List[object]
  $skills = New-Object System.Collections.Generic.List[object]
  $seenSkillIDs = @{}
  $files = Get-ChildItem -Path $source -Recurse -Filter *.md | Sort-Object FullName
  foreach ($file in $files) {
    $relative = [System.IO.Path]::GetRelativePath($source, $file.FullName).Replace('\', '/')
    if (Should-SkipMarkdown $relative) {
      continue
    }

    $content = [System.IO.File]::ReadAllText($file.FullName)
    $frontmatter = [regex]::Match($content, '(?s)^---\s*\r?\n(.*?)\r?\n---\s*\r?\n?(.*)$').Groups[1].Value
    $parsed = Parse-Frontmatter $content
    if ($null -eq $parsed) {
      continue
    }

    $name = [string]$parsed.Fields["name"]
    $description = [string]$parsed.Fields["description"]
    if ([string]::IsNullOrWhiteSpace($name) -or [string]::IsNullOrWhiteSpace($description)) {
      continue
    }

    $slug = ConvertTo-SafeStem ([System.IO.Path]::GetFileNameWithoutExtension($file.Name))
    $category = ($relative -split '/')[0]
    $body = [string]$parsed.Body
    $skillData = Get-ExplicitSkillData $frontmatter $slug $relative
    $skillRefs = @($skillData.Refs)
    $skillDefinitions = @($skillData.Definitions)
    $candidate = [ordered]@{
      agent_profile_id = "profile-market-$slug"
      real_name = $name.Trim()
      display_name = $name.Trim()
      role_name = $description.Trim()
      agent_kind = "ai"
      description = $description.Trim()
      category = $category
      source_slug = $slug
      source_path = $relative
      source_hash = Get-Sha256Text $content
      source_prompt = $content.Trim()
      runtime_kind = "managed"
      persona = [ordered]@{
        backstory = $(if ([string]::IsNullOrWhiteSpace($body)) { $description.Trim() } else { $body })
        responsibility_boundary = "负责$($description.Trim())，并按 BaseAgent 审计要求提交过程、结果和证据。"
        behavior_boundary = Get-MarkdownSection $body @("规则", "原则", "Rules", "Guidelines")
      }
      skill_refs = $skillRefs
      tool_refs = @()
      qa_enabled = $true
      enabled = $true
    }

    $agents.Add([pscustomobject]$candidate)
    foreach ($skill in @($skillDefinitions)) {
      if ($null -eq $skill -or [string]::IsNullOrWhiteSpace([string]$skill.skill_id)) {
        continue
      }
      if ($seenSkillIDs.ContainsKey($skill.skill_id)) {
        continue
      }
      $seenSkillIDs[$skill.skill_id] = $true
      $skills.Add($skill)
    }
    $agentJson = $candidate | ConvertTo-Json -Depth 30
    [System.IO.File]::WriteAllText((Join-Path $tempRoot "agents\$slug.json"), $agentJson, [System.Text.UTF8Encoding]::new($false))
  }

  if ($skills.Count -gt 0) {
    $skillsDir = Join-Path $tempRoot "skills"
    New-Item -ItemType Directory -Force -Path $skillsDir | Out-Null
    foreach ($skill in $skills) {
      $stem = ConvertTo-SafeAsciiStem -Value $skill.skill_id -Fallback "skill"
      $skillJson = $skill | ConvertTo-Json -Depth 30
      [System.IO.File]::WriteAllText((Join-Path $skillsDir "$stem.json"), $skillJson, [System.Text.UTF8Encoding]::new($false))
    }
  }

  $manifest = [ordered]@{
    format = "orrery.talent-market"
    version = 1
    count = $agents.Count
    exported_at = [DateTime]::UtcNow.ToString("o")
    source_app = "agency-agents-zh"
    description = "All agency-agents-zh markdown agents converted to Orrery Talent Market import candidates."
  }
  [System.IO.File]::WriteAllText((Join-Path $tempRoot "manifest.json"), ($manifest | ConvertTo-Json -Depth 10), [System.Text.UTF8Encoding]::new($false))

  if (Test-Path -LiteralPath $outputFullPath) {
    Remove-Item -LiteralPath $outputFullPath -Force
  }
  Add-Type -AssemblyName System.IO.Compression.FileSystem
  [System.IO.Compression.ZipFile]::CreateFromDirectory($tempRoot, $outputFullPath)

  [pscustomobject]@{
    SourceRoot = $source.Path
    OutputZip = $outputFullPath
    Count = $agents.Count
    SkillCount = $skills.Count
  } | ConvertTo-Json
} finally {
  $resolvedTemp = Resolve-Path -LiteralPath $tempRoot -ErrorAction SilentlyContinue
  if ($resolvedTemp -and $resolvedTemp.Path.StartsWith($tempBase, [System.StringComparison]::OrdinalIgnoreCase)) {
    Remove-Item -LiteralPath $resolvedTemp.Path -Recurse -Force
  }
}
