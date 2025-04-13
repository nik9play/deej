@ECHO OFF

IF "%GOPATH%"=="" GOTO NOGO
IF NOT EXIST %GOPATH%\bin\rsrc.exe GOTO INSTALL
:POSTINSTALL
ECHO Creating resource file...
go generate ./...
GOTO DONE

:INSTALL
ECHO Installing rsrc...
go install github.com/akavel/rsrc@latest
IF ERRORLEVEL 1 GOTO GETFAIL
GOTO POSTINSTALL

:GETFAIL
ECHO Failure running go install github.com/akavel/rsrc@latest.  Ensure that go and git are in PATH
GOTO DONE

:NOGO
ECHO GOPATH environment variable not set
GOTO DONE

:DONE
