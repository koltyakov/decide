#Requires -Version 5.1
$ErrorActionPreference = 'Stop'
# PowerShell 5.1's progress bar makes Invoke-WebRequest very slow.
$ProgressPreference = 'SilentlyContinue'

if ($args -contains '--help') {
    Write-Output 'Install or upgrade decide from GitHub Releases. Optional: DECIDE_RELEASE_REPO, DECIDE_VERSION, DECIDE_INSTALL_DIR.'
    exit 0
}
$repo = if ($env:DECIDE_RELEASE_REPO) { $env:DECIDE_RELEASE_REPO } else { 'koltyakov/decide' }
if ($repo -notmatch '\A[A-Za-z0-9_-][A-Za-z0-9_.-]*/[A-Za-z0-9_-][A-Za-z0-9_.-]*\z' -or $repo.Contains('..')) { throw 'Invalid release repository' }

# OSArchitecture reports the native OS even from x64-emulated PowerShell on ARM64.
# Older .NET Framework lacks it, so fall back to the process environment.
$arch = $null
try { $arch = [System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString().ToLowerInvariant() } catch { }
if (-not $arch) {
    $arch = if ($env:PROCESSOR_ARCHITEW6432) { $env:PROCESSOR_ARCHITEW6432 } else { $env:PROCESSOR_ARCHITECTURE }
    $arch = "$arch".ToLowerInvariant()
}
switch ($arch) { { $_ -in 'x64', 'amd64' } { $arch = 'amd64' }; 'arm64' { }; default { throw "Unsupported architecture: $arch" } }
$asset = "decide_windows_$arch.exe"

function Test-Version([string]$value) {
    return $value -match '\A[A-Za-z0-9][A-Za-z0-9_.+-]*\z' -and -not $value.Contains('..')
}

# Keep the user PATH's registry type, so %VARIABLE% entries stay unexpanded.
function Add-UserPath([string]$dir) {
    $key = [Microsoft.Win32.Registry]::CurrentUser.CreateSubKey('Environment')
    try {
        $current = $key.GetValue('Path', '', [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames)
        $kind = if ($key.GetValueNames() -contains 'Path') { $key.GetValueKind('Path') } else { [Microsoft.Win32.RegistryValueKind]::ExpandString }
        $entries = @("$current" -split ';' | Where-Object { $_ })
        foreach ($entry in $entries) {
            if ([string]::Equals([Environment]::ExpandEnvironmentVariables($entry).TrimEnd('\'), $dir.TrimEnd('\'), [StringComparison]::OrdinalIgnoreCase)) { return $false }
        }
        $key.SetValue('Path', (($entries + $dir) -join ';'), $kind)
        return $true
    } finally {
        $key.Close()
    }
}

$tmp = Join-Path ([IO.Path]::GetTempPath()) ([Guid]::NewGuid().ToString())
$staged = $null
New-Item -ItemType Directory -Path $tmp | Out-Null
try {
    $version = $env:DECIDE_VERSION
    if (-not $version) {
        # Resolve "latest" once, so the checksum list and binary come from one release.
        $manifestPath = Join-Path $tmp 'decide-manifest.json'
        Invoke-WebRequest "https://github.com/$repo/releases/latest/download/decide-manifest.json" -OutFile $manifestPath -UseBasicParsing
        $version = [string](Get-Content $manifestPath -Raw | ConvertFrom-Json).version
    }
    if (-not (Test-Version $version)) { throw 'Invalid version' }
    $base = "https://github.com/$repo/releases/download/$version"
    $checksums = Join-Path $tmp 'checksums.txt'
    Invoke-WebRequest "$base/checksums.txt" -OutFile $checksums -UseBasicParsing
    $expected = @(Get-Content $checksums | Where-Object { $_ -match "^[a-f0-9]{64}  $([regex]::Escape($asset))$" })
    if ($expected.Count -eq 0) { throw "Binary checksum missing for $asset in $version" }
    if ($expected.Count -gt 1) { throw "Duplicate binary checksum for $asset" }
    $binary = Join-Path $tmp 'decide.exe'
    Invoke-WebRequest "$base/$asset" -OutFile $binary -UseBasicParsing
    $actual = (Get-FileHash $binary -Algorithm SHA256).Hash.ToLowerInvariant()
    if (($expected[0] -split '  ')[0] -ne $actual) { throw 'Binary checksum mismatch' }

    $dest = if ($env:DECIDE_INSTALL_DIR) { $env:DECIDE_INSTALL_DIR } else { Join-Path $env:LOCALAPPDATA 'Programs\decide' }
    $dest = [IO.Path]::GetFullPath($dest)
    New-Item -ItemType Directory -Path $dest -Force | Out-Null
    # Remove executables renamed aside by earlier upgrades once they are unlocked.
    Get-ChildItem -LiteralPath $dest -Filter '.decide-old-*.exe' -Force -ErrorAction SilentlyContinue | Remove-Item -Force -ErrorAction SilentlyContinue
    $staged = Join-Path $dest ('.decide-install-' + [Guid]::NewGuid().ToString() + '.exe')
    Copy-Item $binary $staged
    # Run the verified binary before replacing anything.
    & $staged --version | Out-Null
    if ($LASTEXITCODE -ne 0) { throw 'Downloaded binary does not run on this system' }
    $target = Join-Path $dest 'decide.exe'
    $previous = $null
    if (Test-Path -LiteralPath $target) {
        # Windows can rename a running executable, such as one serving MCP, but not overwrite it.
        $previous = Join-Path $dest ('.decide-old-' + [Guid]::NewGuid().ToString() + '.exe')
        Move-Item -LiteralPath $target $previous
    }
    try {
        Move-Item -LiteralPath $staged $target
    } catch {
        if ($previous) { Move-Item -LiteralPath $previous $target -Force }
        throw
    }
    $staged = $null
    if ($previous) { Remove-Item -LiteralPath $previous -Force -ErrorAction SilentlyContinue }
    $added = Add-UserPath $dest
    if (@($env:Path -split ';') -notcontains $dest) { $env:Path = "$dest;$env:Path" }
    Write-Output "Installed decide $version to $target."
    if ($added) { Write-Output 'Added it to your user PATH; other open terminals need a restart.' }
    Write-Output 'After an upgrade, run: decide install-skill --refresh'
    Write-Output 'New setup: decide providers connect official; decide install-mcp opencode; decide install-skill opencode'
} finally {
    Remove-Item $tmp -Recurse -Force -ErrorAction SilentlyContinue
    if ($staged -and (Test-Path -LiteralPath $staged)) { Remove-Item -LiteralPath $staged -Force }
}
