<#
Genera silent_password_local.go (ignorado por git) con la contrasena del modo silencioso.
La contrasena se lee de la variable de entorno KAIRO_SILENT_PASSWORD y se guarda en base64 para no
depender de comillas ni caracteres especiales. Sin la variable no se genera nada y el modo --silent
siempre abre la ventana.

  $env:KAIRO_SILENT_PASSWORD = '...'; powershell -File build/windows/silent-password.ps1
  wails3 task build
#>
$ErrorActionPreference = 'Stop'
$root = Split-Path (Split-Path $PSScriptRoot -Parent) -Parent
$destino = Join-Path $root 'silent_password_local.go'

if (-not $env:KAIRO_SILENT_PASSWORD) {
    if (Test-Path $destino) { Remove-Item $destino }
    Write-Host 'KAIRO_SILENT_PASSWORD vacia: se compila sin contrasena (el modo --silent abrira siempre la ventana).'
    exit 0
}

$b64 = [Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes($env:KAIRO_SILENT_PASSWORD))
$codigo = @"
package main

import "encoding/base64"

// Generado por build/windows/silent-password.ps1. No se sube a git.
func init() {
	raw, err := base64.StdEncoding.DecodeString("$b64")
	if err == nil {
		silentPassword = string(raw)
	}
}
"@
[IO.File]::WriteAllText($destino, $codigo, (New-Object Text.UTF8Encoding($false)))
Write-Host "Generado $destino"
