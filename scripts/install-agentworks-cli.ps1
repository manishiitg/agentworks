# install-agentworks.ps1 - install the AgentWorks CLI from your server on Windows and (optionally) sign in.
#
# Served at https://<server>/api/downloads/cli/install-agentworks.ps1 and shown prefilled in Your computer.
#
#   & ([scriptblock]::Create((irm 'https://<server>/api/downloads/cli/install-agentworks.ps1'))) -Server 'https://<server>' -NoLogin
#
# Installs %LOCALAPPDATA%\agentworks\agentworks.exe, adds that folder to your user PATH, and checks for Git for Windows (commands
# the agent runs on your computer go through Git Bash). Nothing needs administrator rights.
param(
  [string]$Server = $env:AGENTWORKS_SERVER,
  [string]$Dir = (Join-Path $env:LOCALAPPDATA 'agentworks'),
  [switch]$NoLogin
)
$ErrorActionPreference = 'Stop'
if (-not $Server) { $Server = 'https://network.agentworkshq.com' }
$Server = $Server.TrimEnd('/')

function Test-AgentWorksDefenderBlock($Failure) {
  for ($cause = $Failure.Exception; $cause; $cause = $cause.InnerException) {
    if ($cause -is [ComponentModel.Win32Exception] -and $cause.NativeErrorCode -in @(225, 226)) { return $true }
  }
  return "$Failure" -match 'virus or potentially unwanted software'
}

function Invoke-AgentWorksCommand([string]$Executable, [string[]]$Arguments, [string]$AfterAllow = 'run: agentworks setup') {
  try {
    & $Executable @Arguments
    $script:LASTEXITCODE = $LASTEXITCODE
  } catch {
    if (Test-AgentWorksDefenderBlock $_) {
      throw "Windows Defender blocked this unsigned AgentWorks app. Open Windows Security > Virus & threat protection > Protection history, open the AgentWorks entry, choose Actions > Allow on device, then $AfterAllow"
    }
    throw
  }
}

$machineArchitecture = $env:PROCESSOR_ARCHITEW6432
if (-not $machineArchitecture) { $machineArchitecture = $env:PROCESSOR_ARCHITECTURE }
switch ($machineArchitecture) {
  'AMD64' { $arch = 'amd64' }
  'ARM64' { $arch = 'arm64' }
  default { throw "unsupported architecture: $machineArchitecture (need AMD64 or ARM64)" }
}
$asset = "agentworks-windows-$arch.exe"
$base = "$Server/api/downloads/cli"
$stage = Join-Path ([IO.Path]::GetTempPath()) ("agentworks-" + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $stage | Out-Null
try {
  Write-Host "Downloading $asset ..."
  Invoke-WebRequest -UseBasicParsing -Uri "$base/$asset.gz" -OutFile (Join-Path $stage "$asset.gz")
  $compressed = [IO.File]::OpenRead((Join-Path $stage "$asset.gz"))
  try {
    $gzip = [IO.Compression.GZipStream]::new($compressed, [IO.Compression.CompressionMode]::Decompress)
    try {
      $unpacked = [IO.File]::Create((Join-Path $stage $asset))
      try { $gzip.CopyTo($unpacked) } finally { $unpacked.Dispose() }
    } finally { $gzip.Dispose() }
  } finally { $compressed.Dispose() }
  Invoke-WebRequest -UseBasicParsing -Uri "$base/$asset.sha256" -OutFile (Join-Path $stage "$asset.sha256")

  Write-Host 'Verifying checksum ...'
  $expected = ((Get-Content (Join-Path $stage "$asset.sha256") -Raw).Trim() -split '\s+')[0].ToLower()
  $actual = (Get-FileHash -Algorithm SHA256 (Join-Path $stage $asset)).Hash.ToLower()
  if ($expected -ne $actual) { throw "checksum mismatch for $asset; refusing to install" }

  # The CLI's version command is local-only. Invoke the staged file with the
  # call operator so spaces and apostrophes in the path stay one argument.
  $candidate = Join-Path $stage $asset
  $versionOutput = Invoke-AgentWorksCommand -Executable $candidate -Arguments @('version') -AfterAllow 'rerun this installer to finish installation.'
  if ($LASTEXITCODE -ne 0) { throw 'The downloaded CLI could not run; the installed copy was kept.' }
  $candidateVersion = $versionOutput | ConvertFrom-Json
  if ($candidateVersion.os -ne 'windows' -or $candidateVersion.arch -ne $arch -or -not $candidateVersion.version) {
    throw 'The downloaded CLI reported the wrong platform; the installed copy was kept.'
  }
  New-Item -ItemType Directory -Force -Path $Dir | Out-Null
  $target = Join-Path $Dir 'agentworks.exe'
  $oldTarget = "$target.old-" + [guid]::NewGuid().ToString('N')
  if (Test-Path $target) { Move-Item $target $oldTarget } # a running copy can be renamed, not replaced
  Move-Item -Force $candidate $target
  # Defender can act on the final name even after the staged file ran.
  $installedVersion = Invoke-AgentWorksCommand -Executable $target -Arguments @('version') | ConvertFrom-Json
  if ($LASTEXITCODE -ne 0 -or $installedVersion.version -ne $candidateVersion.version -or $installedVersion.os -ne 'windows' -or $installedVersion.arch -ne $arch) {
    throw 'The installed CLI could not run correctly. Run this installer again to retry.'
  }
  Remove-Item $oldTarget -Force -ErrorAction SilentlyContinue
  Write-Host "Installed $target"
} finally {
  Remove-Item -Recurse -Force $stage -ErrorAction SilentlyContinue
}

$userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
if (($userPath -split ';') -notcontains $Dir) {
  [Environment]::SetEnvironmentVariable('Path', (($userPath, $Dir) -ne '' -join ';'), 'User')
  $env:Path = "$env:Path;$Dir"
  Write-Host "Added $Dir to your PATH (new terminals pick it up)."
}

$bash = @("$env:ProgramFiles\Git\bin\bash.exe", "$env:ProgramW6432\Git\bin\bash.exe", "${env:ProgramFiles(x86)}\Git\bin\bash.exe", "$env:LOCALAPPDATA\Programs\Git\bin\bash.exe") | Where-Object { $_ -and (Test-Path $_) } | Select-Object -First 1
if (-not $bash) {
  Write-Warning 'Git for Windows was not found. File tools work without it, but commands the agent runs on your computer need it: https://git-scm.com/download/win'
}

if (-not $NoLogin) {
  Write-Host "Signing in to $Server ..."
  Invoke-AgentWorksCommand -Executable $target -Arguments @('login', '--server', $Server)
  if ($LASTEXITCODE -ne 0) { throw 'Sign-in did not finish. AgentWorks is installed; run agentworks login, then agentworks setup to continue.' }
}
Write-Host 'Setting up your installed AI apps ...'
Invoke-AgentWorksCommand -Executable $target -Arguments @('setup', '--server', $Server)
if ($LASTEXITCODE -ne 0) { Write-Warning 'App setup needs attention. Run agentworks setup to retry; AgentWorks is installed.' }
Write-Host "Done. Restart the apps listed above. In AgentWorks, open Your computer to connect this computer's files and commands."
