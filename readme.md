# scroller-renderer

CLI headless em Go para renderizar gameplay de osu!mania a partir de um beatmap
`.osu`, replay `.osr` (ou auto-play) e skin opcional. O renderizador usa buffers
RGBA em software, faz supersampling temporal para motion blur, mistura o áudio
PCM/hitsounds e entrega vídeo H.264/AAC ao FFmpeg.

## Requisitos

- Go 1.22 ou superior
- FFmpeg com `libx264` e encoder AAC disponível no `PATH`
- Dependências Go declaradas no `go.mod`:
  - `github.com/ulikunitz/xz/lzma` para decodificar replays
  - `golang.org/x/image` para fontes TrueType embutidas, escalonamento de sprites e HUD

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
(subframes por quadro), `-speed` (0.5 a 2), `-scrollspeed` (5 a 40, padrão 24), `-w` e `-h` (dimensões pares).
Com `-skin`, o renderer carrega sprites Mania de notas, LNs, teclas, stage,
iluminação e hitbursts, além das fontes numéricas `ScorePrefix`/`ComboPrefix`
para score, combo e accuracy. Aplica `ColumnWidth`, `HitPosition`,
`ScorePosition` (hitbursts), `ComboPosition`, `LightPosition`, cores e demais
assets reconhecidos pelo `skin.ini`; imagens ausentes usam substitutos
procedurais. `AccuracyPosition` não é uma chave padrão do `skin.ini`; como
extensão, pode ser definida em `[Mania]` (ou `[Mania{K}K]`) entre 0 e 480 para
posicionar verticalmente o indicador de accuracy. Sem essa chave, ele mantém a
posição padrão do HUD.

Para replays `.osr`, score e accuracy finais são lidos dos contadores originais
do replay e calculados com a regra ScoreV1/ScoreV2 apropriada. O SV variável do
mapa vem dos `TimingPoints` do `.osu`. O `.osr` padrão não armazena a velocidade
de rolagem escolhida nas opções locais do jogador; para aproximá-la, informe
`-scrollspeed` (5 a 40; padrão 24). Áudio e vídeo são processados em
memória/stream, com WAV temporário removido.

Execute os testes com `go test ./...`.
