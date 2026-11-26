# scroller-renderer

CLI headless em Go para renderizar replay de osu!mania a partir de beatmaps
`.osu`, replay `.osr` (ou auto-play) e skin. O renderizador usa buffers
RGBA em software, faz supersampling temporal para motion blur, mistura o áudio
PCM/hitsounds e entrega vídeo H.264/AAC ao FFmpeg.

## Requisitos

- Go 1.22 ou superior
- FFmpeg com `libx264` e encoder AAC disponível no `PATH`
- Dependências Go declaradas no `go.mod`:
  - `github.com/ulikunitz/xz/lzma` para decodificar replays
  - `golang.org/x/image/font/basicfont` para o HUD

## Instalação

Linux (Debian/Ubuntu):

```sh
sudo apt update
sudo apt install ffmpeg
sudo snap install go --classic
go mod download
```

Use the official Go 1.22+ distribution if Snap is unavailable.

Windows (PowerShell; usando winget):

```powershell
winget install GoLang.Go
winget install Gyan.FFmpeg
go mod download
```

Reabra o terminal depois de instalar para atualizar o `PATH`. Distribuições
Windows do FFmpeg precisam incluir `libx264`; confirme com `ffmpeg -encoders`.

## Primeiro render

```sh
go run ./cmd/mania-renderer -osu "C:\maps\map.osu" -osr "C:\replays\play.osr" -skin "C:\osu\Skins\MySkin" -out "render.mp4"
```

Sem `-osr`, o programa gera um replay auto-play:

```sh
go run ./cmd/mania-renderer -osu ./map.osu -out render.mp4 -fps 60 -motionblur 4 -speed 1 -w 1280 -h 720
```

Flags: `-osu` (obrigatória), `-osr`, `-skin`, `-out`, `-fps`, `-motionblur`
(subframes por quadro), `-speed` (0.5 a 2), `-w` e `-h` (dimensões pares).
As imagens PNG de notas, teclas e julgamentos existentes na skin são carregadas;
quando ausentes, o renderer desenha elementos procedurais. Áudio e vídeo são
processados em memória/stream, com um WAV temporário removido ao terminar.

Execute os testes com `go test ./...`.
