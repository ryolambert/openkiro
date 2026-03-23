@echo off
REM 编译
go build -o openkiro.exe openkiro.go
IF %ERRORLEVEL% NEQ 0 (
    echo 编译失败!
    pause
    exit /b 1
)
REM 压缩
upx --best --lzma openkiro.exe
 