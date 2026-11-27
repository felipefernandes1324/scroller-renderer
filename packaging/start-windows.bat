@echo off
setlocal
cd /d "%~dp0"

where ffmpeg.exe >nul 2>nul
if errorlevel 1 (
    echo FFmpeg was not found on PATH.
    echo Install it with: winget install Gyan.FFmpeg
    pause
    exit /b 1
)

ffmpeg.exe -hide_banner -encoders 2>nul | findstr /C:"libx264" >nul
if errorlevel 1 (
    echo Your FFmpeg build does not provide libx264.
    pause
    exit /b 1
)

ffmpeg.exe -hide_banner -encoders 2>nul | findstr /R /C:"[ ]aac[ ]" >nul
if errorlevel 1 (
    echo Your FFmpeg build does not provide the AAC encoder.
    pause
    exit /b 1
)

scroller-renderer.exe -gui
if errorlevel 1 pause
