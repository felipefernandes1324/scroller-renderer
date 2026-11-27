# scroller-renderer

Renderizador em Go para replay de osu!mania a partir de beatmaps
`.osu`, replay `.osr` (ou auto-play) e skin. O renderizador usa buffers
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
go run ./cmd/mania-renderer -osu ./map.osu -out render.mp4 -fps 60 -motionblur 1 -speed 1 -w 1280 -h 720
```

## Interface gráfica local

```sh
go run ./cmd/mania-renderer -gui
```

O navegador abre uma interface local em que basta selecionar o replay `.osr`.
O mapa é encontrado pelo hash do replay dentro da pasta `Songs` do osu!, e o
áudio é lido da pasta do mapa. A skin selecionada é carregada da instalação
local quando disponível; sem os arquivos instalados, usa a skin padrão
integrada. As pastas usuais são detectadas automaticamente; se necessário,
informe a pasta `Songs` e, opcionalmente, a pasta da skin em “Pastas locais”.
Na GUI, o replay e o áudio permanecem em velocidade normal (`1x`); o scroll
velocity começa em `12` para deixar as notas mais lentas e pode ser ajustado
numericamente, junto aos volumes. A GUI renderiza em 60 FPS e usa uma amostra
por quadro para evitar rastros de motion blur.
O vídeo pronto pode ser baixado pela página. O servidor escuta apenas em
`127.0.0.1`.

Flags: `-osu` (obrigatória), `-osr`, `-skin`, `-out`, `-fps` (máximo 60),
`-motionblur` (subframes por quadro), `-speed` (0.5 a 2), `-scrollspeed` (5 a 40, padrão 12),
`-musicvolume` e `-hitsoundvolume` (0 a 100, padrão 100), `-w` e `-h`
(dimensões pares).
Com `-skin`, o renderer carrega sprites Mania de notas, LNs, teclas, stage,
iluminação e hitbursts, além das fontes numéricas `ScorePrefix`/`ComboPrefix`
para score, combo e accuracy. Aplica `ColumnWidth`, `HitPosition`,
`ScorePosition` (hitbursts), `ComboPosition`, `LightPosition`, cores e demais
assets reconhecidos pelo `skin.ini`; notas e teclas ausentes usam substitutos
procedurais, mas efeitos de iluminação só aparecem quando fornecidos pela skin.
`AccuracyPosition` não é uma chave padrão do `skin.ini`; como
extensão, pode ser definida em `[Mania]` (ou `[Mania{K}K]`) entre 0 e 480 para
posicionar verticalmente o indicador de accuracy. Sem essa chave, ele mantém a
posição padrão do HUD.

Para replays `.osr`, score e accuracy finais são lidos dos contadores originais
do replay e calculados com a regra ScoreV1/ScoreV2 apropriada. O SV variável do
mapa vem dos `TimingPoints` do `.osu`. O `.osr` padrão não armazena a velocidade
de rolagem escolhida nas opções locais do jogador; para aproximá-la, informe
`-scrollspeed` (5 a 40; padrão 12). Áudio e vídeo são processados em
memória/stream, com WAV temporário removido.
O HUD também exibe precisão, UR, razão e contagem de julgamentos, além de BPM,
OD, HP e SR. O score e a precisão são interpolados entre julgamentos para uma
atualização visual mais fluida.

Execute os testes com `go test ./...`.
