# PLAT-856-222: real native PowerShell installer with only the download boundary
# replaced. Run on the Windows verification runner, never a person's profile.
param([string]$Installer, [string]$Executable)
$ErrorActionPreference = 'Stop'
$root = Join-Path ([IO.Path]::GetTempPath()) ('aw-install-' + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory $root | Out-Null
$oldPath = [Environment]::GetEnvironmentVariable('Path', 'User')
$oldProcessPath = $env:Path
$profileKeys = @('HOME', 'USERPROFILE', 'APPDATA', 'LOCALAPPDATA', 'XDG_CONFIG_HOME', 'XDG_CACHE_HOME', 'CODEX_HOME', 'CLAUDE_CONFIG_DIR', 'MUSE_CONFIG_DIR', 'ProgramFiles', 'ProgramW6432', 'ProgramFiles(x86)', 'AGENTWORKS_CONFIG', 'AGENTWORKS_BRIDGE_ROOT', 'AGENTWORKS_TOKEN')
$savedProfile = @{}
foreach ($key in $profileKeys) {
  $savedProfile[$key] = [Environment]::GetEnvironmentVariable($key, 'Process')
}
try {
  # NoLogin still runs app setup. Fence all profile roots and app detection,
  # including provider CLIs on PATH, before starting the native installer.
  foreach ($key in $profileKeys) {
    [Environment]::SetEnvironmentVariable($key, (Join-Path $root ($key -replace '[^a-zA-Z0-9_-]', '_')), 'Process')
  }
  $env:AGENTWORKS_TOKEN = ''
  $env:Path = ''
  $gz = Join-Path $root 'fixture.gz'
  $inputFile = [IO.File]::OpenRead($Executable)
  $outputFile = [IO.File]::Create($gz)
  $gzip = [IO.Compression.GZipStream]::new($outputFile, [IO.Compression.CompressionMode]::Compress)
  try { $inputFile.CopyTo($gzip) } finally { $inputFile.Dispose(); $gzip.Dispose(); $outputFile.Dispose() }
  $checksum = (Get-FileHash -Algorithm SHA256 $Executable).Hash
  function Invoke-WebRequest {
    param([switch]$UseBasicParsing, [string]$Uri, [string]$OutFile)
    if ($Uri.EndsWith('.gz')) { Copy-Item $gz $OutFile }
    elseif ($Uri.EndsWith('.sha256')) { Set-Content -Encoding Ascii $OutFile "$checksum  fixture.exe" }
    else { throw "Unexpected download: $Uri" }
  }
  $dir = Join-Path $root 'installed'
  & $Installer -Server 'https://example.invalid' -Dir $dir -NoLogin
  if (-not (Test-Path (Join-Path $dir 'agentworks.exe'))) { throw 'The final executable was not installed.' }

  # Localized OS text must still be identified by Win32 code, and an unrelated
  # access denial must not suggest an antivirus exception.
  $tokens = $null; $parseErrors = $null
  $ast = [Management.Automation.Language.Parser]::ParseFile($Installer, [ref]$tokens, [ref]$parseErrors)
  if ($parseErrors.Count) { throw $parseErrors[0] }
  $helper = $ast.Find({ param($node) $node -is [Management.Automation.Language.FunctionDefinitionAst] -and $node.Name -eq 'Test-AgentWorksDefenderBlock' }, $true)
  . ([scriptblock]::Create($helper.Extent.Text))
  foreach ($code in @(225, 226, 5)) {
    $exception = [ComponentModel.Win32Exception]::new($code, 'localized fixture text')
    $record = [Management.Automation.ErrorRecord]::new($exception, 'fixture', [Management.Automation.ErrorCategory]::OpenError, $null)
    if ((Test-AgentWorksDefenderBlock $record) -ne ($code -in @(225, 226))) { throw "Incorrect block classification: $code" }
  }
  Write-Output 'Native installer staged/final version checks and antivirus error classification: PASS'
} finally {
  foreach ($key in $profileKeys) {
    [Environment]::SetEnvironmentVariable($key, $savedProfile[$key], 'Process')
  }
  [Environment]::SetEnvironmentVariable('Path', $oldPath, 'User')
  $env:Path = $oldProcessPath
  Remove-Item -Recurse -Force $root -ErrorAction SilentlyContinue
}
