@echo off
REM Build
go build -o openkiro.exe ./cmd/openkiro
IF %ERRORLEVEL% NEQ 0 (
    echo Build failed!
    pause
    exit /b 1
)
REM Compress
upx --best --lzma openkiro.exe
