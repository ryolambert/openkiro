@echo off
REM 编译
go build -o kirolink.exe kirolink.go
IF %ERRORLEVEL% NEQ 0 (
    echo 编译失败!
    pause
    exit /b 1
)
REM 压缩
upx --best --lzma kirolink.exe
 