$ErrorActionPreference = 'Stop'

if ($args -contains '--help') {
    Write-Output 'Install decide from GitHub Releases. Optional: DECIDE_RELEASE_REPO, DECIDE_VERSION, DECIDE_INSTALL_DIR.'
    exit 0
}
$repo = if ($env:DECIDE_RELEASE_REPO) { $env:DECIDE_RELEASE_REPO } else { 'koltyakov/decide' }
if ($repo -notmatch '^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$') { throw 'Invalid release repository' }
$arch = [System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString().ToLowerInvariant()
switch ($arch) { 'x64' { $arch = 'amd64' }; 'arm64' { }; default { throw 'Unsupported architecture' } }
$base = "https://github.com/$repo/releases/latest/download"
if ($env:DECIDE_VERSION) {
    if ($env:DECIDE_VERSION -notmatch '^[A-Za-z0-9_.+-]+$') { throw 'Invalid version' }
    $base = "https://github.com/$repo/releases/download/$env:DECIDE_VERSION"
}
$asset = "decide_windows_$arch.exe"
$tmp = Join-Path ([IO.Path]::GetTempPath()) ([Guid]::NewGuid().ToString())
$staged = $null
New-Item -ItemType Directory -Path $tmp | Out-Null
try {
    Invoke-WebRequest "$base/checksums.txt" -OutFile (Join-Path $tmp 'checksums.txt') -UseBasicParsing
    $binary = Join-Path $tmp 'decide.exe'
    Invoke-WebRequest "$base/$asset" -OutFile $binary -UseBasicParsing
    $expected = @(Get-Content (Join-Path $tmp 'checksums.txt') | Where-Object { $_ -match "^[a-f0-9]{64}  $([regex]::Escape($asset))$" })
    $actual = (Get-FileHash $binary -Algorithm SHA256).Hash.ToLowerInvariant()
    if ($expected.Count -ne 1 -or ($expected[0] -split '  ')[0] -ne $actual) { throw 'Binary checksum mismatch' }
    $dest = if ($env:DECIDE_INSTALL_DIR) { $env:DECIDE_INSTALL_DIR } else { Join-Path $env:LOCALAPPDATA 'Programs\decide' }
    New-Item -ItemType Directory -Path $dest -Force | Out-Null
    $staged = Join-Path $dest ('.decide-install-' + [Guid]::NewGuid().ToString() + '.exe')
    Copy-Item $binary $staged
    Move-Item $staged (Join-Path $dest 'decide.exe') -Force
    $staged = $null
    $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
    if (-not $userPath) { $userPath = '' }
    if (@($userPath -split ';') -notcontains $dest) {
        [Environment]::SetEnvironmentVariable('Path', ($userPath.TrimEnd(';') + ';' + $dest).TrimStart(';'), 'User')
    }
    Write-Output "Installed $dest\decide.exe. Open a new terminal to reload PATH."
    Write-Output 'Next: decide providers connect official; decide install-mcp opencode; decide install-skill opencode'
} finally {
    Remove-Item $tmp -Recurse -Force
    if ($staged -and (Test-Path $staged)) { Remove-Item $staged -Force }
}
