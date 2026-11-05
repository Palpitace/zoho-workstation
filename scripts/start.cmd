@rem input: Extracted application folder and user-owned .env.
@rem output: First-run configuration template and foreground service with logs.
@rem pos: Windows launcher. 一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。
@echo off
setlocal
pushd "%~dp0"
if errorlevel 1 exit /b 1
if not exist ".env" (
  copy /y ".env.example" ".env" >nul
  echo Created .env. Fill in your credentials, save it, then run start.cmd again.
  notepad ".env"
  pause
  popd
  exit /b 0
)
"workstation.exe" check
if errorlevel 1 goto failed
echo Open the local_url printed below in your browser. Keep this window open.
echo Press Ctrl+C to stop. Logs: logs\workstation.log
"workstation.exe" serve
if errorlevel 1 goto failed
popd
exit /b 0
:failed
echo Startup or service failed. See logs\workstation.log for details.
pause
popd
exit /b 1
