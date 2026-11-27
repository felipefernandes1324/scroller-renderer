package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime"
	"mime/multipart"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/felipefernandes1324/scroller-renderer/internal/beatmap"
	"github.com/felipefernandes1324/scroller-renderer/internal/replay"
)

const (
	maxReplayBytes  = int64(128 << 20)
	maxSkinBytes    = int64(256 << 20)
	maxSkinFiles    = 10000
	maxFormOverhead = int64(16 << 20)
)

type renderJob struct {
	mu            sync.RWMutex
	status        string
	message       string
	progress      int
	progressKnown bool
	output        string
	tempDir       string
}

var renderJobs sync.Map

func startGUI() error {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("start local interface: %w", err)
	}
	url := "http://" + listener.Addr().String()
	openBrowser(url)
	log.Printf("Interface local: %s", url)

	mux := http.NewServeMux()
	mux.HandleFunc("/", handleHome)
	mux.HandleFunc("/defaults", handleDefaults)
	mux.HandleFunc("/render", handleRender)
	mux.HandleFunc("/status/", handleJobStatus)
	mux.HandleFunc("/download/", handleDownload)
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("serve local interface: %w", err)
	}
	return nil
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	if err := cmd.Start(); err != nil {
		log.Printf("Abra o endereço no navegador: %v", err)
	}
}

func handleHome(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" || r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = io.WriteString(w, guiHTML)
}

func handleDefaults(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Método não permitido", http.StatusMethodNotAllowed)
		return
	}
	songsPath := discoverOsuPaths()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"songsPath": songsPath,
	})
}

func handleRender(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Método não permitido", http.StatusMethodNotAllowed)
		return
	}
	if origin := r.Header.Get("Origin"); origin != "" && origin != "http://"+r.Host {
		http.Error(w, "Origem inválida", http.StatusForbidden)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxReplayBytes+maxSkinBytes+maxFormOverhead)
	if err := r.ParseMultipartForm(16 << 20); err != nil {
		http.Error(w, "Não foi possível ler os arquivos enviados: "+err.Error(), http.StatusBadRequest)
		return
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	songsPath := strings.TrimSpace(r.FormValue("songsPath"))
	skinPath := strings.TrimSpace(r.FormValue("skinPath"))
	defaultSongsPath := discoverOsuPaths()
	if songsPath == "" {
		songsPath = defaultSongsPath
	}
	if !isDirectory(songsPath) {
		http.Error(w, "Configure a pasta Songs do osu! nas opções de pastas locais", http.StatusBadRequest)
		return
	}
	skinFiles := r.MultipartForm.File["skinFiles"]
	if len(skinFiles) == 0 && skinPath != "" && !isDirectory(skinPath) {
		http.Error(w, "A pasta da skin configurada não existe", http.StatusBadRequest)
		return
	}
	musicVolume, err := parsePercent(r.FormValue("musicVolume"))
	if err != nil {
		http.Error(w, "Volume da música inválido", http.StatusBadRequest)
		return
	}
	hitsoundVolume, err := parsePercent(r.FormValue("hitsoundVolume"))
	if err != nil {
		http.Error(w, "Volume dos hitsounds inválido", http.StatusBadRequest)
		return
	}
	scrollSpeed, err := strconv.ParseFloat(r.FormValue("scrollSpeed"), 64)
	if err != nil || scrollSpeed < 5 || scrollSpeed > 40 {
		http.Error(w, "Scroll velocity deve estar entre 5 e 40", http.StatusBadRequest)
		return
	}

	id, err := newJobID()
	if err != nil {
		http.Error(w, "Não foi possível criar o trabalho de render", http.StatusInternalServerError)
		return
	}
	tempDir, err := os.MkdirTemp("", "mania-render-gui-*")
	if err != nil {
		http.Error(w, "Não foi possível preparar os arquivos: "+err.Error(), http.StatusInternalServerError)
		return
	}
	var uploadedSkinPath string
	if len(skinFiles) > 0 {
		skinPath, err = saveSkinDirectory(tempDir, skinFiles, r.MultipartForm.Value["skinRelativePaths"])
		if err != nil {
			_ = os.RemoveAll(tempDir)
			http.Error(w, "Não foi possível carregar a skin selecionada: "+err.Error(), http.StatusBadRequest)
			return
		}
		uploadedSkinPath = skinPath
	}
	replayHeader, err := requiredUpload(r, "replay")
	if err != nil || !strings.EqualFold(filepath.Ext(replayHeader.Filename), ".osr") {
		_ = os.RemoveAll(tempDir)
		http.Error(w, "Selecione um arquivo .osr válido", http.StatusBadRequest)
		return
	}
	osrPath := filepath.Join(tempDir, "replay.osr")
	if err := saveUpload(replayHeader, osrPath, maxReplayBytes); err != nil {
		_ = os.RemoveAll(tempDir)
		http.Error(w, "Não foi possível salvar o replay: "+err.Error(), http.StatusBadRequest)
		return
	}
	outputName := safeOutputName(r.FormValue("outputName"))
	outputPath := filepath.Join(tempDir, outputName)
	job := &renderJob{status: "rendering", message: "Lendo replay...", output: outputPath, tempDir: tempDir}
	renderJobs.Store(id, job)

	go func() {
		if uploadedSkinPath != "" {
			defer func() {
				if err := os.RemoveAll(uploadedSkinPath); err != nil {
					log.Printf("Não foi possível remover os arquivos temporários da skin: %v", err)
				}
			}()
		}
		mapHash, err := replay.BeatmapHash(osrPath)
		if err == nil {
			updateJobMessage(job, "Procurando beatmap nas músicas locais do osu!...")
			var osuPath string
			var starRating float64
			osuPath, starRating, err = beatmap.FindByMD5ProgressWithRating(songsPath, mapHash, func(count int) {
				updateJobMessage(job, fmt.Sprintf("Procurando beatmap nas músicas locais do osu!... %d verificados", count))
			})
			if err == nil {
				updateJobMessage(job, "Renderizando replay e áudio...")
				err = runWithProgress(osuPath, osrPath, skinPath, outputPath, 60, 1, 1, scrollSpeed, musicVolume, hitsoundVolume, 1280, 720, starRating, func(progress int) {
					updateJobProgress(job, progress)
				})
			}
		}
		job.mu.Lock()
		defer job.mu.Unlock()
		if err != nil {
			job.status = "error"
			job.message = err.Error()
			job.progressKnown = true
			return
		}
		job.status = "done"
		job.message = "Render concluído"
		job.progress = 100
		job.progressKnown = true
	}()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(map[string]string{"id": id})
}

func updateJobMessage(job *renderJob, message string) {
	job.mu.Lock()
	job.message = message
	job.mu.Unlock()
}

func updateJobProgress(job *renderJob, progress int) {
	job.mu.Lock()
	job.progress = progress
	job.progressKnown = true
	job.message = fmt.Sprintf("Renderizando replay... %d%%", progress)
	job.mu.Unlock()
}

func isDirectory(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func discoverOsuPaths() string {
	var roots []string
	for _, base := range []string{
		os.Getenv("LOCALAPPDATA"),
		os.Getenv("APPDATA"),
		os.Getenv("USERPROFILE"),
		os.Getenv("ProgramFiles(x86)"),
		os.Getenv("ProgramFiles"),
	} {
		if base == "" {
			continue
		}
		roots = append(roots,
			filepath.Join(base, "osu!"),
			filepath.Join(base, "Steam", "steamapps", "common", "osu!"),
		)
	}
	if runtime.GOOS == "windows" {
		for drive := 'A'; drive <= 'Z'; drive++ {
			driveRoot := string(drive) + ":" + string(filepath.Separator)
			roots = append(roots,
				filepath.Join(driveRoot, "osu!"),
				filepath.Join(driveRoot, "Steam", "steamapps", "common", "osu!"),
			)
		}
	}
	return discoverOsuPathsFromRoots(roots)
}

func discoverOsuPathsFromRoots(roots []string) string {
	var songsPath, fallbackSongsPath string
	var songsDatabaseSize int64 = -1
	for _, root := range roots {
		databaseInfo, databaseErr := os.Stat(filepath.Join(root, "osu!.db"))
		databaseSize := int64(-1)
		if databaseErr == nil && !databaseInfo.IsDir() {
			databaseSize = databaseInfo.Size()
		}
		candidateSongs := filepath.Join(root, "Songs")
		if isDirectory(candidateSongs) {
			if fallbackSongsPath == "" {
				fallbackSongsPath = candidateSongs
			}
			if databaseSize > songsDatabaseSize {
				songsPath = candidateSongs
				songsDatabaseSize = databaseSize
			}
		}
	}
	if songsPath == "" {
		songsPath = fallbackSongsPath
	}
	return songsPath
}

func requiredUpload(r *http.Request, name string) (*multipart.FileHeader, error) {
	header, err := optionalUpload(r, name)
	if err != nil {
		return nil, err
	}
	if header == nil {
		return nil, fmt.Errorf("%s is required", name)
	}
	return header, nil
}

func optionalUpload(r *http.Request, name string) (*multipart.FileHeader, error) {
	if r.MultipartForm == nil || len(r.MultipartForm.File[name]) == 0 {
		return nil, nil
	}
	return r.MultipartForm.File[name][0], nil
}

func handleJobStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Método não permitido", http.StatusMethodNotAllowed)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/status/")
	value, ok := renderJobs.Load(id)
	if !ok {
		http.NotFound(w, r)
		return
	}
	job := value.(*renderJob)
	job.mu.RLock()
	defer job.mu.RUnlock()
	response := map[string]interface{}{
		"status":        job.status,
		"message":       job.message,
		"progress":      job.progress,
		"progressKnown": job.progressKnown,
	}
	if job.status == "done" {
		response["download"] = "/download/" + id
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(response)
}

func handleDownload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Método não permitido", http.StatusMethodNotAllowed)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/download/")
	value, ok := renderJobs.Load(id)
	if !ok {
		http.NotFound(w, r)
		return
	}
	job := value.(*renderJob)
	job.mu.RLock()
	defer job.mu.RUnlock()
	if job.status != "done" {
		http.Error(w, "O render ainda não está pronto", http.StatusConflict)
		return
	}
	w.Header().Set("Content-Type", "video/mp4")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": filepath.Base(job.output)}))
	http.ServeFile(w, r, job.output)
}

func saveUpload(header *multipart.FileHeader, destination string, limit int64) error {
	if header.Size > limit {
		return fmt.Errorf("arquivo excede o limite de %d bytes", limit)
	}
	source, err := header.Open()
	if err != nil {
		return err
	}
	defer source.Close()
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	written, copyErr := io.Copy(output, io.LimitReader(source, limit+1))
	closeErr := output.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if written > limit {
		return fmt.Errorf("arquivo excede o limite de %d bytes", limit)
	}
	return nil
}

func saveSkinDirectory(tempDir string, headers []*multipart.FileHeader, relativePaths []string) (string, error) {
	if len(headers) > maxSkinFiles {
		return "", fmt.Errorf("a pasta excede o limite de %d arquivos", maxSkinFiles)
	}
	if len(headers) != len(relativePaths) {
		return "", fmt.Errorf("estrutura da pasta selecionada inválida")
	}

	root := filepath.Join(tempDir, "skin")
	if err := os.Mkdir(root, 0700); err != nil {
		return "", err
	}
	if len(headers) == 0 {
		return "", fmt.Errorf("a pasta selecionada não contém arquivos")
	}

	firstPath := strings.ReplaceAll(relativePaths[0], `\`, "/")
	stripRoot := strings.Contains(firstPath, "/")
	rootName := ""
	var totalBytes int64
	for i, header := range headers {
		relative := strings.TrimSpace(strings.ReplaceAll(relativePaths[i], `\`, "/"))
		if relative == "" || strings.HasPrefix(relative, "/") {
			return "", fmt.Errorf("caminho de arquivo inválido")
		}
		if filepath.IsAbs(filepath.FromSlash(relative)) || filepath.VolumeName(filepath.FromSlash(relative)) != "" {
			return "", fmt.Errorf("caminho absoluto não permitido")
		}

		parts := strings.Split(relative, "/")
		if stripRoot {
			if len(parts) < 2 || parts[0] == "" {
				return "", fmt.Errorf("estrutura da pasta selecionada inválida")
			}
			if i == 0 {
				rootName = parts[0]
			} else if parts[0] != rootName {
				return "", fmt.Errorf("selecione uma única pasta de skin")
			}
			relative = strings.Join(parts[1:], "/")
		} else if len(parts) != 1 {
			return "", fmt.Errorf("estrutura da pasta selecionada inválida")
		}

		cleaned := filepath.Clean(filepath.FromSlash(relative))
		if cleaned == "." || cleaned == ".." || filepath.IsAbs(cleaned) ||
			strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
			return "", fmt.Errorf("caminho de arquivo inválido")
		}
		remaining := maxSkinBytes - totalBytes
		if header.Size > remaining {
			return "", fmt.Errorf("os arquivos da skin excedem o limite de %d MB", maxSkinBytes>>20)
		}

		destination := filepath.Join(root, cleaned)
		if err := os.MkdirAll(filepath.Dir(destination), 0700); err != nil {
			return "", err
		}
		source, err := header.Open()
		if err != nil {
			return "", err
		}
		output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			_ = source.Close()
			return "", err
		}
		written, copyErr := io.Copy(output, io.LimitReader(source, remaining+1))
		sourceErr := source.Close()
		closeErr := output.Close()
		if copyErr != nil {
			return "", copyErr
		}
		if sourceErr != nil {
			return "", sourceErr
		}
		if closeErr != nil {
			return "", closeErr
		}
		if written > remaining {
			return "", fmt.Errorf("os arquivos da skin excedem o limite de %d MB", maxSkinBytes>>20)
		}
		totalBytes += written
	}

	info, err := os.Stat(filepath.Join(root, "skin.ini"))
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("a pasta selecionada precisa conter skin.ini")
		}
		return "", err
	}
	if info.IsDir() {
		return "", fmt.Errorf("skin.ini precisa ser um arquivo")
	}
	return root, nil
}

func parsePercent(value string) (float64, error) {
	percent, err := strconv.ParseFloat(value, 64)
	if err != nil || percent < 0 || percent > 100 {
		return 0, fmt.Errorf("percent must be 0..100")
	}
	return percent, nil
}

func safeOutputName(name string) string {
	name = filepath.Base(strings.TrimSpace(name))
	if name == "" || name == "." || name == string(filepath.Separator) {
		name = "mania-render.mp4"
	}
	if !strings.EqualFold(filepath.Ext(name), ".mp4") {
		name += ".mp4"
	}
	return name
}

func newJobID() (string, error) {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes[:]), nil
}

const guiHTML = `<!doctype html>
<html lang="pt-BR">
<head>
<meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Scroller Renderer</title>
<style>
:root{color-scheme:dark;--bg:#111315;--line:#303438;--accent:#8ab4f8;--muted:#a3a7ad;--text:#e5e7eb}
*{box-sizing:border-box}
body{margin:0;min-height:100vh;background:var(--bg);color:var(--text);font:15px/1.5 "Segoe UI",sans-serif}
main{width:min(780px,calc(100% - 32px));margin:36px auto 56px}
.brand{margin-bottom:28px}
h1{font-size:25px;font-weight:600;margin:0}
header p{margin:4px 0 0;color:var(--muted)}
form{display:grid;gap:22px}
.card{padding:0 0 22px;border:0;border-bottom:1px solid var(--line);background:transparent}
.card h2{margin:0 0 14px;font-size:17px;font-weight:600;color:var(--text)}
.grid{display:grid;grid-template-columns:1fr 1fr;gap:16px}
.field{display:grid;gap:7px}
.field span,.label{font-size:14px;color:var(--text)}
.field small{color:var(--muted)}
input[type=file],input[type=text],input[type=number]{width:100%;border:1px solid #45494e;border-radius:4px;background:#17191c;color:var(--text);padding:9px 10px}
input[type=number]{font-variant-numeric:tabular-nums}
input[type=file]::file-selector-button{background:#292c30;color:var(--text);border:1px solid #45494e;border-radius:3px;padding:6px 9px;margin-right:9px}
.setting{padding:12px 0;border-bottom:1px solid #292c30}
.setting:last-child{border:0;padding-bottom:0}
.setting-head{display:flex;justify-content:space-between;align-items:center}
.number-control{display:flex;align-items:center;gap:10px;margin-top:6px}
.number-control input{max-width:120px}
.unit{color:var(--muted)}
.note{font-size:12px;color:var(--muted);margin:7px 0 0}
.submit{justify-self:start;border:1px solid #d6dae0;border-radius:4px;background:#d6dae0;color:#17191c;font-weight:600;font-size:15px;padding:10px 16px;cursor:pointer}
.submit:disabled{opacity:.6;cursor:wait}
#status{display:none;margin-top:16px;padding:12px;border:1px solid var(--line);border-radius:4px;background:#17191c}
#status.error{border-color:#a94c4c;color:#f0b4b4}
#status a{color:var(--accent);font-weight:600}
.progress-track{height:6px;margin-top:12px;overflow:hidden;border-radius:2px;background:#303438}
.progress-fill{height:100%;width:0;background:var(--accent);transition:width .25s ease}
.progress-fill.indeterminate{width:34%;animation:progress-loading 1.25s ease-in-out infinite}
@keyframes progress-loading{from{transform:translateX(-110%)}to{transform:translateX(310%)}}
.progress-label{margin-top:6px;text-align:right;font-size:12px;color:var(--muted);font-variant-numeric:tabular-nums}
#status.error .progress-fill{background:#c96b6b}
details.card{padding-top:2px}
details summary{cursor:pointer;font-size:15px;font-weight:600;color:var(--text)}
details .grid{margin-top:14px}
@media(max-width:650px){main{margin:24px auto}.grid{grid-template-columns:1fr}h1{font-size:22px}}
</style></head>
<body><main>
<header class="brand"><h1>Scroller Renderer</h1><p>Renderize replays de osu!mania usando os arquivos locais do mapa.</p></header>
<form id="form" enctype="multipart/form-data">
<section class="card"><h2>Replay e skin</h2><div class="grid">
<label class="field"><span>Replay (.osr)</span><input name="replay" type="file" accept=".osr" required><small>O beatmap e o áudio devem estar na pasta Songs do osu!.</small></label>
<label class="field"><span>Skin personalizada (opcional)</span><input id="skinFiles" name="skinFiles" type="file" webkitdirectory directory multiple><small id="skinSelection">Escolha a pasta que contém skin.ini. Ela será usada somente neste render.</small></label>
</div></section>
<details class="card"><summary>Pastas locais</summary><p class="note">Configure apenas se a detecção automática não encontrar os arquivos.</p><div class="grid">
<label class="field"><span>Pasta Songs do osu!</span><input name="songsPath" type="text" placeholder="Ex.: C:\osu!\Songs"><small>Usada para encontrar o .osu e o áudio pelo hash do replay.</small></label>
<label class="field"><span>Caminho local da skin (alternativa)</span><input name="skinPath" type="text" placeholder="Ex.: C:\osu!\Skins\MinhaSkin"><small>Alternativa à seleção da pasta acima. Deixe em branco para usar os elementos integrados.</small></label>
</div></details>
<section class="card"><h2>Configurações</h2>
<div class="setting"><div class="setting-head"><label for="musicVolume">Volume da música</label></div><div class="number-control"><input id="musicVolume" name="musicVolume" type="number" min="0" max="100" step="1" value="100" required><span class="unit">%</span></div></div>
<div class="setting"><div class="setting-head"><label for="hitsoundVolume">Volume dos hitsounds</label></div><div class="number-control"><input id="hitsoundVolume" name="hitsoundVolume" type="number" min="0" max="100" step="1" value="100" required><span class="unit">%</span></div></div>
<div class="setting"><div class="setting-head"><label for="scrollSpeed">Scroll velocity do replay</label></div><div class="number-control"><input id="scrollSpeed" name="scrollSpeed" type="number" min="5" max="40" step="0.1" value="12" required></div><p class="note">Valores menores deixam as notas mais lentas; ajuste entre 5 e 40.</p></div>
<label class="field" style="margin-top:18px"><span>Nome do arquivo de saída</span><input name="outputName" type="text" value="mania-render.mp4" maxlength="100"></label>
</section><button class="submit" id="submit" type="submit">Renderizar replay</button>
</form><div id="status" role="status" aria-live="polite"><div id="statusMessage"></div><div class="progress-track"><div id="progressBar" class="progress-fill" role="progressbar" aria-label="Progresso do replay" aria-valuemin="0" aria-valuemax="100"></div></div><div id="progressLabel" class="progress-label">Preparando…</div></div>
<script>
const form=document.querySelector('#form'),status=document.querySelector('#status'),statusMessage=document.querySelector('#statusMessage'),progressBar=document.querySelector('#progressBar'),progressLabel=document.querySelector('#progressLabel'),submit=document.querySelector('#submit');
const songsPath=form.elements.songsPath,skinPath=form.elements.skinPath,skinFiles=form.elements.skinFiles,skinSelection=document.querySelector('#skinSelection');
function setProgress(result){
 const progress=Math.max(0,Math.min(100,Number(result.progress)||0));
 progressBar.classList.toggle('indeterminate',!result.progressKnown);
 if(result.progressKnown){progressBar.setAttribute('aria-valuenow',String(progress));progressBar.style.width=progress+'%';progressLabel.textContent=progress+'%'}
 else{progressBar.removeAttribute('aria-valuenow');progressBar.style.width='34%';progressLabel.textContent='Preparando…'}
}
fetch('/defaults').then(response=>response.json()).then(paths=>{
 songsPath.value=localStorage.getItem('songsPath')||paths.songsPath||'';
 skinPath.value=localStorage.getItem('skinPath')||'';
}).catch(()=>{});
for(const input of [songsPath,skinPath])input.addEventListener('change',()=>localStorage.setItem(input.name,input.value));
skinFiles.addEventListener('change',()=>{
 if(skinFiles.files.length){
  skinPath.value='';localStorage.removeItem('skinPath');
  const first=skinFiles.files[0],root=(first.webkitRelativePath||first.name).split(/[\\/]/)[0];
  skinSelection.textContent=root+' — '+skinFiles.files.length+' arquivos';
 }
});
skinPath.addEventListener('input',()=>{
 if(skinPath.value.trim()){skinFiles.value='';skinSelection.textContent='Usando o caminho local informado abaixo.'}
});
form.addEventListener('submit',async event=>{
 event.preventDefault();submit.disabled=true;submit.textContent='Enviando arquivos…';status.className='';status.style.display='block';statusMessage.textContent='Preparando render…';setProgress({progress:0,progressKnown:false});
 try{
  const formData=new FormData(form);formData.delete('skinFiles');
  for(const file of skinFiles.files){formData.append('skinFiles',file,file.name);formData.append('skinRelativePaths',file.webkitRelativePath||file.name)}
  const response=await fetch('/render',{method:'POST',body:formData}),body=await response.text();let data;try{data=JSON.parse(body)}catch{data={error:body||'Falha ao iniciar o render.'}};
  if(!response.ok)throw new Error(data.error||'Falha ao iniciar o render.');
  submit.textContent='Buscando beatmap…';statusMessage.textContent='Renderizando… isso pode levar alguns minutos.';
  const poll=async()=>{try{const response=await fetch('/status/'+data.id);if(!response.ok)throw new Error('servidor indisponível');const result=await response.json();statusMessage.textContent=result.message||'Renderizando…';setProgress(result);
   if(result.status==='done'){statusMessage.textContent='Render concluído. ';const link=document.createElement('a');link.href=result.download;link.textContent='Baixar vídeo MP4';statusMessage.appendChild(link);submit.disabled=false;submit.textContent='Renderizar outro';return}
   if(result.status==='error'){status.className='error';statusMessage.textContent='Erro: '+result.message;submit.disabled=false;submit.textContent='Tentar novamente';return}
   if(result.message&&result.message.startsWith('Renderizando replay'))submit.textContent='Renderizando…';
   setTimeout(poll,1200)
  }catch(error){status.className='error';if(progressBar.classList.contains('indeterminate'))setProgress({progress:0,progressKnown:true});statusMessage.textContent='Conexão com o renderizador perdida. Reinicie com -gui e envie o replay novamente.';submit.disabled=false;submit.textContent='Tentar novamente'}
  };setTimeout(poll,800)
 }catch(error){status.className='error';setProgress({progress:0,progressKnown:true});statusMessage.textContent=error.message;submit.disabled=false;submit.textContent='Tentar novamente'}
});
</script></main></body></html>`
