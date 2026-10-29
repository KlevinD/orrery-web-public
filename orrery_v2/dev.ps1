param(
  [ValidateSet('start', 'stop', 'restart', 'status', 'logs')]
  [string]$Action = 'start',

  [string]$ApiAddr = '127.0.0.1:8080',
  [int]$WebPort = 5178,
  [switch]$Follow,
  [switch]$SkipDependencies,
  [switch]$Local
)

$ErrorActionPreference = 'Stop'

$Root = Split-Path -Parent $MyInvocation.MyCommand.Path
$WebRoot = Join-Path $Root 'web'
$RunRoot = Join-Path $Root '.dev'
$ApiPidFile = Join-Path $RunRoot 'api.pid'
$WebPidFile = Join-Path $RunRoot 'web.pid'
$ApiOut = Join-Path $RunRoot 'api.out'
$ApiLog = Join-Path $RunRoot 'api.log'
$ApiErrLog = Join-Path $RunRoot 'api.err.log'
$WebLog = Join-Path $RunRoot 'web.log'
$WebErrLog = Join-Path $RunRoot 'web.err.log'
$DevStartLog = Join-Path $RunRoot 'dev-start.log'
$ApiRuntimeStateFile = Join-Path $RunRoot 'api.runtime.json'
$WSLKeepAlivePidFile = Join-Path $RunRoot 'wsl-keepalive.pid'
$DefaultDevConfig = Join-Path $Root 'config\orrery.development.yaml'
$LocalDevConfig = Join-Path $RunRoot 'orrery.wsl.yaml'
$TrackedWSLDevConfig = Join-Path $Root 'config\orrery.wsl-development.yaml'
$RemoteEnvFile = Join-Path $RunRoot 'remote.env'
$RemoteConfigTemplate = Join-Path $Root 'config\orrery.remote-development.yaml'
$RemoteRuntimeConfig = Join-Path $RunRoot 'orrery.remote.yaml'
$RemoteDatabaseRoles = @('core', 'audit', 'events', 'jobs', 'runtime', 'observability')
$LocalIdentityCompose = Join-Path $RunRoot 'identity\docker-compose.yml'
$TrackedIdentityCompose = Join-Path $Root 'deploy\dev\identity\docker-compose.yml'
$OpenFGAModelPath = Join-Path $Root 'deploy\dev\identity\authorization-model.json'
$OpenFGAStateFile = Join-Path $RunRoot 'openfga-state.json'
$OpenFGADefaultPort = 8082
$script:OpenFGAPublicPort = $OpenFGADefaultPort
$script:OpenFGAApiURL = "http://127.0.0.1:$OpenFGADefaultPort"
$OpenFGAStoreName = 'orrery-development'
$DefaultWSLDistro = 'CapsuleSecurity'
$DefaultWSLPostgresContainer = 'pageindex-postgres'
$DefaultWSLPostgresImage = 'postgres:16-alpine'
$DefaultWSLPostgresVolume = 'pageindex-postgres-data'
$DefaultWSLPostgresUser = 'orrery'
$DefaultWSLPostgresPassword = 'orrery'
$DefaultWSLPostgresDatabase = 'orrery'
$DefaultWSLKratosUsername = 'superadmin'
$DefaultWSLKratosPassword = 'orrery-dev-password'
$DefaultWSLKratosAgentProfileID = 'human-superadmin'
$DefaultWSLKratosDisplayName = 'superadmin'
$LegacyWSLKratosUsername = 'pm'
$LegacyWSLKratosAgentProfileID = 'human-pm'
$DefaultDevPostgresHome = 'E:\pgsql'
$DefaultDevPostgresData = 'E:\pgsql-data'
$DefaultDevPostgresHost = '127.0.0.1'
$DefaultDevPostgresPort = 5433
$DefaultDevPostgresUser = 'orrery'
$DefaultDevPostgresPassword = 'orrery'
$DefaultDevPostgresAdminUser = 'postgres'
$DefaultDevPostgresAdminPassword = 'postgres'
$DefaultDevPostgresDatabase = 'orrery'
$PiCodingAgentRoot = Join-Path $Root 'runtime\pi-coding-agent'
$MiniMaxTokenPlanSecret = Join-Path $Root 'secrets\minimax_api_key'
$RequiredPiNodeMajor = 22
$RequiredPiNodeMinor = 19
$ApiAddrWasExplicit = $PSBoundParameters.ContainsKey('ApiAddr')

function Resolve-Tool([string]$Name) {
  if ($Name -eq 'npm') {
    $Cmd = Get-Command 'npm.cmd' -ErrorAction SilentlyContinue
    if ($Cmd) {
      return $Cmd.Source
    }
  }

  $Command = Get-Command $Name -ErrorAction Stop
  if ($Command.CommandType -eq 'ExternalScript') {
    return $Command.Source
  }
  if ($Command.Path) {
    return $Command.Path
  }
  return $Command.Source
}

function Ensure-RunRoot {
  if (-not (Test-Path -LiteralPath $RunRoot)) {
    New-Item -ItemType Directory -Path $RunRoot | Out-Null
  }
}

function Write-DevMessage([string]$Message = '') {
  Write-Information $Message -InformationAction Continue
  if (Test-Path -LiteralPath $RunRoot) {
    $Timestamp = Get-Date -Format o
    Add-Content -LiteralPath $DevStartLog -Value "[$Timestamp] $Message" -Encoding UTF8
  }
}

function Assert-PiNodeVersion {
  $NodePath = Resolve-Tool 'node'
  $VersionText = (& $NodePath --version 2>&1 | Out-String).Trim()
  $Version = $VersionText.TrimStart('v')
  $Parts = $Version.Split('.')
  if ($Parts.Count -lt 2) {
    throw "Cannot parse Node version '$VersionText'. PI runtime requires Node >= $RequiredPiNodeMajor.$RequiredPiNodeMinor.0."
  }
  $Major = [int]$Parts[0]
  $Minor = [int]$Parts[1]
  if ($Major -lt $RequiredPiNodeMajor -or ($Major -eq $RequiredPiNodeMajor -and $Minor -lt $RequiredPiNodeMinor)) {
    throw "PI runtime requires Node >= $RequiredPiNodeMajor.$RequiredPiNodeMinor.0, current $VersionText. Upgrade Node before starting Orrery with PI."
  }
}

function Ensure-PiCodingAgentRuntime {
  $Package = Join-Path $PiCodingAgentRoot 'package.json'
  $Lock = Join-Path $PiCodingAgentRoot 'package-lock.json'
  $PiPackage = Join-Path $PiCodingAgentRoot 'node_modules\@earendil-works\pi-coding-agent\package.json'
  $UtilityVerifier = Join-Path $Root 'runtime\orrery-js\install.mjs'
  Write-DevMessage "Checking PI coding agent runtime..."
  if (-not (Test-Path -LiteralPath $Package) -or -not (Test-Path -LiteralPath $Lock)) {
    throw "PI coding agent runtime is missing at fixed path: $PiCodingAgentRoot"
  }
  Assert-PiNodeVersion
  $NeedsInstall = -not (Test-Path -LiteralPath $PiPackage)
  if (-not $NeedsInstall) {
    $PreviousErrorPreference = $ErrorActionPreference
    try {
      $ErrorActionPreference = 'Continue'
      & (Resolve-Tool 'node') $UtilityVerifier pi --check *> $null
      $NeedsInstall = $LASTEXITCODE -ne 0
    } finally { $ErrorActionPreference = $PreviousErrorPreference }
  }
  if ($NeedsInstall) {
    Write-DevMessage "Installing PI coding agent runtime dependencies at $PiCodingAgentRoot..."
    & (Resolve-Tool 'npm') --prefix $PiCodingAgentRoot ci --ignore-scripts
    if ($LASTEXITCODE -ne 0) { throw 'PI coding agent dependency installation failed.' }
  }
  & (Resolve-Tool 'node') $UtilityVerifier pi --check
  if ($LASTEXITCODE -ne 0) { throw 'PI coding agent Orrery utility verification failed.' }
  Write-DevMessage "PI coding agent runtime ready."
}

function Get-ProcessEnvironmentOrDefault([string]$Name, [string]$DefaultValue) {
  $Value = [Environment]::GetEnvironmentVariable($Name, 'Process')
  if ([string]::IsNullOrWhiteSpace($Value)) {
    return $DefaultValue
  }
  return $Value
}

function Read-DotEnvFile([string]$Path) {
  if (-not (Test-Path -LiteralPath $Path)) {
    throw "Remote database env file is missing: $Path. Create it from config\env\remote-development.env.example."
  }
  $Values = @{}
  $LineNumber = 0
  foreach ($RawLine in Get-Content -LiteralPath $Path) {
    $LineNumber++
    $Line = [string]$RawLine
    $Trimmed = $Line.Trim()
    if ([string]::IsNullOrWhiteSpace($Trimmed) -or $Trimmed.StartsWith('#')) {
      continue
    }
    $Separator = $Line.IndexOf('=')
    if ($Separator -le 0) {
      throw "Invalid env assignment at ${Path}:$LineNumber. Expected NAME=value."
    }
    $Name = $Line.Substring(0, $Separator).Trim()
    if ($Name -notmatch '^[A-Za-z_][A-Za-z0-9_]*$') {
      throw "Invalid env variable name '$Name' at ${Path}:$LineNumber."
    }
    if ($Name -notmatch '^(ORRERY_|MINIMAX_)') {
      throw "Unsupported variable '$Name' at ${Path}:$LineNumber. Remote env files may only set ORRERY_* or MINIMAX_* values."
    }
    $Value = $Line.Substring($Separator + 1).Trim()
    if ($Value.Length -ge 2) {
      $First = $Value[0]
      $Last = $Value[$Value.Length - 1]
      if (($First -eq '"' -and $Last -eq '"') -or ($First -eq "'" -and $Last -eq "'")) {
        $Value = $Value.Substring(1, $Value.Length - 2)
      }
    }
    if ($Values.ContainsKey($Name)) {
      throw "Duplicate env variable '$Name' at ${Path}:$LineNumber."
    }
    $Values[$Name] = $Value
  }
  return $Values
}

function Get-RequiredRemoteEnvValue([hashtable]$Values, [string]$Name) {
  if (-not $Values.ContainsKey($Name) -or [string]::IsNullOrWhiteSpace([string]$Values[$Name])) {
    throw "Required remote database value '$Name' is missing from $RemoteEnvFile."
  }
  return [string]$Values[$Name]
}

function ConvertTo-RemoteDatabaseURI([string]$Name, [string]$Value, [string]$ExpectedDatabase) {
  try {
    $URI = [System.Uri]::new($Value)
  } catch {
    throw "Remote database value '$Name' is not a valid URL."
  }
  if ($URI.Scheme -notin @('postgres', 'postgresql')) {
    throw "Remote database value '$Name' must use postgres:// or postgresql://."
  }
  if ([string]::IsNullOrWhiteSpace($URI.Host) -or $URI.Host -in @('localhost', '127.0.0.1', '::1')) {
    throw "Remote database value '$Name' must use a non-loopback host; local database fallback is disabled."
  }
  $Address = $null
  if ([System.Net.IPAddress]::TryParse($URI.Host, [ref]$Address) -and [System.Net.IPAddress]::IsLoopback($Address)) {
    throw "Remote database value '$Name' must not use a loopback IP address."
  }
  if ($URI.UserInfo.Contains(':')) {
    throw "Remote database value '$Name' must not contain an inline password; use ORRERY_DATABASE_PASSWORD."
  }
  if ($URI.AbsolutePath.Trim('/') -ne $ExpectedDatabase) {
    throw "Remote database value '$Name' must target database '$ExpectedDatabase'."
  }
  if ($URI.Port -le 0) {
    throw "Remote database value '$Name' must include an explicit port."
  }
  return $URI
}

function Assert-RemoteDatabaseEndpoint([System.Uri]$URI) {
  $Client = [System.Net.Sockets.TcpClient]::new()
  try {
    $Connect = $Client.ConnectAsync($URI.Host, $URI.Port)
    if (-not $Connect.Wait(5000) -or -not $Client.Connected) {
      throw "Timed out connecting to remote PostgreSQL at $($URI.Host):$($URI.Port)."
    }
  } catch {
    throw "Remote PostgreSQL is unreachable at $($URI.Host):$($URI.Port): $($_.Exception.GetBaseException().Message)"
  } finally {
    $Client.Dispose()
  }
}

function Assert-RemoteDatabaseEnvironment([hashtable]$Values) {
  [void](Get-RequiredRemoteEnvValue $Values 'ORRERY_DATABASE_PASSWORD')
  [void](Get-RequiredRemoteEnvValue $Values 'ORRERY_BOOTSTRAP_SUPERADMIN_PASSWORD')
  $SkipBootstrap = Get-RequiredRemoteEnvValue $Values 'ORRERY_SKIP_DEFAULT_PROFILE_BOOTSTRAP'
  if (-not [string]::Equals($SkipBootstrap, 'true', [System.StringComparison]::OrdinalIgnoreCase)) {
    throw 'ORRERY_SKIP_DEFAULT_PROFILE_BOOTSTRAP must be true when using the remote database.'
  }
  $Endpoint = $null
  foreach ($Role in $RemoteDatabaseRoles) {
    $Name = 'ORRERY_DATABASE_' + $Role.ToUpperInvariant() + '_URL'
    $ExpectedDatabase = 'orrery_' + $Role
    $URI = ConvertTo-RemoteDatabaseURI $Name (Get-RequiredRemoteEnvValue $Values $Name) $ExpectedDatabase
    $CurrentEndpoint = "$($URI.Scheme)://$($URI.UserInfo)@$($URI.Host):$($URI.Port)"
    if ($null -eq $Endpoint) {
      $Endpoint = $CurrentEndpoint
    } elseif (-not [string]::Equals($Endpoint, $CurrentEndpoint, [System.StringComparison]::OrdinalIgnoreCase)) {
      throw 'All remote database URLs must use the same scheme, user, host, and port.'
    }
  }
  $CoreURI = ConvertTo-RemoteDatabaseURI 'ORRERY_DATABASE_CORE_URL' ([string]$Values['ORRERY_DATABASE_CORE_URL']) 'orrery_core'
  Assert-RemoteDatabaseEndpoint $CoreURI
  return $CoreURI
}

function Write-RemoteDevelopmentConfig([hashtable]$Values) {
  if (-not (Test-Path -LiteralPath $RemoteConfigTemplate)) {
    throw "Remote development config template is missing: $RemoteConfigTemplate"
  }
  $RenderArgs = @(
    'run', './cmd/configctl',
    '-input', $RemoteConfigTemplate,
    '-output', $RemoteRuntimeConfig,
    '-set', "database.default_url=$($Values['ORRERY_DATABASE_CORE_URL'])",
    '-set', "device_delivery.public_origin=http://127.0.0.1:$WebPort"
  )
  foreach ($Role in $RemoteDatabaseRoles) {
    $Name = 'ORRERY_DATABASE_' + $Role.ToUpperInvariant() + '_URL'
    $RenderArgs += @('-set', "database.urls.$Role=$($Values[$Name])")
  }
  & go @RenderArgs
  if ($LASTEXITCODE -ne 0) {
    throw "Failed to render remote development config with exit code $LASTEXITCODE."
  }
}

function Get-RandomLoopbackPort([int[]]$ExcludedPorts = @()) {
  for ($Attempt = 0; $Attempt -lt 20; $Attempt++) {
    $Listener = [System.Net.Sockets.TcpListener]::new([System.Net.IPAddress]::Loopback, 0)
    try {
      $Listener.Start()
      $Port = [int]$Listener.LocalEndpoint.Port
    } finally {
      $Listener.Stop()
    }
    if ($Port -notin $ExcludedPorts) {
      return $Port
    }
  }
  throw 'Unable to allocate an available loopback port.'
}

function Test-HTTPReady([string]$URL, [int]$TimeoutSeconds = 1) {
  try {
    $Response = Invoke-WebRequest -Uri $URL -Method Get -TimeoutSec $TimeoutSeconds -UseBasicParsing
    return $Response.StatusCode -ge 200 -and $Response.StatusCode -lt 300
  } catch {
    return $false
  }
}

function Resolve-EffectiveApiAddress(
  [string]$RequestedAddress,
  [bool]$WasExplicit,
  $ManagedProcess,
  $RuntimeState
) {
  if ($ManagedProcess -and $RuntimeState -and -not [string]::IsNullOrWhiteSpace([string]$RuntimeState.api_addr)) {
    return [string]$RuntimeState.api_addr
  }
  $RequestedPort = [int]($RequestedAddress.Split(':')[-1])
  if (-not (Test-PortListening $RequestedPort)) {
    return $RequestedAddress
  }
  if ($WasExplicit -or (Test-HTTPReady "http://$RequestedAddress/api/health")) {
    return $RequestedAddress
  }
  $RandomPort = Get-RandomLoopbackPort @($RequestedPort, $WebPort, 4433, 5432, 8082)
  Write-DevMessage "API port $RequestedPort is occupied but not serving Orrery; using random loopback port $RandomPort."
  return "127.0.0.1:$RandomPort"
}

function Resolve-KratosPublicPort([int]$ApiPort) {
  if (-not (Test-PortListening 4433) -or (Test-HTTPReady 'http://127.0.0.1:4433/health/ready')) {
    return 4433
  }
  $RandomPort = Get-RandomLoopbackPort @($ApiPort, $WebPort, 4433, 5432, 8082)
  Write-DevMessage "Kratos public port 4433 is occupied but not healthy; using random loopback port $RandomPort."
  return $RandomPort
}

function Resolve-DefaultDevConfig {
  if (-not $Local) {
    return $RemoteRuntimeConfig
  }
  if (Test-Path -LiteralPath $LocalDevConfig) {
    return $LocalDevConfig
  }
  return $TrackedWSLDevConfig
}

function Test-RemoteDevConfigSelected([hashtable]$Environment) {
  if (-not $Environment.ContainsKey('ORRERY_CONFIG')) {
    return $false
  }
  try {
    $ConfiguredPath = [System.IO.Path]::GetFullPath([string]$Environment['ORRERY_CONFIG'])
    $RemotePath = [System.IO.Path]::GetFullPath($RemoteRuntimeConfig)
    return [string]::Equals($ConfiguredPath, $RemotePath, [System.StringComparison]::OrdinalIgnoreCase)
  } catch {
    return $false
  }
}

function Stop-StaleRemoteModeApi([int]$Port) {
  $Owner = Get-PortOwner $Port
  if (-not $Owner) {
    return
  }
  $ProcessInfo = Get-CimInstance Win32_Process -Filter "ProcessId=$($Owner.Id)" -ErrorAction SilentlyContinue
  $CommandLine = [string]$ProcessInfo.CommandLine
  $LooksLikeOrrery = $CommandLine -match '(?i)orrery|cmd[\/]server|server\.exe'
  if (-not $LooksLikeOrrery) {
    throw "API port $Port is occupied by PID $($Owner.Id) ($($Owner.ProcessName)); remote mode refuses to reuse an unknown process."
  }
  Stop-Process -Id $Owner.Id -Force -ErrorAction Stop
  Wait-ProcessExit $Owner.Id
  Write-DevMessage "Stopped stale local Orrery API on port $Port (PID $($Owner.Id)) before remote startup."
  Remove-Item -LiteralPath $ApiPidFile, $ApiRuntimeStateFile -ErrorAction SilentlyContinue
}

function Test-RemoteRuntimeStateMatches($RuntimeState, [hashtable]$Environment) {
  if (-not $RuntimeState -or [string]$RuntimeState.database_mode -ne 'remote') {
    return $false
  }
  if (-not $Environment.ContainsKey('ORRERY_DATABASE_CORE_URL')) {
    return $false
  }
  try {
    $URI = [System.Uri]::new([string]$Environment['ORRERY_DATABASE_CORE_URL'])
    $ExpectedEndpoint = "$($URI.Host):$($URI.Port)"
  } catch {
    return $false
  }
  return [string]::Equals(
    [string]$RuntimeState.database_endpoint,
    $ExpectedEndpoint,
    [System.StringComparison]::OrdinalIgnoreCase
  )
}

function Test-DefaultDevConfigSelected([hashtable]$Environment) {
  if (-not $Environment.ContainsKey('ORRERY_CONFIG')) {
    return $false
  }
  try {
    $ConfiguredPath = [System.IO.Path]::GetFullPath([string]$Environment['ORRERY_CONFIG'])
    $DefaultPath = [System.IO.Path]::GetFullPath($DefaultDevConfig)
    return [string]::Equals($ConfiguredPath, $DefaultPath, [System.StringComparison]::OrdinalIgnoreCase)
  } catch {
    return $false
  }
}

function Test-WSLDevConfigSelected([hashtable]$Environment) {
  if (-not $Environment.ContainsKey('ORRERY_CONFIG')) {
    return $false
  }
  try {
    $ConfiguredPath = [System.IO.Path]::GetFullPath([string]$Environment['ORRERY_CONFIG'])
    $LocalPath = [System.IO.Path]::GetFullPath($LocalDevConfig)
    $TrackedPath = [System.IO.Path]::GetFullPath($TrackedWSLDevConfig)
    return (
      [string]::Equals($ConfiguredPath, $LocalPath, [System.StringComparison]::OrdinalIgnoreCase) -or
      [string]::Equals($ConfiguredPath, $TrackedPath, [System.StringComparison]::OrdinalIgnoreCase)
    )
  } catch {
    return $false
  }
}

function Resolve-IdentityCompose {
  if (Test-Path -LiteralPath $LocalIdentityCompose) {
    return $LocalIdentityCompose
  }
  if (Test-Path -LiteralPath $TrackedIdentityCompose) {
    return $TrackedIdentityCompose
  }
  throw "WSL identity dependency compose file is missing: $TrackedIdentityCompose"
}

function Invoke-WSLCommand([string]$Distro, [string[]]$ArgumentList) {
  $Output = (& wsl.exe -d $Distro -- @ArgumentList 2>&1 | Out-String).Trim()
  return @{
    ExitCode = $LASTEXITCODE
    Output = $Output
  }
}

function Invoke-WSLComposeCommand(
  [string]$Distro,
  [string]$ComposePath,
  [int]$KratosPublicPort,
  [string[]]$ArgumentList
) {
  $Args = @(
    'env', "ORRERY_KRATOS_PUBLIC_PORT=$KratosPublicPort",
    "ORRERY_OPENFGA_PUBLIC_PORT=$script:OpenFGAPublicPort",
    'docker', 'compose', '-f', $ComposePath
  ) + $ArgumentList
  return Invoke-WSLCommand $Distro $Args
}

function Ensure-WSLKeepAlive([string]$Distro) {
  $Existing = Get-AliveProcess $WSLKeepAlivePidFile
  if ($Existing -and $Existing.ProcessName -eq 'wsl') {
    return
  }
  Remove-Item -LiteralPath $WSLKeepAlivePidFile -ErrorAction SilentlyContinue
  $Process = Start-Process `
    -FilePath 'wsl.exe' `
    -ArgumentList @('-d', $Distro, '--', 'tail', '-f', '/dev/null') `
    -WindowStyle Hidden `
    -PassThru
  Start-Sleep -Milliseconds 500
  $Process.Refresh()
  if ($Process.HasExited) {
    throw "Unable to keep WSL distribution '$Distro' running."
  }
  Set-Content -LiteralPath $WSLKeepAlivePidFile -Value $Process.Id -Encoding ASCII
  Write-DevMessage "WSL keepalive started for $Distro (PID $($Process.Id))."
}

function Stop-WSLKeepAlive {
  $Process = Get-AliveProcess $WSLKeepAlivePidFile
  if ($Process -and $Process.ProcessName -eq 'wsl') {
    Stop-Process -Id $Process.Id -Force -ErrorAction SilentlyContinue
    Wait-ProcessExit $Process.Id
    Write-DevMessage "WSL keepalive stopped (PID $($Process.Id))."
  }
  Remove-Item -LiteralPath $WSLKeepAlivePidFile -ErrorAction SilentlyContinue
}

function Ensure-WSLPostgres([string]$Distro, [string]$Container) {
  $Inspect = Invoke-WSLCommand $Distro @('docker', 'container', 'inspect', $Container)
  if ($Inspect.ExitCode -ne 0) {
    $Image = Get-ProcessEnvironmentOrDefault 'ORRERY_WSL_POSTGRES_IMAGE' $DefaultWSLPostgresImage
    $Volume = Get-ProcessEnvironmentOrDefault 'ORRERY_WSL_POSTGRES_VOLUME' $DefaultWSLPostgresVolume
    $User = Get-ProcessEnvironmentOrDefault 'ORRERY_WSL_POSTGRES_USER' $DefaultWSLPostgresUser
    $Password = Get-ProcessEnvironmentOrDefault 'ORRERY_WSL_POSTGRES_PASSWORD' $DefaultWSLPostgresPassword
    $Database = Get-ProcessEnvironmentOrDefault 'ORRERY_WSL_POSTGRES_DATABASE' $DefaultWSLPostgresDatabase
    $CreateArgs = @(
      'docker', 'create', '--name', $Container, '--restart', 'unless-stopped',
      '-e', "POSTGRES_USER=$User",
      '-e', "POSTGRES_PASSWORD=$Password",
      '-e', "POSTGRES_DB=$Database",
      '-p', '127.0.0.1:5432:5432',
      '-v', "${Volume}:/var/lib/postgresql/data",
      $Image
    )
    $Create = Invoke-WSLCommand $Distro $CreateArgs
    if ($Create.ExitCode -ne 0) {
      throw "Unable to create WSL PostgreSQL container '$Container': $($Create.Output)"
    }
  }

  $PortBindingResult = Invoke-WSLCommand $Distro @(
    'docker', 'container', 'inspect', '--format', '{{json .HostConfig.PortBindings}}', $Container
  )
  if ($PortBindingResult.ExitCode -ne 0) {
    throw "Unable to inspect WSL PostgreSQL container '$Container' port bindings: $($PortBindingResult.Output)"
  }
  try {
    $PortBindings = $PortBindingResult.Output | ConvertFrom-Json
    $ExpectedBinding = @($PortBindings.'5432/tcp') | Where-Object {
      [string]$_.HostIp -eq '127.0.0.1' -and [string]$_.HostPort -eq '5432'
    } | Select-Object -First 1
  } catch {
    throw "WSL PostgreSQL container '$Container' has invalid port bindings: $($PortBindingResult.Output)"
  }
  if (-not $ExpectedBinding) {
    throw "WSL PostgreSQL container '$Container' must publish 127.0.0.1:5432 to container port 5432."
  }

  $Start = Invoke-WSLCommand $Distro @('docker', 'start', $Container)
  if ($Start.ExitCode -ne 0) {
    throw "Unable to start WSL PostgreSQL container '$Container': $($Start.Output)"
  }
}

function Wait-WSLPostgresReady([string]$Distro, [string]$Container, [int]$TimeoutSeconds = 60) {
  $User = Get-ProcessEnvironmentOrDefault 'ORRERY_WSL_POSTGRES_USER' $DefaultWSLPostgresUser
  $Database = Get-ProcessEnvironmentOrDefault 'ORRERY_WSL_POSTGRES_DATABASE' $DefaultWSLPostgresDatabase
  $Deadline = (Get-Date).AddSeconds($TimeoutSeconds)
  $LastOutput = ''
  while ((Get-Date) -lt $Deadline) {
    $Probe = Invoke-WSLCommand $Distro @('docker', 'exec', $Container, 'pg_isready', '-U', $User, '-d', $Database)
    $LastOutput = $Probe.Output
    if ($Probe.ExitCode -eq 0) {
      Write-DevMessage "WSL PostgreSQL ready in container $Container."
      return
    }
    Start-Sleep -Milliseconds 500
  }
  throw "WSL PostgreSQL container '$Container' did not become ready within $TimeoutSeconds seconds: $LastOutput"
}

function Wait-TCPDependencyReady(
  [string]$HostName,
  [int]$Port,
  [string]$Name,
  [int]$TimeoutSeconds = 60
) {
  $Deadline = (Get-Date).AddSeconds($TimeoutSeconds)
  while ((Get-Date) -lt $Deadline) {
    $Client = [System.Net.Sockets.TcpClient]::new()
    try {
      $Connect = $Client.ConnectAsync($HostName, $Port)
      if ($Connect.Wait(2000) -and $Client.Connected) {
        Write-DevMessage "$Name ready at ${HostName}:${Port}."
        return
      }
    } catch {
    } finally {
      $Client.Dispose()
    }
    Start-Sleep -Milliseconds 500
  }
  throw "$Name did not become ready at ${HostName}:${Port} within $TimeoutSeconds seconds."
}

function Wait-HTTPDependencyReady([string]$URL, [string]$Name, [int]$TimeoutSeconds = 60) {
  $Deadline = (Get-Date).AddSeconds($TimeoutSeconds)
  $LastError = ''
  while ((Get-Date) -lt $Deadline) {
    try {
      $Response = Invoke-WebRequest -Uri $URL -Method Get -TimeoutSec 2 -UseBasicParsing
      if ($Response.StatusCode -ge 200 -and $Response.StatusCode -lt 300) {
        Write-DevMessage "$Name ready at $URL."
        return
      }
      $LastError = "HTTP $($Response.StatusCode)"
    } catch {
      $LastError = $_.Exception.Message
    }
    Start-Sleep -Milliseconds 500
  }
  throw "$Name did not become ready at $URL within $TimeoutSeconds seconds: $LastError"
}

function Invoke-OpenFGARequest([string]$Method, [string]$Path, [string]$Body = '') {
  $Request = @{
    Uri = $script:OpenFGAApiURL + $Path
    Method = $Method
    TimeoutSec = 10
  }
  if (-not [string]::IsNullOrWhiteSpace($Body)) {
    $Request['ContentType'] = 'application/json'
    $Request['Body'] = $Body
  }
  return Invoke-RestMethod @Request
}

function Read-OpenFGAState {
  if (-not (Test-Path -LiteralPath $OpenFGAStateFile)) {
    return $null
  }
  try {
    return Get-Content -Raw -LiteralPath $OpenFGAStateFile | ConvertFrom-Json
  } catch {
    Write-Warning "Ignoring invalid OpenFGA development state at ${OpenFGAStateFile}: $($_.Exception.Message)"
    return $null
  }
}

function Ensure-WSLOpenFGAConfiguration {
  if (-not (Test-Path -LiteralPath $OpenFGAModelPath)) {
    throw "OpenFGA development authorization model is missing: $OpenFGAModelPath"
  }

  $StoresResponse = Invoke-OpenFGARequest 'Get' '/stores'
  $Store = @($StoresResponse.stores) |
    Where-Object { [string]::Equals([string]$_.name, $OpenFGAStoreName, [System.StringComparison]::Ordinal) } |
    Select-Object -First 1
  if ($null -eq $Store) {
    $StoreBody = @{ name = $OpenFGAStoreName } | ConvertTo-Json -Compress
    $Store = Invoke-OpenFGARequest 'Post' '/stores' $StoreBody
    Write-DevMessage "OpenFGA development store '$OpenFGAStoreName' created."
  }
  $StoreID = [string]$Store.id
  if ([string]::IsNullOrWhiteSpace($StoreID)) {
    throw "OpenFGA development store '$OpenFGAStoreName' did not return an id."
  }

  $ModelHash = (Get-FileHash -LiteralPath $OpenFGAModelPath -Algorithm SHA256).Hash.ToLowerInvariant()
  $State = Read-OpenFGAState
  $ModelID = ''
  if (
    $null -ne $State -and
    [string]::Equals([string]$State.store_id, $StoreID, [System.StringComparison]::Ordinal) -and
    [string]::Equals([string]$State.model_hash, $ModelHash, [System.StringComparison]::OrdinalIgnoreCase) -and
    -not [string]::IsNullOrWhiteSpace([string]$State.authorization_model_id)
  ) {
    try {
      $ModelID = [string]$State.authorization_model_id
      $null = Invoke-OpenFGARequest 'Get' "/stores/$StoreID/authorization-models/$ModelID"
    } catch {
      $ModelID = ''
    }
  }
  if ([string]::IsNullOrWhiteSpace($ModelID)) {
    $ModelBody = Get-Content -Raw -LiteralPath $OpenFGAModelPath
    $Model = Invoke-OpenFGARequest 'Post' "/stores/$StoreID/authorization-models" $ModelBody
    $ModelID = [string]$Model.authorization_model_id
    if ([string]::IsNullOrWhiteSpace($ModelID)) {
      throw 'OpenFGA development authorization model did not return an id.'
    }
    [ordered]@{
      store_id = $StoreID
      authorization_model_id = $ModelID
      model_hash = $ModelHash
      recorded_at = (Get-Date -Format o)
    } | ConvertTo-Json | Set-Content -LiteralPath $OpenFGAStateFile -Encoding UTF8
    Write-DevMessage 'OpenFGA development authorization model published.'
  }

  return @{
    'authz.mode' = 'openfga_enforce'
    'authz.role_scope.mode' = 'authoritative'
    'authz.openfga.api_url' = $script:OpenFGAApiURL
    'authz.openfga.store_id' = $StoreID
    'authz.openfga.authorization_model_id' = $ModelID
    'authz.openfga.ignore_duplicate_writes' = 'true'
    'authz.openfga.enforce_ready' = 'true'
    'authz.relation_backfill.mode' = 'write'
  }
}

function Resolve-OpenFGAPublicPort([int]$ApiPort, [int]$KratosPublicPort) {
  if (-not (Test-PortListening $OpenFGADefaultPort)) {
    return $OpenFGADefaultPort
  }
  $RandomPort = Get-RandomLoopbackPort @($ApiPort, $WebPort, 4433, $KratosPublicPort, 5432, $OpenFGADefaultPort)
  Write-DevMessage "OpenFGA public port $OpenFGADefaultPort is occupied; using random loopback port $RandomPort."
  return $RandomPort
}

function Write-WSLDevelopmentConfig(
  [int]$KratosPublicPort,
  [hashtable]$OpenFGAConfig
) {
  $RenderArgs = @(
    'run', './cmd/configctl',
    '-input', $TrackedWSLDevConfig,
    '-output', $LocalDevConfig,
    '-set', "auth.kratos.public_url=http://127.0.0.1:$KratosPublicPort",
    '-set', "device_delivery.public_origin=http://127.0.0.1:$WebPort",
    '-set', "authz.mode=$($OpenFGAConfig['authz.mode'])",
    '-set', "authz.role_scope.mode=$($OpenFGAConfig['authz.role_scope.mode'])",
    '-set', "authz.openfga.api_url=$($OpenFGAConfig['authz.openfga.api_url'])",
    '-set', "authz.openfga.store_id=$($OpenFGAConfig['authz.openfga.store_id'])",
    '-set', "authz.openfga.authorization_model_id=$($OpenFGAConfig['authz.openfga.authorization_model_id'])",
    '-set', "authz.openfga.ignore_duplicate_writes=$($OpenFGAConfig['authz.openfga.ignore_duplicate_writes'])",
    '-set', "authz.openfga.enforce_ready=$($OpenFGAConfig['authz.openfga.enforce_ready'])",
    '-set', "authz.relation_backfill.mode=$($OpenFGAConfig['authz.relation_backfill.mode'])"
  )
  & go @RenderArgs
  if ($LASTEXITCODE -ne 0) {
    throw "Failed to render WSL development config with exit code $LASTEXITCODE."
  }
}

function ConvertFrom-WSLKratosIdentityOutput([string]$Output) {
  $JsonLine = $Output -split "`r?`n" |
    Where-Object { $_.TrimStart().StartsWith('{') -or $_.TrimStart().StartsWith('[') } |
    Select-Object -Last 1
  if ([string]::IsNullOrWhiteSpace($JsonLine)) {
    throw "Kratos CLI output did not contain JSON: $Output"
  }
  try {
    $Parsed = $JsonLine | ConvertFrom-Json
  } catch {
    throw "Kratos CLI returned invalid JSON: $JsonLine"
  }
  if ($Parsed.PSObject.Properties.Name -contains 'identities') {
    return @($Parsed.identities)
  }
  return @($Parsed)
}

function Get-WSLDevelopmentIdentities(
  [string]$Distro,
  [string]$ComposePath,
  [int]$KratosPublicPort
) {
  $List = Invoke-WSLComposeCommand $Distro $ComposePath $KratosPublicPort @(
    'exec', '-T', 'kratos',
    'kratos', 'list', 'identities',
    '--endpoint', 'http://127.0.0.1:4434',
    '--format', 'json',
    '--consistency', 'strong'
  )
  if ($List.ExitCode -ne 0) {
    throw "Unable to inspect Kratos development identities: $($List.Output)"
  }
  return @(ConvertFrom-WSLKratosIdentityOutput $List.Output)
}

function Remove-WSLDevelopmentIdentity(
  [string]$Distro,
  [string]$ComposePath,
  [int]$KratosPublicPort,
  [string]$IdentityID,
  [string]$Username
) {
  $Delete = Invoke-WSLComposeCommand $Distro $ComposePath $KratosPublicPort @(
    'exec', '-T', 'kratos',
    'kratos', 'delete', 'identity', $IdentityID,
    '--endpoint', 'http://127.0.0.1:4434'
  )
  if ($Delete.ExitCode -ne 0) {
    throw "Unable to remove Kratos development identity '$Username': $($Delete.Output)"
  }
}

function Remove-WSLLegacyDevelopmentIdentity(
  [string]$Distro,
  [string]$ComposePath,
  [int]$KratosPublicPort
) {
  $Identities = Get-WSLDevelopmentIdentities $Distro $ComposePath $KratosPublicPort
  foreach ($Identity in $Identities) {
    if (
      [string]$Identity.traits.username -ne $LegacyWSLKratosUsername -or
      [string]$Identity.traits.agent_profile_id -ne $LegacyWSLKratosAgentProfileID
    ) {
      continue
    }
    Remove-WSLDevelopmentIdentity $Distro $ComposePath $KratosPublicPort ([string]$Identity.id) $LegacyWSLKratosUsername
    Write-DevMessage "Legacy Kratos development identity '$LegacyWSLKratosUsername' removed."
  }
}

function Ensure-WSLDevelopmentIdentity(
  [string]$Distro,
  [string]$ComposePath,
  [int]$KratosPublicPort
) {
  $Username = Get-ProcessEnvironmentOrDefault 'ORRERY_WSL_KRATOS_USERNAME' $DefaultWSLKratosUsername
  $Password = Get-ProcessEnvironmentOrDefault 'ORRERY_WSL_KRATOS_PASSWORD' $DefaultWSLKratosPassword
  $AgentProfileID = Get-ProcessEnvironmentOrDefault 'ORRERY_WSL_KRATOS_AGENT_PROFILE_ID' $DefaultWSLKratosAgentProfileID
  $DisplayName = Get-ProcessEnvironmentOrDefault 'ORRERY_WSL_KRATOS_DISPLAY_NAME' $DefaultWSLKratosDisplayName
  $Email = "$Username@orrery.local"
  $AdminURL = 'http://127.0.0.1:4434/admin/identities'
  $Identities = Get-WSLDevelopmentIdentities $Distro $ComposePath $KratosPublicPort
  $Existing = $Identities | Where-Object { [string]$_.traits.username -eq $Username } | Select-Object -First 1
  if ($Existing) {
    if (
      [string]$Existing.traits.agent_profile_id -ne $AgentProfileID -or
      [string]$Existing.traits.display_name -ne $DisplayName -or
      [string]$Existing.traits.email -ne $Email
    ) {
      Remove-WSLDevelopmentIdentity $Distro $ComposePath $KratosPublicPort ([string]$Existing.id) $Username
      Write-DevMessage "Kratos development identity '$Username' had stale traits and will be reprovisioned."
    } else {
      Write-DevMessage "Kratos development identity '$Username' ready."
      Write-DevMessage "Kratos development credentials: username '$Username', password '$Password'."
      return
    }
  }

  $Payload = [ordered]@{
    schema_id = 'default'
    state = 'active'
    traits = [ordered]@{
      username = $Username
      email = $Email
      display_name = $DisplayName
      agent_profile_id = $AgentProfileID
    }
    credentials = [ordered]@{
      password = [ordered]@{
        config = [ordered]@{
          password = $Password
        }
      }
    }
  }
  $Body = $Payload | ConvertTo-Json -Depth 8 -Compress
  $Create = Invoke-WSLComposeCommand $Distro $ComposePath $KratosPublicPort @(
    'exec', '-T', 'kratos',
    'wget', '-q', '-O', '-',
    '--header', 'Content-Type: application/json',
    '--post-data', $Body,
    $AdminURL
  )
  if ($Create.ExitCode -ne 0) {
    throw "Unable to provision Kratos development identity '$Username': $($Create.Output)"
  }
  Write-DevMessage "Kratos development identity '$Username' provisioned."
  Write-DevMessage "Kratos development credentials: username '$Username', password '$Password'."
}

function Wait-WSLKratosAdminReady(
  [string]$Distro,
  [string]$ComposePath,
  [int]$KratosPublicPort,
  [int]$TimeoutSeconds = 60
) {
  $Deadline = (Get-Date).AddSeconds($TimeoutSeconds)
  $LastOutput = ''
  while ((Get-Date) -lt $Deadline) {
    $Probe = Invoke-WSLComposeCommand $Distro $ComposePath $KratosPublicPort @(
      'exec', '-T', 'kratos',
      'wget', '-q', '--spider', '-T', '2',
      'http://127.0.0.1:4434/health/ready'
    )
    $LastOutput = $Probe.Output
    if ($Probe.ExitCode -eq 0) {
      Write-DevMessage 'Kratos admin API ready inside its container.'
      return
    }
    Start-Sleep -Milliseconds 500
  }
  throw "Kratos admin API did not become ready inside its container within $TimeoutSeconds seconds: $LastOutput"
}

function ConvertTo-WSLPath([string]$Path) {
  $FullPath = [System.IO.Path]::GetFullPath($Path)
  $RootPath = [System.IO.Path]::GetPathRoot($FullPath)
  if ($RootPath -notmatch '^([A-Za-z]):\\$') {
    throw "Only drive-qualified Windows paths can be converted to WSL paths: $Path"
  }
  $Drive = $Matches[1].ToLowerInvariant()
  $RelativePath = $FullPath.Substring($RootPath.Length).Replace('\', '/')
  return "/mnt/$Drive/$RelativePath"
}

function Ensure-WSLDependencies([int]$KratosPublicPort) {
  $Distro = Get-ProcessEnvironmentOrDefault 'ORRERY_WSL_DISTRO' $DefaultWSLDistro
  $PostgresContainer = Get-ProcessEnvironmentOrDefault 'ORRERY_WSL_POSTGRES_CONTAINER' $DefaultWSLPostgresContainer
  $IdentityCompose = Resolve-IdentityCompose
  Write-DevMessage "Starting WSL development dependencies in $Distro..."

  Ensure-WSLKeepAlive $Distro
  Ensure-WSLPostgres $Distro $PostgresContainer
  Wait-WSLPostgresReady $Distro $PostgresContainer
  Wait-TCPDependencyReady '127.0.0.1' 5432 'WSL PostgreSQL host relay'

  $WSLComposePath = ConvertTo-WSLPath $IdentityCompose
  $Compose = Invoke-WSLComposeCommand $Distro $WSLComposePath $KratosPublicPort @('up', '-d', '--pull', 'missing')
  if ($Compose.ExitCode -ne 0) {
    throw "Unable to start WSL identity dependencies: $($Compose.Output)"
  }

  Wait-HTTPDependencyReady "http://127.0.0.1:$KratosPublicPort/health/ready" 'Kratos public API'
  Wait-WSLKratosAdminReady $Distro $WSLComposePath $KratosPublicPort
  Remove-WSLLegacyDevelopmentIdentity $Distro $WSLComposePath $KratosPublicPort
  Ensure-WSLDevelopmentIdentity $Distro $WSLComposePath $KratosPublicPort
  Wait-HTTPDependencyReady "$script:OpenFGAApiURL/healthz" 'OpenFGA API'
  return Ensure-WSLOpenFGAConfiguration
}

function Invoke-WithProcessEnvironment([hashtable]$Environment, [scriptblock]$Script) {
  $PreviousValues = @{}
  foreach ($Key in $Environment.Keys) {
    $PreviousValues[$Key] = [Environment]::GetEnvironmentVariable($Key, 'Process')
    [Environment]::SetEnvironmentVariable($Key, [string]$Environment[$Key], 'Process')
  }
  try {
    & $Script
  } finally {
    foreach ($Key in $PreviousValues.Keys) {
      [Environment]::SetEnvironmentVariable($Key, $PreviousValues[$Key], 'Process')
    }
  }
}

function Assert-DevPostgresIdentifier([string]$Name, [string]$Value) {
  if ($Value -notmatch '^[A-Za-z_][A-Za-z0-9_]*$') {
    throw "$Name must be a PostgreSQL identifier, got '$Value'."
  }
}

function ConvertTo-PostgresLiteral([string]$Value) {
  return "'" + $Value.Replace("'", "''") + "'"
}

function Invoke-DevPostgresTool(
  [string]$FilePath,
  [string[]]$ArgumentList,
  [string]$Password
) {
  $PreviousPassword = [Environment]::GetEnvironmentVariable('PGPASSWORD', 'Process')
  try {
    [Environment]::SetEnvironmentVariable('PGPASSWORD', $Password, 'Process')
    $Output = & $FilePath @ArgumentList 2>&1
    return @{
      ExitCode = $LASTEXITCODE
      Output = ($Output | Out-String).Trim()
    }
  } finally {
    [Environment]::SetEnvironmentVariable('PGPASSWORD', $PreviousPassword, 'Process')
  }
}

function Invoke-DevPostgresSQL(
  [string]$PsqlPath,
  [string]$HostValue,
  [int]$Port,
  [string]$User,
  [string]$Password,
  [string]$Database,
  [string]$SQL
) {
  return Invoke-DevPostgresTool `
    -FilePath $PsqlPath `
    -ArgumentList @('-w', '-h', $HostValue, '-p', [string]$Port, '-U', $User, '-d', $Database, '-v', 'ON_ERROR_STOP=1', '-q', '-t', '-A', '-c', $SQL) `
    -Password $Password
}

function Test-DevPostgresAccess(
  [string]$PsqlPath,
  [string]$HostValue,
  [int]$Port,
  [string]$User,
  [string]$Password,
  [string[]]$DatabaseNames
) {
  foreach ($DatabaseName in $DatabaseNames) {
    $Result = Invoke-DevPostgresSQL $PsqlPath $HostValue $Port $User $Password $DatabaseName 'SELECT 1'
    if ($Result.ExitCode -ne 0 -or $Result.Output.Trim() -ne '1') {
      return $false
    }
  }
  return $true
}

function Clear-StaleDevPostgresPid([string]$DataRoot) {
  $PostmasterPid = Join-Path $DataRoot 'postmaster.pid'
  if (-not (Test-Path -LiteralPath $PostmasterPid)) {
    return
  }
  $Lines = Get-Content -LiteralPath $PostmasterPid -ErrorAction SilentlyContinue
  if (-not $Lines -or $Lines.Count -eq 0) {
    Remove-Item -LiteralPath $PostmasterPid -Force -ErrorAction SilentlyContinue
    return
  }
  $ProcessId = 0
  if (-not [int]::TryParse([string]$Lines[0], [ref]$ProcessId)) {
    Remove-Item -LiteralPath $PostmasterPid -Force -ErrorAction SilentlyContinue
    return
  }
  $Process = Get-Process -Id $ProcessId -ErrorAction SilentlyContinue
  if ($Process -and $Process.ProcessName -like 'postgres*') {
    return
  }
  Remove-Item -LiteralPath $PostmasterPid -Force -ErrorAction SilentlyContinue
}

function Test-DevPostgresListening(
  [string]$PgIsReadyPath,
  [string]$HostValue,
  [int]$Port,
  [string]$User
) {
  $Output = & $PgIsReadyPath -h $HostValue -p ([string]$Port) -U $User 2>&1
  return $LASTEXITCODE -eq 0
}

function Wait-DevPostgresReady(
  [string]$PgIsReadyPath,
  [string]$HostValue,
  [int]$Port,
  [string]$User,
  [string]$LogPath,
  [int]$TimeoutSeconds = 45
) {
  $Deadline = (Get-Date).AddSeconds($TimeoutSeconds)
  $LastOutput = ''
  while ((Get-Date) -lt $Deadline) {
    $Output = & $PgIsReadyPath -h $HostValue -p ([string]$Port) -U $User 2>&1
    $LastOutput = ($Output | Out-String).Trim()
    if ($LASTEXITCODE -eq 0) {
      return
    }
    Start-Sleep -Milliseconds 500
  }

  $Tail = ''
  if (Test-Path -LiteralPath $LogPath) {
    $Tail = (Get-Content -LiteralPath $LogPath -Tail 40 -ErrorAction SilentlyContinue | Out-String).Trim()
  }
  throw "Timed out waiting for PostgreSQL to accept connections at ${HostValue}:${Port}. Last pg_isready output: $LastOutput`nPostgreSQL log tail:`n$Tail"
}

function Start-DevPostgresServer(
  [string]$PgCtlPath,
  [string]$PgIsReadyPath,
  [string]$DataRoot,
  [string]$HostValue,
  [int]$Port,
  [string]$User
) {
  Write-DevMessage "Checking PostgreSQL at $DataRoot..."
  $LogPath = Join-Path $RunRoot 'postgres.log'
  if (Test-DevPostgresListening $PgIsReadyPath $HostValue $Port $User) {
    Write-DevMessage "PostgreSQL already accepting connections at ${HostValue}:${Port}."
    return
  }

  $Status = & $PgCtlPath status -D $DataRoot 2>&1
  if ($LASTEXITCODE -eq 0) {
    Write-DevMessage "PostgreSQL already running."
    Wait-DevPostgresReady $PgIsReadyPath $HostValue $Port $User $LogPath
    return
  }
  Clear-StaleDevPostgresPid $DataRoot
  Write-DevMessage "Starting PostgreSQL; log: $LogPath"
  try {
    $Process = Start-Process `
      -FilePath $PgCtlPath `
      -ArgumentList @('start', '-D', $DataRoot, '-l', $LogPath, '-W') `
      -WindowStyle Hidden `
      -PassThru
    Write-DevMessage "PostgreSQL start command launched (PID $($Process.Id))."
  } catch {
    throw "Failed to launch PostgreSQL start command from '$DataRoot': $($_.Exception.Message)"
  }
  Wait-DevPostgresReady $PgIsReadyPath $HostValue $Port $User $LogPath
  Write-DevMessage "PostgreSQL ready."
}

function Ensure-DevPostgresDatabases(
  [string]$PsqlPath,
  [string]$CreatedbPath,
  [string]$HostValue,
  [int]$Port,
  [string]$AppUser,
  [string]$AppPassword,
  [string]$AdminUser,
  [string]$AdminPassword,
  [string[]]$DatabaseNames
) {
  Write-DevMessage "Checking PostgreSQL databases..."
  if (Test-DevPostgresAccess $PsqlPath $HostValue $Port $AppUser $AppPassword $DatabaseNames) {
    Write-DevMessage "PostgreSQL databases ready."
    return
  }

  Write-DevMessage "Provisioning PostgreSQL role and databases..."
  $RoleLiteral = ConvertTo-PostgresLiteral $AppUser
  $PasswordLiteral = ConvertTo-PostgresLiteral $AppPassword
  $RoleSQL = "DO `$`$ BEGIN IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = $RoleLiteral) THEN CREATE ROLE $AppUser LOGIN PASSWORD $PasswordLiteral CREATEDB; ELSE ALTER ROLE $AppUser LOGIN PASSWORD $PasswordLiteral CREATEDB; END IF; END `$`$;"
  $RoleResult = Invoke-DevPostgresSQL $PsqlPath $HostValue $Port $AdminUser $AdminPassword 'postgres' $RoleSQL
  if ($RoleResult.ExitCode -ne 0) {
    throw "Cannot provision PostgreSQL role '$AppUser'. Set ORRERY_DEV_POSTGRES_ADMIN_USER and ORRERY_DEV_POSTGRES_ADMIN_PASSWORD if the local cluster does not use postgres/postgres. $($RoleResult.Output)"
  }

  foreach ($DatabaseName in $DatabaseNames) {
    $DatabaseLiteral = ConvertTo-PostgresLiteral $DatabaseName
    $Exists = Invoke-DevPostgresSQL $PsqlPath $HostValue $Port $AdminUser $AdminPassword 'postgres' "SELECT 1 FROM pg_database WHERE datname = $DatabaseLiteral"
    if ($Exists.ExitCode -ne 0) {
      throw "Cannot inspect PostgreSQL database '$DatabaseName': $($Exists.Output)"
    }
    if ($Exists.Output.Trim() -ne '1') {
      $CreateResult = Invoke-DevPostgresTool `
        -FilePath $CreatedbPath `
        -ArgumentList @('-w', '-h', $HostValue, '-p', [string]$Port, '-U', $AdminUser, '-O', $AppUser, $DatabaseName) `
        -Password $AdminPassword
      if ($CreateResult.ExitCode -ne 0) {
        throw "Cannot create PostgreSQL database '$DatabaseName': $($CreateResult.Output)"
      }
    }
    $OwnerResult = Invoke-DevPostgresSQL $PsqlPath $HostValue $Port $AdminUser $AdminPassword 'postgres' "ALTER DATABASE $DatabaseName OWNER TO $AppUser"
    if ($OwnerResult.ExitCode -ne 0) {
      throw "Cannot assign PostgreSQL database '$DatabaseName' to '$AppUser': $($OwnerResult.Output)"
    }
  }

  if (-not (Test-DevPostgresAccess $PsqlPath $HostValue $Port $AppUser $AppPassword $DatabaseNames)) {
    throw "PostgreSQL started but the '$AppUser' role cannot access all Orrery development databases."
  }
  Write-DevMessage "PostgreSQL databases ready."
}

function Ensure-DevPostgres {
  $PostgresHome = Get-ProcessEnvironmentOrDefault 'ORRERY_DEV_POSTGRES_HOME' $DefaultDevPostgresHome
  $DataRoot = Get-ProcessEnvironmentOrDefault 'ORRERY_DEV_POSTGRES_DATA' $DefaultDevPostgresData
  $HostValue = Get-ProcessEnvironmentOrDefault 'ORRERY_DEV_POSTGRES_HOST' $DefaultDevPostgresHost
  $Port = [int](Get-ProcessEnvironmentOrDefault 'ORRERY_DEV_POSTGRES_PORT' ([string]$DefaultDevPostgresPort))
  $AppUser = Get-ProcessEnvironmentOrDefault 'ORRERY_DEV_POSTGRES_USER' $DefaultDevPostgresUser
  $AppPassword = Get-ProcessEnvironmentOrDefault 'ORRERY_DEV_POSTGRES_PASSWORD' $DefaultDevPostgresPassword
  $AdminUser = Get-ProcessEnvironmentOrDefault 'ORRERY_DEV_POSTGRES_ADMIN_USER' $DefaultDevPostgresAdminUser
  $AdminPassword = Get-ProcessEnvironmentOrDefault 'ORRERY_DEV_POSTGRES_ADMIN_PASSWORD' $DefaultDevPostgresAdminPassword
  $DatabaseName = Get-ProcessEnvironmentOrDefault 'ORRERY_DEV_POSTGRES_DATABASE' $DefaultDevPostgresDatabase

  Assert-DevPostgresIdentifier 'ORRERY_DEV_POSTGRES_USER' $AppUser
  Assert-DevPostgresIdentifier 'ORRERY_DEV_POSTGRES_ADMIN_USER' $AdminUser
  Assert-DevPostgresIdentifier 'ORRERY_DEV_POSTGRES_DATABASE' $DatabaseName

  $BinRoot = Join-Path $PostgresHome 'bin'
  $PgCtlPath = Join-Path $BinRoot 'pg_ctl.exe'
  $PgIsReadyPath = Join-Path $BinRoot 'pg_isready.exe'
  $PsqlPath = Join-Path $BinRoot 'psql.exe'
  $CreatedbPath = Join-Path $BinRoot 'createdb.exe'
  foreach ($Path in @($PgCtlPath, $PgIsReadyPath, $PsqlPath, $CreatedbPath)) {
    if (-not (Test-Path -LiteralPath $Path)) {
      throw "Required PostgreSQL tool not found: $Path"
    }
  }
  if (-not (Test-Path -LiteralPath $DataRoot)) {
    throw "PostgreSQL data directory not found: $DataRoot"
  }

  Start-DevPostgresServer $PgCtlPath $PgIsReadyPath $DataRoot $HostValue $Port $AdminUser
  Ensure-DevPostgresDatabases $PsqlPath $CreatedbPath $HostValue $Port $AppUser $AppPassword $AdminUser $AdminPassword @($DatabaseName)
}

function Initialize-DevDatabases([hashtable]$Environment) {
  $GoPath = Resolve-Tool 'go'
  $ArgumentList = @('run', './cmd/server', '-init-db')
  if ($Environment.ContainsKey('ORRERY_CONFIG')) {
    $ArgumentList += @('-config', [string]$Environment['ORRERY_CONFIG'])
  }
  $StartedAt = Get-Date -Format o
  Add-SharedLogText $ApiOut "`n== Database init $StartedAt ==`n"
  Write-DevMessage "Initializing configured databases..."
  Invoke-WithProcessEnvironment $Environment {
    $Output = & $GoPath @ArgumentList 2>&1
    $ExitCode = $LASTEXITCODE
    $Text = ($Output | Out-String)
    if (-not [string]::IsNullOrWhiteSpace($Text)) {
      Add-SharedLogText $ApiOut $Text
      $Text.TrimEnd() -split "`r?`n" | ForEach-Object {
        if (-not [string]::IsNullOrWhiteSpace($_)) {
          Write-DevMessage "  $_"
        }
      }
      if (-not $Text.EndsWith("`n")) {
        Add-SharedLogText $ApiOut "`n"
      }
    }
    if ($ExitCode -ne 0) {
      throw "Database initialization failed with exit code $ExitCode. See $ApiOut for details."
    }
  }
  Write-DevMessage "Database initialization completed."
}

function New-ApiEnvironment {
  $Environment = @{}
  $PiBin = Join-Path $PiCodingAgentRoot 'node_modules\.bin'
  $ProcessPath = [Environment]::GetEnvironmentVariable('PATH', 'Process')
  $Environment['PATH'] = $PiBin + [System.IO.Path]::PathSeparator + $ProcessPath
  $MiniMaxCNAPIKey = [Environment]::GetEnvironmentVariable('MINIMAX_CN_API_KEY', 'Process')
  if ([string]::IsNullOrWhiteSpace($MiniMaxCNAPIKey) -and (Test-Path -LiteralPath $MiniMaxTokenPlanSecret)) {
    $MiniMaxCNAPIKey = (Get-Content -Raw -LiteralPath $MiniMaxTokenPlanSecret).Trim()
  }
  if (-not [string]::IsNullOrWhiteSpace($MiniMaxCNAPIKey)) {
    $Environment['MINIMAX_CN_API_KEY'] = $MiniMaxCNAPIKey
  }

  if (-not $Local) {
    $RemoteValues = Read-DotEnvFile $RemoteEnvFile
    [void](Assert-RemoteDatabaseEnvironment $RemoteValues)
    Write-RemoteDevelopmentConfig $RemoteValues
    foreach ($Key in $RemoteValues.Keys) {
      $Value = [string]$RemoteValues[$Key]
      if (-not [string]::IsNullOrWhiteSpace($Value)) {
        $Environment[$Key] = $Value
      }
    }
    $Environment['ORRERY_CONFIG'] = $RemoteRuntimeConfig
    return $Environment
  }

  foreach ($Key in @('ORRERY_CONFIG', 'ORRERY_DATABASE_PASSWORD')) {
    $Value = [Environment]::GetEnvironmentVariable($Key, 'Process')
    if (-not [string]::IsNullOrWhiteSpace($Value)) {
      $Environment[$Key] = $Value
    }
  }

  if (-not $Environment.ContainsKey('ORRERY_CONFIG')) {
    $Environment['ORRERY_CONFIG'] = Resolve-DefaultDevConfig
  }
  if (-not $Environment.ContainsKey('ORRERY_DATABASE_PASSWORD')) {
    if (Test-WSLDevConfigSelected $Environment) {
      $Environment['ORRERY_DATABASE_PASSWORD'] = Get-ProcessEnvironmentOrDefault 'ORRERY_WSL_POSTGRES_PASSWORD' $DefaultWSLPostgresPassword
    } else {
      $Environment['ORRERY_DATABASE_PASSWORD'] = Get-ProcessEnvironmentOrDefault 'ORRERY_DEV_POSTGRES_PASSWORD' $DefaultDevPostgresPassword
    }
  }

  return $Environment
}

function Write-ApiRuntimeState(
  [hashtable]$Environment,
  [string]$ApiAddrValue,
  [int]$KratosPublicPort,
  [string]$WSLDistro
) {
  $ConfigPath = ''
  if ($Environment.ContainsKey('ORRERY_CONFIG')) {
    try {
      $ConfigPath = [System.IO.Path]::GetFullPath([string]$Environment['ORRERY_CONFIG'])
    } catch {
      $ConfigPath = [string]$Environment['ORRERY_CONFIG']
    }
  }
  $State = [ordered]@{
    api_addr = $ApiAddrValue
    kratos_public_port = $KratosPublicPort
    openfga_public_port = $script:OpenFGAPublicPort
    wsl_distro = $WSLDistro
    config_path = $ConfigPath
    wsl_dependencies = [bool](Test-WSLDevConfigSelected $Environment)
    database_mode = if (Test-RemoteDevConfigSelected $Environment) { 'remote' } else { 'local' }
    database_endpoint = ''
    recorded_at = (Get-Date -Format o)
  }
  if (Test-RemoteDevConfigSelected $Environment -and $Environment.ContainsKey('ORRERY_DATABASE_CORE_URL')) {
    try {
      $RemoteURI = [System.Uri]::new([string]$Environment['ORRERY_DATABASE_CORE_URL'])
      $State.database_endpoint = "$($RemoteURI.Host):$($RemoteURI.Port)"
    } catch {
      $State.database_endpoint = 'invalid'
    }
  }
  $State | ConvertTo-Json | Set-Content -LiteralPath $ApiRuntimeStateFile -Encoding UTF8
}

function Read-ApiRuntimeState {
  if (-not (Test-Path -LiteralPath $ApiRuntimeStateFile)) {
    return $null
  }
  try {
    return Get-Content -Raw -LiteralPath $ApiRuntimeStateFile | ConvertFrom-Json
  } catch {
    Write-Warning "Ignoring invalid API runtime state at ${ApiRuntimeStateFile}: $($_.Exception.Message)"
    return $null
  }
}

function ConvertTo-CmdArgument([string]$Value) {
  return '"' + $Value.Replace('"', '\"') + '"'
}

function Read-Pid([string]$Path) {
  if (-not (Test-Path -LiteralPath $Path)) {
    return $null
  }
  $Text = (Get-Content -Raw -LiteralPath $Path).Trim()
  if (-not $Text) {
    return $null
  }
  return [int]$Text
}

function Get-AliveProcess([string]$PidFile) {
  $PidValue = Read-Pid $PidFile
  if (-not $PidValue) {
    return $null
  }
  try {
    return Get-Process -Id $PidValue -ErrorAction Stop
  } catch {
    return $null
  }
}

function Test-PortListening([int]$Port) {
  $Connections = Get-NetTCPConnection -LocalPort $Port -State Listen -ErrorAction SilentlyContinue
  return $null -ne $Connections
}

function Wait-ProcessExit([int]$ProcessId, [int]$TimeoutSeconds = 5) {
  try {
    Wait-Process -Id $ProcessId -Timeout $TimeoutSeconds -ErrorAction SilentlyContinue
  } catch {
  }
}

function Get-PortOwner([int]$Port) {
  $Connection = Get-NetTCPConnection -LocalPort $Port -State Listen -ErrorAction SilentlyContinue | Select-Object -First 1
  if (-not $Connection) {
    return $null
  }
  try {
    return Get-Process -Id $Connection.OwningProcess -ErrorAction Stop
  } catch {
    return $null
  }
}

function Get-PortOwners([int]$Port) {
  $Connections = Get-NetTCPConnection -LocalPort $Port -State Listen -ErrorAction SilentlyContinue
  if (-not $Connections) {
    return @()
  }
  $OwnerIds = @($Connections | Select-Object -ExpandProperty OwningProcess -Unique | Where-Object { $_ -and $_ -gt 0 })
  $Owners = @()
  foreach ($OwnerId in $OwnerIds) {
    try {
      $Owners += Get-Process -Id $OwnerId -ErrorAction Stop
    } catch {
    }
  }
  return $Owners
}

function Add-SharedLogText(
  [string]$Path,
  [string]$Text,
  [int]$TimeoutSeconds = 10,
  [int]$RetryMilliseconds = 150
) {
  $Encoding = [System.Text.Encoding]::UTF8
  $Deadline = (Get-Date).AddSeconds($TimeoutSeconds)
  $LastError = $null

  while ($true) {
    $Stream = $null
    try {
      $Stream = [System.IO.FileStream]::new(
        $Path,
        [System.IO.FileMode]::Append,
        [System.IO.FileAccess]::Write,
        [System.IO.FileShare]::ReadWrite
      )
      $Bytes = $Encoding.GetBytes($Text)
      $Stream.Write($Bytes, 0, $Bytes.Length)
      return
    } catch [System.IO.IOException] {
      $LastError = $_.Exception
    } catch [System.UnauthorizedAccessException] {
      $LastError = $_.Exception
    } finally {
      if ($Stream) {
        $Stream.Dispose()
      }
    }

    if ((Get-Date) -ge $Deadline) {
      throw "Timed out waiting for log file '$Path' to become appendable: $($LastError.Message)"
    }
    Start-Sleep -Milliseconds $RetryMilliseconds
  }
}

function Start-ServiceProcess(
  [string]$Name,
  [string]$PidFile,
  [string]$FilePath,
  [string[]]$ArgumentList,
  [string]$WorkingDirectory,
  [string]$OutLog,
  [string]$ErrLog,
  [hashtable]$Environment = @{}
) {
  $Existing = Get-AliveProcess $PidFile
  if ($Existing) {
    Write-DevMessage "$Name already running (PID $($Existing.Id))."
    return
  }

  $ResolvedFilePath = Resolve-Tool $FilePath
  $Process = Invoke-WithProcessEnvironment $Environment {
    Start-Process `
      -FilePath $ResolvedFilePath `
      -ArgumentList $ArgumentList `
      -WorkingDirectory $WorkingDirectory `
      -RedirectStandardOutput $OutLog `
      -RedirectStandardError $ErrLog `
      -WindowStyle Hidden `
      -PassThru
  }

  Set-Content -LiteralPath $PidFile -Value $Process.Id -Encoding ASCII
  Write-DevMessage "$Name started (PID $($Process.Id))."
}

function Start-MergedLogServiceProcess(
  [string]$Name,
  [string]$PidFile,
  [string]$FilePath,
  [string[]]$ArgumentList,
  [string]$WorkingDirectory,
  [string]$CombinedLog,
  [hashtable]$Environment = @{}
) {
  $Existing = Get-AliveProcess $PidFile
  if ($Existing) {
    Write-DevMessage "$Name already running (PID $($Existing.Id))."
    return
  }

  $ResolvedFilePath = Resolve-Tool $FilePath
  $CmdPath = Resolve-Tool 'cmd'
  $CommandLine = ConvertTo-CmdArgument $ResolvedFilePath
  foreach ($Argument in $ArgumentList) {
    $CommandLine += ' ' + (ConvertTo-CmdArgument $Argument)
  }
  $StartedAt = Get-Date -Format o
  Add-SharedLogText $CombinedLog "`n== $Name start $StartedAt ==`n"
  $CmdLine = '/d /s /c "cd /d ' + (ConvertTo-CmdArgument $WorkingDirectory) + ' && ' + $CommandLine + ' >> ' + (ConvertTo-CmdArgument $CombinedLog) + ' 2>&1"'
  $Process = Invoke-WithProcessEnvironment $Environment {
    Start-Process `
      -FilePath $CmdPath `
      -ArgumentList $CmdLine `
      -WorkingDirectory $WorkingDirectory `
      -WindowStyle Hidden `
      -PassThru
  }

  Set-Content -LiteralPath $PidFile -Value $Process.Id -Encoding ASCII
  Write-DevMessage "$Name started (PID $($Process.Id), log $CombinedLog)."
}

function Stop-PortOwner([string]$Name, [int]$Port) {
  $Owner = Get-PortOwner $Port
  if (-not $Owner) {
    return $false
  }

  Stop-Process -Id $Owner.Id -Force
  Wait-ProcessExit $Owner.Id
  Write-DevMessage "$Name stopped by port $Port (PID $($Owner.Id), $($Owner.ProcessName))."
  return $true
}

function Stop-PortOwners([string]$Name, [int]$Port, [int]$TimeoutSeconds = 8) {
  $StoppedAny = $false
  $Deadline = (Get-Date).AddSeconds($TimeoutSeconds)
  while ((Get-Date) -lt $Deadline) {
    $Owners = @(Get-PortOwners $Port)
    if ($Owners.Count -eq 0) {
      return $StoppedAny
    }

    foreach ($Owner in $Owners) {
      try {
        Stop-Process -Id $Owner.Id -Force -ErrorAction Stop
        Wait-ProcessExit $Owner.Id
        Write-DevMessage "$Name stopped by port $Port (PID $($Owner.Id), $($Owner.ProcessName))."
        $StoppedAny = $true
      } catch {
      }
    }
    Start-Sleep -Milliseconds 150
  }

  if (Test-PortListening $Port) {
    throw "$Name port $Port is still in use after stopping listener processes."
  }
  return $StoppedAny
}

function Stop-ServiceProcess(
  [string]$Name,
  [string]$PidFile,
  [int]$Port,
  [bool]$CleanPortWhenPidMissing = $false
) {
  $Process = Get-AliveProcess $PidFile
  if (-not $Process) {
    Remove-Item -LiteralPath $PidFile -ErrorAction SilentlyContinue
    if ($CleanPortWhenPidMissing) {
      if (-not (Stop-PortOwners $Name $Port)) {
        Write-DevMessage "$Name is not running."
      }
    } else {
      Write-DevMessage "$Name is not running."
    }
    return
  }

  Stop-Process -Id $Process.Id -Force
  Wait-ProcessExit $Process.Id
  Remove-Item -LiteralPath $PidFile -ErrorAction SilentlyContinue
  Write-DevMessage "$Name stopped (PID $($Process.Id))."

  Stop-PortOwners $Name $Port | Out-Null
}

function Show-Status {
  $Api = Get-AliveProcess $ApiPidFile
  $Web = Get-AliveProcess $WebPidFile
  $RuntimeState = Read-ApiRuntimeState
  $EffectiveConfigPath = $null
  if ($RuntimeState -and -not [string]::IsNullOrWhiteSpace([string]$RuntimeState.config_path)) {
    $EffectiveConfigPath = [string]$RuntimeState.config_path
  } else {
    $EffectiveConfigPath = [Environment]::GetEnvironmentVariable('ORRERY_CONFIG', 'Process')
    if ([string]::IsNullOrWhiteSpace($EffectiveConfigPath)) {
      $EffectiveConfigPath = Resolve-DefaultDevConfig
    }
  }
  $EffectiveApiAddr = $ApiAddr
  if ($RuntimeState -and -not [string]::IsNullOrWhiteSpace([string]$RuntimeState.api_addr)) {
    $EffectiveApiAddr = [string]$RuntimeState.api_addr
  }
  $ApiPort = [int]($EffectiveApiAddr.Split(':')[-1])
  $ApiPortOwner = Get-PortOwner $ApiPort
  $WebPortOwner = Get-PortOwner $WebPort

  if ($Api) {
    $ApiStatus = "running (PID $($Api.Id), http://$EffectiveApiAddr)"
  } elseif ($ApiPortOwner) {
    $ApiStatus = "external (PID $($ApiPortOwner.Id), $($ApiPortOwner.ProcessName), http://$EffectiveApiAddr)"
  } else {
    $ApiStatus = "stopped"
  }

  if ($Web) {
    $WebStatus = "running (PID $($Web.Id), http://127.0.0.1:$WebPort)"
  } elseif ($WebPortOwner) {
    $WebStatus = "external (PID $($WebPortOwner.Id), $($WebPortOwner.ProcessName), http://127.0.0.1:$WebPort)"
  } else {
    $WebStatus = "stopped"
  }

  Write-DevMessage "Orrery dev services"
  Write-DevMessage "  Config: $EffectiveConfigPath"
  Write-DevMessage "  API: $ApiStatus"
  Write-DevMessage "  Web: $WebStatus"
  if ($RuntimeState -and [string]$RuntimeState.database_mode -eq 'remote') {
    Write-DevMessage "  Database: remote PostgreSQL ($([string]$RuntimeState.database_endpoint), direct; no SSH tunnel)"
    Write-DevMessage "  Background workers: disabled locally (production workers remain authoritative)"
  } elseif ($RuntimeState) {
    Write-DevMessage "  Database: local development"
  }
  Write-DevMessage "  Ports:"
  if ($ApiPortOwner) {
    Write-DevMessage "    API $ApiPort listening: True (PID $($ApiPortOwner.Id), $($ApiPortOwner.ProcessName))"
  } else {
    Write-DevMessage "    API $ApiPort listening: False"
  }
  if ($WebPortOwner) {
    Write-DevMessage "    Web $WebPort listening: True (PID $($WebPortOwner.Id), $($WebPortOwner.ProcessName))"
  } else {
    Write-DevMessage "    Web $WebPort listening: False"
  }
  if ($RuntimeState -and $RuntimeState.wsl_dependencies) {
    Write-DevMessage "  WSL dependencies:"
    Write-DependencyPortStatus 5432 'PostgreSQL'
    $KratosPublicPort = 4433
    if ([int]$RuntimeState.kratos_public_port -gt 0) {
      $KratosPublicPort = [int]$RuntimeState.kratos_public_port
    }
    Write-DependencyPortStatus $KratosPublicPort 'Kratos public'
    $OpenFGAPublicPort = $OpenFGADefaultPort
    if ([int]$RuntimeState.openfga_public_port -gt 0) {
      $OpenFGAPublicPort = [int]$RuntimeState.openfga_public_port
    }
    Write-DependencyPortStatus $OpenFGAPublicPort 'OpenFGA'
  } elseif ($Api -and -not $RuntimeState) {
    Write-DevMessage "  Dependencies: unknown (runtime state is unavailable)"
  }
  Write-DevMessage "  Logs: $RunRoot"
  Write-DevMessage "    API combined: $ApiOut"
}

function Write-DependencyPortStatus([int]$Port, [string]$Name) {
  $Owner = Get-PortOwner $Port
  if ($Owner) {
    Write-DevMessage "    $Name ${Port}: listening (PID $($Owner.Id), $($Owner.ProcessName))"
  } else {
    Write-DevMessage "    $Name ${Port}: stopped"
  }
}

function Start-All {
  Ensure-RunRoot
  if ($Local) {
    Write-DevMessage "Starting Orrery dev services with explicitly requested local dependencies..."
  } else {
    Write-DevMessage "Starting Orrery dev services against the persistent remote database in $RemoteEnvFile..."
  }
  Ensure-PiCodingAgentRuntime
  $ManagedApi = Get-AliveProcess $ApiPidFile
  $ManagedWeb = Get-AliveProcess $WebPidFile
  $RuntimeState = Read-ApiRuntimeState
  $ApiEnvironment = New-ApiEnvironment
  if (-not $Local) {
    $RequestedApiPort = [int]($ApiAddr.Split(':')[-1])
    $RemoteRuntimeMatches = Test-RemoteRuntimeStateMatches $RuntimeState $ApiEnvironment
    if ($ManagedApi -and $RemoteRuntimeMatches) {
      $EffectiveApiAddr = [string]$RuntimeState.api_addr
      $ApiPort = [int]($EffectiveApiAddr.Split(':')[-1])
    } else {
      if ($ManagedApi) {
        $ManagedApiPort = $RequestedApiPort
        if ($RuntimeState -and -not [string]::IsNullOrWhiteSpace([string]$RuntimeState.api_addr)) {
          $ManagedApiPort = [int](([string]$RuntimeState.api_addr).Split(':')[-1])
        }
        Stop-ServiceProcess 'API' $ApiPidFile $ManagedApiPort $true
        $ManagedApi = $null
      }
      $RecordedApiPort = 0
      if ($RuntimeState -and -not [string]::IsNullOrWhiteSpace([string]$RuntimeState.api_addr)) {
        $RecordedApiPort = [int](([string]$RuntimeState.api_addr).Split(':')[-1])
      }
      if ($RecordedApiPort -gt 0 -and $RecordedApiPort -ne $RequestedApiPort -and (Get-PortOwner $RecordedApiPort)) {
        Stop-StaleRemoteModeApi $RecordedApiPort
      }
      if (Get-PortOwner $RequestedApiPort) {
        Stop-StaleRemoteModeApi $RequestedApiPort
      }
      Remove-Item -LiteralPath $ApiPidFile, $ApiRuntimeStateFile -ErrorAction SilentlyContinue
      $RuntimeState = $null
      $EffectiveApiAddr = $ApiAddr
      $ApiPort = $RequestedApiPort
    }
  } else {
    $EffectiveApiAddr = Resolve-EffectiveApiAddress $ApiAddr $ApiAddrWasExplicit $ManagedApi $RuntimeState
    $ApiPort = [int]($EffectiveApiAddr.Split(':')[-1])
  }
  $ApiPortOwner = Get-PortOwner $ApiPort
  if (
    $ManagedWeb -and
    -not $ManagedApi -and
    $RuntimeState -and
    -not [string]::IsNullOrWhiteSpace([string]$RuntimeState.api_addr) -and
    -not [string]::Equals([string]$RuntimeState.api_addr, $EffectiveApiAddr, [System.StringComparison]::OrdinalIgnoreCase)
  ) {
    Stop-ServiceProcess 'Web' $WebPidFile $WebPort $true
    $ManagedWeb = $null
  }
  $WebPortOwner = Get-PortOwner $WebPort
  $SkipApi = $false
  $SkipWeb = $false
  $UseWSLDependencies = Test-WSLDevConfigSelected $ApiEnvironment
  $Distro = Get-ProcessEnvironmentOrDefault 'ORRERY_WSL_DISTRO' $DefaultWSLDistro
  $KratosPublicPort = 0
  $OpenFGAPublicPort = $OpenFGADefaultPort

  if ($ManagedApi) {
    if ($null -ne $RuntimeState) {
      $UseWSLDependencies = [bool]$RuntimeState.wsl_dependencies
    }
  }

  if ($UseWSLDependencies) {
    if ($ManagedApi -and $RuntimeState -and [int]$RuntimeState.kratos_public_port -gt 0) {
      $KratosPublicPort = [int]$RuntimeState.kratos_public_port
    } elseif ($SkipDependencies) {
      $KratosPublicPort = 4433
    } else {
      $KratosPublicPort = Resolve-KratosPublicPort $ApiPort
    }
    if ($ManagedApi -and $RuntimeState -and [int]$RuntimeState.openfga_public_port -gt 0) {
      $OpenFGAPublicPort = [int]$RuntimeState.openfga_public_port
    } elseif (-not $SkipDependencies) {
      $OpenFGAPublicPort = Resolve-OpenFGAPublicPort $ApiPort $KratosPublicPort
    }
    $script:OpenFGAPublicPort = $OpenFGAPublicPort
    $script:OpenFGAApiURL = "http://127.0.0.1:$OpenFGAPublicPort"
  }

  if (-not $ManagedApi -and $ApiPortOwner) {
    if (Test-HTTPReady "http://$EffectiveApiAddr/api/health") {
      Write-Warning "Port $ApiPort already serves an external Orrery API; reusing it."
      $SkipApi = $true
    } elseif ($Local -and $ApiAddrWasExplicit) {
      Write-Warning "Explicit API port $ApiPort is already used by PID $($ApiPortOwner.Id) ($($ApiPortOwner.ProcessName)); skipping API start."
      $SkipApi = $true
    } else {
      throw "Random API port $ApiPort became occupied before startup. Run start again to allocate another port."
    }
  }
  if (-not $ManagedWeb -and $WebPortOwner) {
    Write-Warning "Port $WebPort is already used by PID $($WebPortOwner.Id) ($($WebPortOwner.ProcessName)); skipping Web start."
    $SkipWeb = $true
  }

  if (-not $SkipApi -and -not $SkipDependencies -and $UseWSLDependencies) {
    $OpenFGAConfig = Ensure-WSLDependencies $KratosPublicPort
    Write-WSLDevelopmentConfig $KratosPublicPort $OpenFGAConfig
    $ApiEnvironment['ORRERY_CONFIG'] = $LocalDevConfig
  }

  if (-not $SkipApi -and -not $ManagedApi -and -not (Test-RemoteDevConfigSelected $ApiEnvironment)) {
    if (Test-DefaultDevConfigSelected $ApiEnvironment) {
      Ensure-DevPostgres
    }
    Initialize-DevDatabases $ApiEnvironment
  }

  if (-not $SkipApi) {
    $ApiArguments = @('run', './cmd/server', '-addr', $EffectiveApiAddr)
    if (Test-RemoteDevConfigSelected $ApiEnvironment) {
      $ApiArguments += '-disable-background-workers'
    }
    Start-MergedLogServiceProcess `
      -Name 'API' `
      -PidFile $ApiPidFile `
      -FilePath 'go' `
      -ArgumentList $ApiArguments `
      -WorkingDirectory $Root `
      -CombinedLog $ApiOut `
      -Environment $ApiEnvironment
    if (-not $ManagedApi) {
      Write-ApiRuntimeState $ApiEnvironment $EffectiveApiAddr $KratosPublicPort $Distro
      Wait-HTTPDependencyReady "http://$EffectiveApiAddr/api/health" 'Orrery API'
    }
  }

  if (-not $SkipWeb) {
    $WebEnvironment = @{
      VITE_API_PROXY_TARGET = Get-ProcessEnvironmentOrDefault 'VITE_API_PROXY_TARGET' "http://127.0.0.1:$ApiPort"
    }
    Start-ServiceProcess `
      -Name 'Web' `
      -PidFile $WebPidFile `
      -FilePath 'npm' `
      -ArgumentList @('run', 'dev', '--', '--host', '0.0.0.0', '--port', [string]$WebPort) `
      -WorkingDirectory $WebRoot `
      -OutLog $WebLog `
      -ErrLog $WebErrLog `
      -Environment $WebEnvironment
    if (-not $ManagedWeb) {
      Wait-HTTPDependencyReady "http://127.0.0.1:$WebPort/login" 'Orrery Web'
    }
  }

  Write-DevMessage ""
  Show-Status
  Write-DevMessage ""
  Write-DevMessage "Open http://127.0.0.1:$WebPort"
  Write-DevMessage "LAN/Netbird http://<host-ip>:$WebPort"
}

function Stop-All {
  $RuntimeState = Read-ApiRuntimeState
  $EffectiveApiAddr = $ApiAddr
  if ($RuntimeState -and -not [string]::IsNullOrWhiteSpace([string]$RuntimeState.api_addr)) {
    $EffectiveApiAddr = [string]$RuntimeState.api_addr
  }
  $ApiPort = [int]($EffectiveApiAddr.Split(':')[-1])
  $CleanManagedPorts = $null -ne $RuntimeState
  Stop-ServiceProcess 'Web' $WebPidFile $WebPort $CleanManagedPorts
  Stop-ServiceProcess 'API' $ApiPidFile $ApiPort $CleanManagedPorts
  Stop-WSLKeepAlive
  Remove-Item -LiteralPath $ApiRuntimeStateFile -ErrorAction SilentlyContinue
}

function Show-Logs {
  Ensure-RunRoot
  $Files = @($ApiOut, $ApiLog, $ApiErrLog, $WebLog, $WebErrLog) | Select-Object -Unique | Where-Object { Test-Path -LiteralPath $_ }
  if ($Files.Count -eq 0) {
    Write-DevMessage "No logs yet. Start services first."
    return
  }
  if ($Follow) {
    Get-Content -LiteralPath $Files -Wait -Tail 80
  } else {
    foreach ($File in $Files) {
      Write-DevMessage ""
      Write-DevMessage "== $File =="
      Get-Content -LiteralPath $File -Tail 80
    }
  }
}

switch ($Action) {
  'start' { Start-All }
  'stop' { Stop-All }
  'restart' {
    Stop-All
    Start-All
  }
  'status' { Show-Status }
  'logs' { Show-Logs }
}
