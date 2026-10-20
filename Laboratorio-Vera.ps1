param([switch]$Demo, [ValidateRange(1,300)][int]$MaxSolicitudes = 20)
$ErrorActionPreference = 'Stop'
Set-Location -LiteralPath $PSScriptRoot
if (-not (Get-Command docker -ErrorAction SilentlyContinue)) { Write-Host 'Necesitas Docker Desktop instalado y abierto.'; exit 1 }
docker info *> $null
if ($LASTEXITCODE -ne 0) { Write-Host 'Abre Docker Desktop y espera a que el motor esté listo.'; exit 1 }
if (-not $Demo -and -not (Test-Path -LiteralPath '.env.local')) { Write-Host 'Falta .env.local con GEMINI_API_KEY y VERA_MODELO. No compartas la clave.'; exit 1 }
# Prefiere el puerto habitual; si está ocupado, Windows asigna uno disponible.
$sondaPuerto = [Net.Sockets.TcpListener]::new([Net.IPAddress]::Loopback, 8787)
try { $sondaPuerto.Start() }
catch [Net.Sockets.SocketException] {
    $sondaPuerto.Stop()
    $sondaPuerto = [Net.Sockets.TcpListener]::new([Net.IPAddress]::Loopback, 0)
    $sondaPuerto.Start()
}
$puertoLaboratorio = $sondaPuerto.LocalEndpoint.Port
$sondaPuerto.Stop()
if ($puertoLaboratorio -ne 8787) { Write-Host "El puerto 8787 está ocupado. Se usará el puerto $puertoLaboratorio." }
$bytesSesion = New-Object byte[] 32
$generadorSesion = [Security.Cryptography.RandomNumberGenerator]::Create()
$generadorSesion.GetBytes($bytesSesion)
$generadorSesion.Dispose()
$tokenSesion = [BitConverter]::ToString($bytesSesion).Replace('-','').ToLowerInvariant()
$env:VERA_LAB_TOKEN = $tokenSesion
$navegador = Start-Job -ArgumentList $tokenSesion,$puertoLaboratorio -ScriptBlock {
    param($tokenLocal, $puertoLocal)
    for ($intento = 0; $intento -lt 120; $intento++) {
        try {
            $estadoLocal = Invoke-WebRequest -Uri "http://localhost:$puertoLocal/api/estado" -Headers @{'X-Vera-Token'=$tokenLocal} -UseBasicParsing -TimeoutSec 1
            if ($estadoLocal.StatusCode -eq 200) { Start-Process -FilePath ("http://localhost:$puertoLocal/#token=" + $tokenLocal); break }
        } catch { }
        Start-Sleep -Seconds 1
    }
}
$argumentosDocker = @('run','--rm','-it','-p',"127.0.0.1:${puertoLaboratorio}:${puertoLaboratorio}",'-e','VERA_LAB_TOKEN')
if (-not $Demo) { $argumentosDocker += @('--env-file','.env.local') }
$argumentosDocker += @('-v',"${PSScriptRoot}:/src",'-v','vera117-gomod:/go/pkg/mod','-v','vera117-gocache:/root/.cache/go-build','-w','/src','golang:1.27.0','go','run','./cmd/laboratorio-vera','-addr',"0.0.0.0:$puertoLaboratorio",'-max-solicitudes',"$MaxSolicitudes")
if ($Demo) { $argumentosDocker += '-demo' } else { $argumentosDocker += '-nivel-gratuito-confirmado' }
Write-Host 'Preparando el laboratorio. Se abrirá el navegador cuando esté listo.'
Write-Host 'Mantén esta ventana abierta. El audio y las propuestas usan datos de prueba.'
try { & docker @argumentosDocker; $resultadoLaboratorio = $LASTEXITCODE }
finally { Stop-Job -Job $navegador -ErrorAction SilentlyContinue; Remove-Job -Job $navegador -Force -ErrorAction SilentlyContinue; Remove-Item Env:\VERA_LAB_TOKEN -ErrorAction SilentlyContinue }
exit $resultadoLaboratorio
