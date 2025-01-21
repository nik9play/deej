@ECHO OFF

REM check if mode parameter is provided
IF "%1"=="" (
    ECHO Usage: build.bat [dev^|release]
    EXIT /B 1
)

SET MODE=%1

REM validate the mode parameter
IF NOT "%MODE%"=="dev" IF NOT "%MODE%"=="release" (
    ECHO Invalid mode: %MODE%
    ECHO Use "dev" or "release".
    EXIT /B 1
)

ECHO Building deej (%MODE%)...

REM set repo root in relation to script path to avoid cwd dependency
SET "DEEJ_ROOT=%~dp0..\..\..\.."

REM get git commit and version tag
FOR /f "delims=" %%a IN ('git rev-list -1 --abbrev-commit HEAD') DO @SET GIT_COMMIT=%%a
FOR /f "delims=" %%a IN ('git describe --tags --always') DO @SET VERSION_TAG=%%a
SET BUILD_TYPE=%MODE%

ECHO Embedding build-time parameters:
ECHO - gitCommit %GIT_COMMIT%
ECHO - versionTag %VERSION_TAG%
ECHO - buildType %BUILD_TYPE%

REM build based on mode
IF "%MODE%"=="dev" (
    go build -o "%DEEJ_ROOT%\build\deej-dev.exe" -gcflags=all="-N -l" -ldflags "-X main.gitCommit=%GIT_COMMIT% -X main.versionTag=%VERSION_TAG% -X main.buildType=%BUILD_TYPE%" "%DEEJ_ROOT%\pkg\deej\cmd"
) ELSE (
    go build -o "%DEEJ_ROOT%\build\deej-release.exe" -ldflags "-H=windowsgui -s -w -X main.gitCommit=%GIT_COMMIT% -X main.versionTag=%VERSION_TAG% -X main.buildType=%BUILD_TYPE%" "%DEEJ_ROOT%\pkg\deej\cmd"
)

IF %ERRORLEVEL% NEQ 0 GOTO BUILDERROR

ECHO Done.
GOTO DONE

:BUILDERROR
ECHO Failed to build deej in %MODE% mode! See above output for details.
EXIT /B 1

:DONE