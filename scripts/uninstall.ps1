#Requires -Version 5.1
$ErrorActionPreference = 'Stop'

if ($args -contains '--help') {
    Write-Output 'Remove the decide binary and its user PATH entry. Optional: DECIDE_INSTALL_DIR. Credentials, MCP entries, and skills are kept.'
    exit 0
}
$dest = if ($env:DECIDE_INSTALL_DIR) { $env:DECIDE_INSTALL_DIR } else { Join-Path $env:LOCALAPPDATA 'Programs\decide' }
$dest = [IO.Path]::GetFullPath($dest)
$target = Join-Path $dest 'decide.exe'
if (Test-Path -LiteralPath $target) {
    # An agent host may still be running decide mcp; wait up to 10 seconds for it to exit.
    $deadline = (Get-Date).AddSeconds(10)
    while ($true) {
        try { Remove-Item -LiteralPath $target -Force; break } catch {
            if ((Get-Date) -ge $deadline) { throw "decide.exe is in use; stop agent hosts running decide mcp and retry" }
            Start-Sleep -Milliseconds 250
        }
    }
    Write-Output "Removed $target"
} else {
    Write-Output "decide is not installed in $dest"
}
Get-ChildItem -LiteralPath $dest -Filter '.decide-*.exe' -Force -ErrorAction SilentlyContinue | Remove-Item -Force -ErrorAction SilentlyContinue
$key = [Microsoft.Win32.Registry]::CurrentUser.CreateSubKey('Environment')
try {
    if ($key.GetValueNames() -contains 'Path') {
        $kind = $key.GetValueKind('Path')
        $entries = @("$($key.GetValue('Path', '', [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames))" -split ';' | Where-Object { $_ })
        $kept = @($entries | Where-Object { -not [string]::Equals([Environment]::ExpandEnvironmentVariables($_).TrimEnd('\'), $dest.TrimEnd('\'), [StringComparison]::OrdinalIgnoreCase) })
        if ($kept.Count -ne $entries.Count) {
            $key.SetValue('Path', ($kept -join ';'), $kind)
            Write-Output "Removed $dest from your user PATH."
        }
    }
} finally {
    $key.Close()
}
if ((Test-Path -LiteralPath $dest) -and -not (Get-ChildItem -LiteralPath $dest -Force)) { Remove-Item -LiteralPath $dest -Force }
Write-Output 'Kept: saved credentials (%USERPROFILE%\.config\decide, XDG_CONFIG_HOME, or DECIDE_CREDENTIALS_FILE), client MCP entries, and installed skills.'
Write-Output 'Remove those by hand, and revoke provider keys in each provider console if needed.'
