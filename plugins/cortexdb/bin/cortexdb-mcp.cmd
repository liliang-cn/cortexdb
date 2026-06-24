@echo off
setlocal enabledelayedexpansion
rem CortexDB MCP launcher (Windows).
rem Downloads the matching prebuilt MCP server binary from the GitHub release
rem (cached in the plugin data dir), then runs it. No Go toolchain required.
rem Override with a local binary via the CORTEXDB_MCP_BIN environment variable.

if defined CORTEXDB_MCP_BIN (
  if exist "%CORTEXDB_MCP_BIN%" (
    "%CORTEXDB_MCP_BIN%"
    exit /b %errorlevel%
  )
)

set "REPO=liliang-cn/cortexdb"
set "GOARCH=amd64"
if /I "%PROCESSOR_ARCHITECTURE%"=="ARM64" set "GOARCH=arm64"
set "ASSET=cortexdb-mcp-windows-%GOARCH%.exe"

set "CACHE_ROOT=%CLAUDE_PLUGIN_DATA%"
if not defined CLAUDE_PLUGIN_DATA set "CACHE_ROOT=%LOCALAPPDATA%\cortexdb"
set "CACHE_DIR=%CACHE_ROOT%\bin"
set "BIN=%CACHE_DIR%\%ASSET%"

if not exist "%BIN%" (
  if not exist "%CACHE_DIR%" mkdir "%CACHE_DIR%"
  powershell -NoProfile -ExecutionPolicy Bypass -Command ^
    "Invoke-WebRequest -UseBasicParsing -Uri 'https://github.com/%REPO%/releases/latest/download/%ASSET%' -OutFile '%BIN%.download'" || exit /b 1
  move /Y "%BIN%.download" "%BIN%" >nul
)

"%BIN%"
exit /b %errorlevel%
