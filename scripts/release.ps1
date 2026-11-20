#Requires -Version 5.1
<#
.SYNOPSIS
Prepares a release pull request or tags an already merged release.

.DESCRIPTION
Prepare mode validates the version, generates the changelog entry from git
history, commits CHANGELOG.md on a release branch and opens the pull request.

Tag mode verifies that the working tree is clean, master matches origin/master
and CHANGELOG.md already contains the version, then creates and pushes the
annotated tag. The tag starts .github/workflows/release.yml.

The script never pushes to master and never re-tags a published version.

.PARAMETER Version
Release version without the v prefix, for example 0.3.1.

.PARAMETER Tag
Tags the already merged version. Mutually exclusive with -Version.

.PARAMETER DryRun
Prints the actions without changing the repository or GitHub.
#>
[CmdletBinding(DefaultParameterSetName = 'Prepare')]
param(
    [Parameter(ParameterSetName = 'Prepare', Mandatory = $true)]
    [string]$Version,

    [Parameter(ParameterSetName = 'Tag', Mandatory = $true)]
    [string]$Tag,

    [switch]$DryRun
)

$ErrorActionPreference = 'Stop'
$repoRoot = Split-Path -Parent $PSScriptRoot
Set-Location $repoRoot

function Invoke-Git {
    param([Parameter(ValueFromRemainingArguments = $true)][string[]]$Arguments)
    $result = & git @Arguments 2>&1
    if ($LASTEXITCODE -ne 0) {
        throw "git $($Arguments -join ' ') failed: $result"
    }
    return $result
}

function Write-Utf8 {
    param([string]$Path, [string]$Text)
    [System.IO.File]::WriteAllText($Path, $Text, (New-Object System.Text.UTF8Encoding($false)))
}

function Assert-CleanTree {
    $dirty = Invoke-Git status --porcelain
    if ($dirty) { throw "Working tree is not clean:`n$($dirty -join "`n")" }
}

function Assert-Master {
    $branch = (Invoke-Git rev-parse --abbrev-ref HEAD) -join ''
    if ($branch.Trim() -ne 'master') { throw "Release work must start on master, not $branch" }

    Invoke-Git fetch origin master | Out-Null
    $local = (Invoke-Git rev-parse HEAD) -join ''
    $remote = (Invoke-Git rev-parse origin/master) -join ''
    if ($local.Trim() -ne $remote.Trim()) {
        throw 'master is not identical to origin/master. Pull or push before releasing.'
    }
}

function Assert-TagAbsent {
    param([string]$Name)
    & git rev-parse -q --verify "refs/tags/$Name" *> $null
    if ($LASTEXITCODE -eq 0) { throw "Tag $Name already exists" }
}

$changelog = Join-Path $repoRoot 'CHANGELOG.md'
$changelogScript = Join-Path $PSScriptRoot 'changelog.ps1'

if ($PSCmdlet.ParameterSetName -eq 'Tag') {
    $normalized = $Tag -replace '^v', ''
    if ($normalized -notmatch '^\d+\.\d+\.\d+(-[0-9A-Za-z.-]+)?$') {
        throw "Invalid version: $Tag"
    }
    $name = "v$normalized"

    Assert-CleanTree
    Assert-Master
    Assert-TagAbsent -Name $name

    $hasSection = & powershell -NoProfile -ExecutionPolicy Bypass -File $changelogScript -Section $normalized 2>$null
    if ($LASTEXITCODE -ne 0) {
        throw "CHANGELOG.md has no section for $normalized. Merge the release pull request first."
    }

    if ($DryRun) {
        Write-Host "Would create and push annotated tag $name"
        Write-Host "Section preview:"
        Write-Host ($hasSection | Select-Object -First 6)
        return
    }

    Invoke-Git tag -a $name -m $name | Out-Null
    Invoke-Git push origin $name | Out-Null
    Write-Host "Tagged $name and started the release workflow." -ForegroundColor Green
    Write-Host "Watch: gh run list --workflow release.yml --limit 1"
    return
}

$normalized = $Version -replace '^v', ''
if ($normalized -notmatch '^\d+\.\d+\.\d+(-[0-9A-Za-z.-]+)?$') {
    throw "Invalid version: $Version"
}
$name = "v$normalized"

Assert-CleanTree
Assert-Master
Assert-TagAbsent -Name $name
if (-not (Test-Path $changelog)) { throw 'CHANGELOG.md does not exist' }

$section = (& powershell -NoProfile -ExecutionPolicy Bypass -File $changelogScript -Version $normalized) -join "`n"
if (-not $section.Trim()) { throw 'Changelog generation produced no output' }

# Replace an existing Unreleased block, otherwise insert the new section after
# the title. A fresh empty Unreleased block stays at the top.
$content = [System.IO.File]::ReadAllText($changelog)
$header = "# Changelog`n`nMaintained from conventional commits. See [docs/RELEASING.md](docs/RELEASING.md) for the release process.`n`n## Unreleased`n"
if ($content -match '(?m)^## Unreleased') {
    $content = [regex]::Replace($content, '(?ms)(^# Changelog.*?^## Unreleased[ \t]*\r?\n).*?(?=^## |\z)', "`$1`n$section`n")
} else {
    $content = $header + "`n$section`n" +
        ($content -replace '(?s)^# Changelog\s*\n+', '')
}
Write-Utf8 -Path $changelog -Text $content

$branch = "chore/release-$name"
if ($DryRun) {
    Write-Host "Would create branch $branch, commit CHANGELOG.md and open a pull request for $name"
    Write-Host $section
    Invoke-Git checkout -- $changelog
    return
}

Invoke-Git checkout -b $branch | Out-Null
Invoke-Git add CHANGELOG.md | Out-Null
Invoke-Git commit -m "chore(release): $name" -m "Generated from conventional commits between the previous tag and master." | Out-Null
Invoke-Git push -u origin $branch | Out-Null

$body = @"
## Summary

- Task(s): ``BASE-08`` (release model)
- Change type: ``chore``
- Branch: ``$branch``

Release preparation for ``$name``. The changelog entry is generated by
``scripts/changelog.ps1`` from first-parent history, conventional-commit types, pull request
numbers and task IDs.

## Risk and blast radius

- Risk level: ``low``
- Affected surfaces: ``CHANGELOG.md`` only. No runtime code changes.
- Rollback plan: close the pull request and delete the branch. No tag exists until the merge.

## Verification

- [x] ``scripts/changelog.ps1`` generated the section below
- [x] ``master`` matched ``origin/master`` before branching
- [x] Tag ``$name`` does not exist
- [ ] Pull-request gate green
- [ ] Staging deployment and rollback rehearsal (if this is the first release of the minor line)

## Release notes

$section
"@
$bodyFile = Join-Path ([System.IO.Path]::GetTempPath()) "spm-release-$normalized.md"
Write-Utf8 -Path $bodyFile -Text $body

$url = & gh pr create --base master --head $branch --title "chore(release): $name" --body-file $bodyFile 2>&1
if ($LASTEXITCODE -ne 0) { throw "gh pr create failed: $url" }
Remove-Item $bodyFile -ErrorAction SilentlyContinue

Write-Host "Release pull request: $url" -ForegroundColor Green
Write-Host "After it merges: powershell -NoProfile -ExecutionPolicy Bypass -File scripts/release.ps1 -Tag $normalized"
