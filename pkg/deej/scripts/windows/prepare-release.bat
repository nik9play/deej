@ECHO OFF

IF "%1"=="" GOTO NOTAG

ECHO Preparing release (%1)...
ECHO.

git tag --delete %1 >NUL 2>&1
git tag %1

REM set windows scripts dir root in relation to script path to avoid cwd dependency
SET "WIN_SCRIPTS_ROOT=%~dp0"

CALL "%WIN_SCRIPTS_ROOT%build-all.bat"

REM make this next part nicer by setting the repo root
SET "DEEJ_ROOT=%WIN_SCRIPTS_ROOT%..\..\..\.."
PUSHD "%DEEJ_ROOT%"
SET "DEEJ_ROOT=%CD%"
POPD

MKDIR "%DEEJ_ROOT%\releases\%1" 2> NUL
COPY /Y "%DEEJ_ROOT%\build\deej-release.exe" "%DEEJ_ROOT%\releases\%1\deej.exe" >NUL 2>&1
COPY /Y "%DEEJ_ROOT%\build\deej-dev.exe" "%DEEJ_ROOT%\releases\%1\deej-debug.exe" >NUL 2>&1
COPY /Y "%DEEJ_ROOT%\config_examples\config.example.yaml" "%DEEJ_ROOT%\releases\%1\config.yaml" >NUL 2>&1
COPY /Y "%DEEJ_ROOT%\pkg\deej\scripts\misc\release-notes.txt" "%DEEJ_ROOT%\releases\%1\notes.txt" >NUL 2>&1

ISCC /O"%DEEJ_ROOT%\releases\%1" /F"deej-setup-%1" "/DAppVersion=%1" /Qp "%DEEJ_ROOT%\pkg\deej\scripts\windows\installer.iss"

ECHO.
ECHO Release binaries created in %DEEJ_ROOT%\releases\%1
ECHO Opening release directory and notes for editing.
ECHO When you're done, run "git push origin %1" and draft the release on GitHub.

START explorer.exe "%DEEJ_ROOT%\releases\%1"
START notepad.exe "%DEEJ_ROOT%\releases\%1\notes.txt"

GOTO DONE

:NOTAG
ECHO usage: %0 ^<tag name^>    (use semver i.e. v0.9.3)
GOTO DONE

:DONE
