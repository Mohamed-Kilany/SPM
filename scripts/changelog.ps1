#Requires -Version 5.1
<#
.SYNOPSIS
Generates changelog markdown from first-parent git history.

.DESCRIPTION
Parses Conventional Commit subjects, groups entries by type, and adds the pull
request number plus every task ID found in the subject or body. Merge commits
and release commits are skipped. Dependabot updates are grouped under
Dependencies.

.PARAMETER From
Start of the range, excluded. Defaults to the most recent tag reachable from
the end of the range. When no tag exists the first commit is used.

.PARAMETER To
End of the range, included. Defaults to HEAD.

.PARAMETER Unreleased
Generates the section from the last reachable tag to HEAD.

.PARAMETER Version
Version heading, for example 0.3.1. Defaults to Unreleased.

.PARAMETER Section
Prints an existing section from CHANGELOG.md instead of generating one.

.PARAMETER Output
Writes to a file instead of stdout.
#>
[CmdletBinding()]
param(
    [string]$From,
    [string]$To = 'HEAD',
    [switch]$Unreleased,
    [string]$Version,
    [string]$Section,
    [string]$Output
)

$ErrorActionPreference = 'Stop'
$repoRoot = Split-Path -Parent $PSScriptRoot
$changelogPath = Join-Path $repoRoot 'CHANGELOG.md'

function Invoke-Git {
    param([Parameter(ValueFromRemainingArguments = $true)][string[]]$Arguments)
    $result = & git -C $repoRoot @Arguments 2>&1
    if ($LASTEXITCODE -ne 0) {
        throw "git $($Arguments -join ' ') failed: $result"
    }
    return $result
}

function Write-Utf8 {
    param([string]$Path, [string]$Text)
    [System.IO.File]::WriteAllText($Path, $Text, (New-Object System.Text.UTF8Encoding($false)))
}

function Get-Section {
    param([string]$Content, [string]$Name)
    $pattern = "(?ms)^##\s+\[?$([regex]::Escape($Name))\]?[^\r\n]*\r?\n(.*?)(?=^##\s|\z)"
    $match = [regex]::Match($Content, $pattern)
    if (-not $match.Success) { return $null }
    return $match.Groups[1].Value.Trim()
}

if ($Section) {
    if (-not (Test-Path $changelogPath)) { throw 'CHANGELOG.md does not exist' }
    $body = Get-Section -Content ([System.IO.File]::ReadAllText($changelogPath)) -Name $Section
    if (-not $body) { throw "CHANGELOG.md has no section for $Section" }
    if ($Output) { Write-Utf8 -Path $Output -Text $body } else { $body }
    return
}

if ($Unreleased) {
    $To = 'HEAD'
}

if (-not $From) {
    # Pick the newest tag that does not point at the end of the range, so a
    # release tag does not produce an empty changelog against itself.
    $atEnd = (Invoke-Git rev-list -n 1 $To | Select-Object -First 1)
    $tags = @(Invoke-Git tag --merged $To --sort=-creatordate)
    $previous = $null
    foreach ($candidate in $tags) {
        $target = (Invoke-Git rev-list -n 1 $candidate | Select-Object -First 1)
        if ($target -ne $atEnd) {
            $previous = $candidate
            break
        }
    }
    if ($previous) {
        $From = $previous
    } else {
        $From = (Invoke-Git rev-list --max-parents=0 $To | Select-Object -First 1)
    }
}

$range = if ($From) { "$From..$To" } else { $To }
$format = '%H%x1f%s%x1f%b%x1e'
$raw = ((Invoke-Git log --first-parent --reverse "--format=$format" $range) -join "`n")
$commits = $raw -split [char]0x1e | Where-Object { $_.Trim() }

$remote = (Invoke-Git config --get remote.origin.url) -join ''
$remote = $remote.Trim()
$repoUrl = $null
if ($remote -match 'github\.com[:/](?<owner>[^/]+)/(?<repo>[^/.]+)(\.git)?$') {
    $repoUrl = "https://github.com/$($Matches['owner'])/$($Matches['repo'])"
}

$groups = [ordered]@{
    'BREAKING CHANGES' = [System.Collections.Generic.List[string]]::new()
    'Features'         = [System.Collections.Generic.List[string]]::new()
    'Fixes'            = [System.Collections.Generic.List[string]]::new()
    'Performance'      = [System.Collections.Generic.List[string]]::new()
    'Documentation'    = [System.Collections.Generic.List[string]]::new()
    'Tests'            = [System.Collections.Generic.List[string]]::new()
    'Dependencies'     = [System.Collections.Generic.List[string]]::new()
    'Build and CI'     = [System.Collections.Generic.List[string]]::new()
    'Refactors'        = [System.Collections.Generic.List[string]]::new()
    'Reverts'          = [System.Collections.Generic.List[string]]::new()
    'Chores'           = [System.Collections.Generic.List[string]]::new()
}
$typeMap = @{
    feat     = 'Features'
    fix      = 'Fixes'
    perf     = 'Performance'
    docs     = 'Documentation'
    test     = 'Tests'
    tests    = 'Tests'
    build    = 'Build and CI'
    ci       = 'Build and CI'
    refactor = 'Refactors'
    revert   = 'Reverts'
    chore    = 'Chores'
}

foreach ($record in $commits) {
    $fields = $record.Trim("`r", "`n") -split [char]0x1f, 3
    if ($fields.Count -lt 2) { continue }
    $subject = $fields[1].Trim()
    $body = if ($fields.Count -gt 2) { $fields[2] } else { '' }

    if ($subject -match '^Merge ') { continue }
    if ($subject -match '^chore\(release\)') { continue }

    $taskIds = [System.Collections.Generic.List[string]]::new()
    foreach ($text in @($subject, $body)) {
        foreach ($m in [regex]::Matches($text, '\b[A-Z][A-Z0-9]{1,5}-\d{1,3}\b')) {
            if (-not $taskIds.Contains($m.Value)) { $taskIds.Add($m.Value) }
        }
    }

    $pr = $null
    if ($subject -match '\(#(?<pr>\d+)\)\s*$') {
        $pr = [int]$Matches['pr']
        $subject = $subject.Substring(0, $subject.Length - $Matches[0].Length).Trim()
    }

    $type = 'chore'
    $scope = ''
    $title = $subject
    $breaking = $false
    if ($subject -match '^(?<type>[a-zA-Z]+)(\((?<scope>[^)]+)\))?(?<bang>!)?:\s*(?<title>.+)$') {
        $type = $Matches['type'].ToLowerInvariant()
        $scope = $Matches['scope']
        $title = $Matches['title'].Trim()
        $breaking = [bool]$Matches['bang']
    }
    if ($body -match 'BREAKING[ -]CHANGE') { $breaking = $true }

    # Remove task IDs already reported in the suffix from the visible title.
    $title = ($title -replace '\s*\(([A-Z][A-Z0-9]{1,5}-\d{1,3})\)', '').Trim()

    $suffix = @()
    if ($pr -and $repoUrl) { $suffix += "[#$pr]($repoUrl/pull/$pr)" }
    if ($taskIds.Count -gt 0) {
        $suffix += (($taskIds | ForEach-Object { "``$_``" }) -join ', ')
    }
    $entry = "- $title"
    if ($suffix.Count -gt 0) { $entry += ' - ' + ($suffix -join ' | ') }

    $group = $typeMap[$type]
    if (-not $group) { $group = 'Chores' }
    if ($type -eq 'chore' -and $scope -eq 'deps') { $group = 'Dependencies' }

    if ($breaking) {
        $groups['BREAKING CHANGES'].Add($entry)
    } else {
        $groups[$group].Add($entry)
    }
}

$heading = if ($Version) { "## [$Version] - $(Get-Date -Format 'yyyy-MM-dd')" } else { '## Unreleased' }
$lines = [System.Collections.Generic.List[string]]::new()
$lines.Add($heading)
foreach ($name in $groups.Keys) {
    if ($groups[$name].Count -eq 0) { continue }
    $lines.Add('')
    $lines.Add("### $name")
    foreach ($entry in $groups[$name]) { $lines.Add($entry) }
}

$markdown = ($lines -join "`n") + "`n"
if ($Output) {
    Write-Utf8 -Path $Output -Text $markdown
} else {
    $markdown
}
