@echo off
chcp 65001 >nul
cd /d "%~dp0"
title Vera - Consola de pruebas
where docker >nul 2>&1
if errorlevel 1 (
  echo Necesitas Docker Desktop instalado y abierto para usar esta consola.
  pause
  exit /b 1
)
docker info >nul 2>&1
if errorlevel 1 (
  echo Abre Docker Desktop y espera a que el motor este listo. Luego vuelve a abrir este archivo.
  pause
  exit /b 1
)
if /i "%~1"=="-demo" goto demo
if not exist ".env.local" (
  echo Falta .env.local con GEMINI_API_KEY y VERA_MODELO. No pegues tu clave en el chat.
  pause
  exit /b 1
)
echo Preparando la consola. Al inicio puede tardar mientras compila Go.
echo Gemini real con datos financieros FICTICIOS. Limite predeterminado: 20 solicitudes.
docker run --rm -it --env-file .env.local -v "%CD%:/src" -v vera117-gomod:/go/pkg/mod -v vera117-gocache:/root/.cache/go-build -w /src golang:1.27.0 go run ./cmd/consola-vera -nivel-gratuito-confirmado %*
goto fin
:demo
docker run --rm -it -v "%CD%:/src" -v vera117-gomod:/go/pkg/mod -v vera117-gocache:/root/.cache/go-build -w /src golang:1.27.0 go run ./cmd/consola-vera -demo
:fin
set "VERA_RESULTADO=%ERRORLEVEL%"
echo.
echo Consola terminada. Puedes abrirla de nuevo cuando quieras.
pause
exit /b %VERA_RESULTADO%
