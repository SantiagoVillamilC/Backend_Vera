@echo off
chcp 65001 >nul
cd /d "%~dp0"
title Vera - Laboratorio de voz
powershell.exe -NoProfile -ExecutionPolicy Bypass -File "%~dp0Laboratorio-Vera.ps1" %*
set "VERA_RESULTADO=%ERRORLEVEL%"
echo.
pause
exit /b %VERA_RESULTADO%
