@echo off
REM ============================================================================
REM  CodePorter Client - one-click Windows build
REM
REM  Double-click this file (or: build-client.bat) to produce:
REM    client\release\CodePorter-<ver>-x64.exe   single-file portable
REM      (no installer, no registry writes - just double-click and run)
REM    client\release\CodePorter-<ver>-x64.zip   same app as a plain zip
REM
REM  Steps:  build Go core, build Electron UI, package exe
REM  ("->" is avoided on purpose: cmd would read ">" as a redirection.)
REM
REM  Run a single stage:
REM    build-client.bat deps      install npm dependencies only
REM    build-client.bat core      force-build the Go core only
REM    build-client.bat app       build the Electron app only
REM    build-client.bat pack      full package only (portable exe, no UI recompile)
REM    build-client.bat dir       fastest: unpacked folder only, no compression
REM    build-client.bat zip       unpacked folder + zip, skip portable compression
REM    build-client.bat portable  portable exe only, fail instead of falling back
REM
REM  Speed knobs (environment):
REM    FORCE_CORE=1   force a Go rebuild even if sources are older than the binary
REM                   (default full build already does incremental core builds)
REM    SKIP_INSTALLER=1  full build emits folder + zip instead of the portable exe
REM
REM  NOTE: this file is intentionally pure ASCII. A .bat file is parsed with
REM  the system ANSI code page (GBK on Chinese Windows); non-ASCII characters
REM  get mangled and break the parsing. Chinese docs live in client\README.md.
REM ============================================================================
setlocal
set "ROOT=%~dp0"
set "CLIENT=%ROOT%client"
set "STEP=%~1"

REM --- Defaults tuned for networks where GitHub / npm are slow -------------
if not defined GOPROXY         set "GOPROXY=https://goproxy.cn,direct"
if not defined ELECTRON_MIRROR set "ELECTRON_MIRROR=https://npmmirror.com/mirrors/electron/"
REM NSIS / winCodeSign 等打包工具链默认从 GitHub Releases 下载（国内经常不通）。
REM 这个变量让 electron-builder 改从同一套文件的国内镜像取。
if not defined ELECTRON_BUILDER_BINARIES_MIRROR set "ELECTRON_BUILDER_BINARIES_MIRROR=https://npmmirror.com/mirrors/electron-builder-binaries/"

echo.
echo ============================================================
echo  CodePorter Client Build
echo  Repo: %ROOT%
echo ============================================================
echo.

REM ---------- 0. prerequisites ----------
where go   >nul 2>&1 || (echo [ERROR] Go not found. Install Go 1.23+ and add it to PATH. & goto :fail)
where node >nul 2>&1 || (echo [ERROR] Node not found. Install Node.js 18+ and add it to PATH. & goto :fail)
for /f "tokens=*" %%v in ('go version') do set "GOVER=%%v"
for /f "tokens=*" %%v in ('node -v') do set "NODEVER=%%v"
echo [1/5] Environment OK: %GOVER% ^| Node %NODEVER%

REM ---------- 1. npm dependencies ----------
if "%STEP%"=="pack" goto :core
if not exist "%CLIENT%\node_modules" (
  echo [2/5] Installing npm dependencies ^(first run takes a while^)...
  pushd "%CLIENT%"
  call npm install --no-audit --no-fund
  if errorlevel 1 (popd & echo [ERROR] npm install failed. & goto :fail)
  popd
) else (
  echo [2/5] Dependencies already installed, skipping.
)

REM ---------- 2. Electron runtime ----------
REM npm allow-scripts may block electron's postinstall, leaving no binary.
if not exist "%CLIENT%\node_modules\electron\dist\electron.exe" (
  echo [2.5/5] Downloading Electron runtime...
  pushd "%CLIENT%"
  call node node_modules\electron\install.js
  if errorlevel 1 (popd & echo [ERROR] Electron runtime download failed. & goto :fail)
  popd
)

REM ---------- 3. Go core ----------
:core
echo [3/5] Building Go core...
REM Build through the Node helper so we get an incremental skip for free: when no
REM .go source is newer than the existing binary it reuses it instead of linking
REM again. "core" stage or FORCE_CORE=1 forces a clean rebuild. The helper also
REM deletes a stale/truncated binary before a real build.
set "CORE_ARGS=--skip-if-fresh"
if /i "%STEP%"=="core" set "CORE_ARGS="
if defined FORCE_CORE if not "%FORCE_CORE%"=="0" set "CORE_ARGS="
pushd "%CLIENT%"
call node scripts\build-core.mjs %CORE_ARGS% < nul
if errorlevel 1 (popd & echo [ERROR] Go core build failed. & goto :fail)
popd
REM Endpoint security (EDR/AV) can quarantine the freshly built exe and leave a
REM 9-byte stub or nothing at all behind. Catch it here instead of shipping an
REM installer that silently reports "core process not started" on the user side.
if not exist "%ROOT%backend\bin\codeporter-core.exe" (
  echo [ERROR] Core binary missing after build - likely quarantined by antivirus/EDR.
  echo         Add backend\bin and client\release to the real-time scan exclusion list,
  echo         or sign the binary, then run this again.
  goto :fail
)
for %%F in ("%ROOT%backend\bin\codeporter-core.exe") do set "CORESIZE=%%~zF"
if %CORESIZE% LSS 1048576 (
  echo [ERROR] Core binary is only %CORESIZE% bytes - it was truncated/quarantined by
  echo         antivirus or EDR. Add the output folders to the scan exclusion list and
  echo         rebuild.
  goto :fail
)
REM Never write "->" here: cmd parses ">" as a redirection and would overwrite
REM the core binary with this line's text (that is exactly what produced the
REM 9/26-byte "core" that failed to start).
echo       core: backend\bin\codeporter-core.exe ^(%CORESIZE% bytes^)

if "%STEP%"=="core" goto :ok
REM Pack-only stages reuse the already-built UI and jump straight to packaging.
if "%STEP%"=="pack"     goto :package
if "%STEP%"=="dir"      goto :packdironly
if "%STEP%"=="zip"      goto :packzip
if "%STEP%"=="portable" goto :packportable

REM ---------- 4. Electron app ----------
:app
echo [4/5] Building Electron main process and renderer...
pushd "%CLIENT%"
call npm run build < nul
if errorlevel 1 (popd & echo [ERROR] App build failed. & goto :fail)
popd

if "%STEP%"=="app" goto :ok

REM ---------- 5. package ----------
:package
REM Default output is the portable exe: one file, no installer, no registry.
REM SKIP_INSTALLER=1 skips the heavy single-file compression and emits only the
REM unpacked folder + zip.
REM If the portable build fails for any reason we still emit a directory build
REM plus a zip, so the user always ends up with something that runs.
if "%SKIP_INSTALLER%"=="1" goto :packzip

:packportable
call :sanitizeproxy
echo [5/5] Packaging portable exe ^(single file, no installer^)...
pushd "%CLIENT%"
call npx electron-builder --win portable --x64 --config electron-builder.config.cjs < nul
set "RC=%ERRORLEVEL%"
popd
if "%RC%"=="0" goto portableok
if "%STEP%"=="portable" (echo [ERROR] Portable build failed. & goto :fail)
echo.
echo [WARN] Portable build failed. Falling back to directory build + zip.
echo.
goto :packzip
:portableok
REM Portable exe is self-contained; the unpacked folder was only an intermediate.
call :cleanunpacked
goto :packok

:packzip
call :sanitizeproxy
echo [5/5] Packaging directory build + zip...
pushd "%CLIENT%"
call npx electron-builder --dir --win --x64 --config electron-builder.config.cjs < nul
if errorlevel 1 (popd & echo [ERROR] Directory build failed. & goto :fail)
call npx electron-builder --win zip --x64 --config electron-builder.config.cjs < nul
set "RC=%ERRORLEVEL%"
popd
if not "%RC%"=="0" (echo [ERROR] Zip build failed. & goto :fail)
REM The zip already contains the whole app; drop the redundant 200MB+ folder.
call :cleanunpacked
goto :packok

:packdironly
call :sanitizeproxy
echo [5/5] Packaging unpacked directory only ^(fastest, no compression^)...
pushd "%CLIENT%"
call npx electron-builder --dir --win --x64 --config electron-builder.config.cjs < nul
popd
if errorlevel 1 (echo [ERROR] Directory build failed. & goto :fail)
REM Intentionally keep win-unpacked here: that folder IS the deliverable for
REM this stage; run CodePorter.exe inside it directly.
goto :packok

:packok
REM The packaged copy is a second place where EDR can strike: it may be emptied
REM while the app is running. Warn loudly instead of shipping a broken exe.
if exist "%CLIENT%\release\win-unpacked\resources\core\codeporter-core.exe" call :checkpackedcore

:ok
echo.
echo ============================================================
echo  BUILD OK
echo ============================================================
if exist "%CLIENT%\release" (
  for %%f in ("%CLIENT%\release\*.exe") do echo    %%~nxf   %%~zf bytes
  for %%f in ("%CLIENT%\release\*.zip") do echo    %%~nxf   %%~zf bytes
  if exist "%CLIENT%\release\win-unpacked\CodePorter.exe" (
    echo    win-unpacked\CodePorter.exe   ^<^- runnable directly from this folder
  )
)
echo.
echo Output: %CLIENT%\release
echo.

REM Open the output folder when double-clicked. Skipped when a stage argument is
REM given, so the script stays pipe-friendly when invoked from CI or another shell.
REM (A bare "start" inherits handles and would keep a caller's pipe open forever.)
if "%STEP%"=="" (
  start "" explorer.exe "%CLIENT%\release"
)
goto :eof

:fail
echo.
echo ============================================================
echo  BUILD FAILED - see the error above
echo ============================================================
echo.
echo Common causes:
echo   * "is being used by another process" - CodePorter is still running.
echo     Close it (Task Manager ^}- end CodePorter.exe^) and run this again.
echo   * artifact download failed - rerun; set SKIP_INSTALLER=1 to skip the
echo     portable exe and produce a runnable folder build plus a zip instead.
echo.
REM Only pause when double-clicked; a pause would hang scripted/CI invocations.
if "%STEP%"=="" pause
exit /b 1

:sanitizeproxy
REM Strip unusable proxy config before electron-builder downloads artifacts.
REM electron-builder's downloader honours HTTP_PROXY/HTTPS_PROXY; a malformed
REM value ("http://", ":0", ...) makes every download die with
REM "proxyconnect tcp: dial tcp :0". Same trap from npm config: old npm versions
REM leave proxy=null in ~/.npmrc and electron-builder treats "null" as a real
REM proxy. Must be "call npm" (npm.cmd ends with a goto that kills the calling
REM batch otherwise) and npm must not run inside for /f.
for %%V in (HTTP_PROXY HTTPS_PROXY http_proxy https_proxy ALL_PROXY all_proxy) do call :checkproxy %%V
call npm config get proxy > "%TEMP%\cp-npm-proxy.txt" 2>nul
findstr /c:"null" "%TEMP%\cp-npm-proxy.txt" >nul && call npm config delete proxy
call npm config get https-proxy > "%TEMP%\cp-npm-proxy.txt" 2>nul
findstr /c:"null" "%TEMP%\cp-npm-proxy.txt" >nul && call npm config delete https-proxy
del /q "%TEMP%\cp-npm-proxy.txt" 2>nul
goto :eof

:checkproxy
REM %1=proxy variable name. Clears it when the value is not a usable
REM "scheme://host:port" URL. Keeps the build working on machines that export a
REM broken proxy (common with sandbox/CLI wrappers) instead of failing on every
REM artifact download. NOTE: this line must start with a single colon - "::" is
REM a comment in cmd and `call :label` would not find it.
set "PVAR=%~1"
call set "PVAL=%%%PVAR%%%"
if not defined PVAL goto :eof
echo.%PVAL%| findstr /i /r /c:"://[0-9a-z._-]*[0-9a-z]:[1-9][0-9]*" >nul
if not errorlevel 1 goto :eof
echo [WARN] %PVAR%="%PVAL%" is not a usable proxy URL - ignoring it for packaging.
set "%PVAR%="
goto :eof

:cleanunpacked
REM win-unpacked is a mandatory intermediate electron-builder assembles before
REM compressing the portable exe / zip. It is 200MB+ and is not a deliverable
REM once a self-contained exe or zip exists, so remove it on those stages to save
REM disk and keep release\ clean. The "dir" stage keeps it on purpose.
if exist "%CLIENT%\release\win-unpacked" (
  echo       Cleaning intermediate win-unpacked folder...
  rmdir /s /q "%CLIENT%\release\win-unpacked"
)
goto :eof

:checkpackedcore
REM %1 unused. Flags a packaged core binary that is too small to be real - the
REM signature of an antivirus/EDR emptying the file.
for %%F in ("%CLIENT%\release\win-unpacked\resources\core\codeporter-core.exe") do set "PSIZE=%%~zF"
if %PSIZE% LSS 1048576 (
  echo.
  echo [WARN] The packaged core is only %PSIZE% bytes - antivirus or EDR emptied it.
  echo [WARN] The app will start but report "core process not started". Add
  echo [WARN] client\release to the real-time scan exclusion list and rebuild.
  echo.
)
goto :eof
