[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [ValidatePattern('^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z.-]+)?$')]
    [string]$Tag,

    [string]$Remote = 'github',

    [string]$Branch = 'main',

    [switch]$Push,

    [switch]$AllowNonGitHubRemote
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

function Invoke-Git {
    param([Parameter(Mandatory = $true)][string[]]$Arguments)

    $result = @(& git @Arguments 2>&1)
    if ($LASTEXITCODE -ne 0) {
        throw "git $($Arguments -join ' ') failed:`n$($result -join [Environment]::NewLine)"
    }
    return $result
}

function Get-GitOutput {
    param([Parameter(Mandatory = $true)][string[]]$Arguments)

    return ((Invoke-Git -Arguments $Arguments) -join "`n").Trim()
}

$repositoryRoot = Get-GitOutput -Arguments @('rev-parse', '--show-toplevel')
Push-Location $repositoryRoot
try {
    $status = Get-GitOutput -Arguments @('status', '--porcelain=v1', '--untracked-files=normal')
    if ($status) {
        throw 'The private working tree must be clean before creating a public snapshot.'
    }

    $remoteURL = Get-GitOutput -Arguments @('remote', 'get-url', '--push', $Remote)
    if (-not $AllowNonGitHubRemote -and $remoteURL -notmatch '^(git@github\.com:|https://github\.com/|ssh://git@github\.com/)[^/]+/[^/]+(?:\.git)?$') {
        throw "Remote '$Remote' is not a GitHub repository: $remoteURL"
    }

    $tagReference = "refs/tags/$Tag"
    $existingTag = Get-GitOutput -Arguments @('ls-remote', '--tags', $Remote, $tagReference)
    if ($existingTag) {
        throw "Release tag $Tag already exists on $Remote and will not be moved."
    }

    $branchReference = "refs/heads/$Branch"
    $branchResult = Get-GitOutput -Arguments @('ls-remote', '--heads', $Remote, $branchReference)
    $publicParent = $null
    if ($branchResult) {
        $fields = $branchResult -split '\s+'
        if ($fields.Count -lt 2 -or $fields[1] -ne $branchReference -or $fields[0] -notmatch '^[0-9a-f]{40,64}$') {
            throw "Unexpected response while reading $Remote/$Branch."
        }
        $publicParent = $fields[0]
        Invoke-Git -Arguments @('fetch', '--no-tags', $Remote, "+${branchReference}:refs/remotes/${Remote}/${Branch}") | Out-Null
        $fetchedParent = Get-GitOutput -Arguments @('rev-parse', "refs/remotes/${Remote}/${Branch}^{commit}")
        if ($fetchedParent -ne $publicParent) {
            throw "Fetched $Remote/$Branch does not match its advertised commit."
        }
        $parentMessage = Get-GitOutput -Arguments @('show', '-s', '--format=%B', $publicParent)
        if ($parentMessage -notmatch '(?m)^Kaven-Public-Snapshot: true$') {
            throw "$Remote/$Branch was not created by this publisher. Start with an empty GitHub repository."
        }
    }

    $trackedPaths = @(Invoke-Git -Arguments @('ls-tree', '-r', '--name-only', 'HEAD'))
    $unsafePatterns = @(
        '(^|/)\.env($|\.)',
        '\.(bson|db|sqlite|sqlite3|pem|key|p12|pfx)$',
        '(^|/)(credentials|secrets)\.json$'
    )
    foreach ($trackedPath in $trackedPaths) {
        foreach ($pattern in $unsafePatterns) {
            if ($trackedPath -match $pattern -and $trackedPath -notmatch '(^|/)\.env\.example$') {
                throw "Refusing to publish sensitive-looking tracked path: $trackedPath"
            }
        }
    }

    $treeEntries = @(Invoke-Git -Arguments @('ls-tree', '-r', 'HEAD'))
    if ($treeEntries | Where-Object { $_ -match '^160000\s' }) {
        throw 'Git submodules are not supported in a public snapshot.'
    }

    $sourceTree = Get-GitOutput -Arguments @('rev-parse', 'HEAD^{tree}')
    $message = "Release $Tag`n`nKaven-Public-Snapshot: true`nSource-Tree: $sourceTree"
    $commitArguments = @('commit-tree', $sourceTree, '-m', $message)
    if ($publicParent) {
        $commitArguments += @('-p', $publicParent)
    }
    $sourceAuthorDate = Get-GitOutput -Arguments @('show', '-s', '--format=%aI', 'HEAD')
    $sourceCommitterDate = Get-GitOutput -Arguments @('show', '-s', '--format=%cI', 'HEAD')
    $previousAuthorDate = $env:GIT_AUTHOR_DATE
    $previousCommitterDate = $env:GIT_COMMITTER_DATE
    try {
        $env:GIT_AUTHOR_DATE = $sourceAuthorDate
        $env:GIT_COMMITTER_DATE = $sourceCommitterDate
        $publicCommit = Get-GitOutput -Arguments $commitArguments
    } finally {
        if ($null -eq $previousAuthorDate) {
            Remove-Item Env:GIT_AUTHOR_DATE -ErrorAction SilentlyContinue
        } else {
            $env:GIT_AUTHOR_DATE = $previousAuthorDate
        }
        if ($null -eq $previousCommitterDate) {
            Remove-Item Env:GIT_COMMITTER_DATE -ErrorAction SilentlyContinue
        } else {
            $env:GIT_COMMITTER_DATE = $previousCommitterDate
        }
    }

    $publishedTree = Get-GitOutput -Arguments @('show', '-s', '--format=%T', $publicCommit)
    if ($publishedTree -ne $sourceTree) {
        throw 'The generated public commit does not contain the current source tree.'
    }
    $parentLines = @(Invoke-Git -Arguments @('show', '-s', '--format=%P', $publicCommit))
    $actualParents = @(($parentLines -join ' ').Trim() -split '\s+' | Where-Object { $_ })
    if ($publicParent) {
        if ($actualParents.Count -ne 1 -or $actualParents[0] -ne $publicParent) {
            throw 'The generated commit does not have exactly the previous public snapshot as its parent.'
        }
        $newCommitCount = Get-GitOutput -Arguments @('rev-list', '--count', "$publicParent..$publicCommit")
        if ($newCommitCount -ne '1') {
            throw 'The generated public history contains more than one new commit.'
        }
    } elseif ($actualParents.Count -ne 0) {
        throw 'The first public snapshot must be an orphan commit.'
    }

    Write-Host "Release tag:       $Tag"
    Write-Host "Private source:    $(Get-GitOutput -Arguments @('rev-parse', 'HEAD^{commit}'))"
    Write-Host "Source tree:       $sourceTree"
    Write-Host "Public commit:     $publicCommit"
    if ($publicParent) {
        Write-Host "Public parent:     $publicParent"
    } else {
        Write-Host 'Public parent:     none (first release)'
    }
    Write-Host "Destination:       $Remote/$Branch ($remoteURL)"

    if (-not $Push) {
        Write-Host ''
        Write-Host 'Preview only. Run the same command with -Push to publish the branch and tag atomically.'
        return
    }

    if ($publicParent) {
        Invoke-Git -Arguments @(
            'push', '--atomic', $Remote,
            "${publicCommit}:${branchReference}",
            "${publicCommit}:${tagReference}"
        ) | ForEach-Object { Write-Host $_ }
    } else {
        # GitHub discovers tag-triggered workflows from the default branch. On
        # an empty repository, publish that branch before creating the first tag.
        Invoke-Git -Arguments @('push', $Remote, "${publicCommit}:${branchReference}") |
            ForEach-Object { Write-Host $_ }
        Invoke-Git -Arguments @('push', $Remote, "${publicCommit}:${tagReference}") |
            ForEach-Object { Write-Host $_ }
    }

    Write-Host ''
    Write-Host "Published $Tag. GitHub Actions will build the release from public commit $publicCommit."
} finally {
    Pop-Location
}
