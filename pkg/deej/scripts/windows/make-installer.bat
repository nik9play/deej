@ECHO OFF

SET "DEEJ_ROOT=%~dp0..\..\..\.."

FOR /f "delims=" %%a IN ('git rev-list -1 --abbrev-commit HEAD') DO @SET GIT_COMMIT=%%a
FOR /f "delims=" %%a IN ('git describe --tags --always') DO @SET VERSION_TAG=%%a

SET VERSION=%GIT_COMMIT%-%VERSION_TAG%

ECHO - gitCommit %GIT_COMMIT%
ECHO - versionTag %VERSION_TAG%

ISCC /O"%DEEJ_ROOT%\build" "/DAppVersion=%VERSION_TAG%" /Qp "%DEEJ_ROOT%\pkg\deej\scripts\windows\installer.iss"
ECHO Installer successfully built