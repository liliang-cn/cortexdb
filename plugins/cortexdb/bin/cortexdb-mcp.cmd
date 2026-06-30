@echo off
setlocal enabledelayedexpansion
rem CortexDB MCP launcher (Windows). Shared by the Claude Code and Codex plugins.
rem Downloads the matching prebuilt MCP server binary from the GitHub release
rem (cached in the plugin data dir), then runs it. No Go toolchain — ever.
rem The cache key includes the plugin version, so upgrades fetch the new server
rem automatically instead of reusing a stale cached binary.
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

rem Resolve the plugin version so the cache invalidates on upgrade.
set "PLUGIN_ROOT=%CLAUDE_PLUGIN_ROOT%"
if not defined PLUGIN_ROOT set "PLUGIN_ROOT=%~dp0.."
set "VERSION="
for /f "usebackq delims=" %%v in (`powershell -NoProfile -Command "try { (Get-Content -Raw '%PLUGIN_ROOT%\.claude-plugin\plugin.json' ^| ConvertFrom-Json).version } catch { '' }"`) do set "VERSION=%%v"

set "CACHE_ROOT=%CLAUDE_PLUGIN_DATA%"
if not defined CLAUDE_PLUGIN_DATA set "CACHE_ROOT=%LOCALAPPDATA%\cortexdb"
set "CACHE_DIR=%CACHE_ROOT%\bin"

if defined VERSION (
  set "BIN=%CACHE_DIR%\%ASSET%-%VERSION%"
  set "URL=https://github.com/%REPO%/releases/download/v%VERSION%/%ASSET%"
) else (
  set "BIN=%CACHE_DIR%\%ASSET%"
  set "URL=https://github.com/%REPO%/releases/latest/download/%ASSET%"
)

if not exist "%BIN%" (
  if not exist "%CACHE_DIR%" mkdir "%CACHE_DIR%"
  powershell -NoProfile -ExecutionPolicy Bypass -Command ^
    "Invoke-WebRequest -UseBasicParsing -Uri '%URL%' -OutFile '%BIN%.download'" || exit /b 1
  move /Y "%BIN%.download" "%BIN%" >nul
)

"%BIN%"
exit /b %errorlevel%
