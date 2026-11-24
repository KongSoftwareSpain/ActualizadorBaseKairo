<#
Genera kairo_password_local.go (ignorado por git) con la contrasena fija de la BD de Kairo (PASSWORD_BD de modConfigBD.bas), que el importador usa
para crear el login del config.ini si no existe.
La contrasena se lee de la variable de entorno KAIRO_DB_PASSWORD y se guarda en base64 para no
depender de comillas ni caracteres especiales. Sin la variable no se genera nada y el importador solo
avisa de que no puede crear ese login.

  $env:KAIRO_DB_PASSWORD = '...'; powershell -File build/windows/kairo-db-password.ps1
  wails3 task build
#>
$ErrorActionPreference = 'Stop'
$root = Split-Path (Split-Path $PSScriptRoot -Parent) -Parent
$destino = Join-Path $root 'kairo_password_local.go'

if (-not $env:KAIRO_DB_PASSWORD) {
    if (Test-Path $destino) { Remove-Item $destino }
    Write-Host 'KAIRO_DB_PASSWORD vacia: se compila sin contrasena (el modo --silent abrira siempre la ventana).'
    exit 0
}

$b64 = [Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes($env:KAIRO_DB_PASSWORD))
$codigo = @"
package main

import "encoding/base64"

// Generado por build/windows/kairo-db-password.ps1. No se sube a git.
func init() {
	raw, err := base64.StdEncoding.DecodeString("$b64")
	if err == nil {
		kairoDbPassword = string(raw)
	}
}
"@
[IO.File]::WriteAllText($destino, $codigo, (New-Object Text.UTF8Encoding($false)))
Write-Host "Generado $destino"
