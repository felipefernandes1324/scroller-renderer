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
	presetSkinName = "Nameless_GodBuffv2"
	maxReplayBytes = int64(128 << 20)
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
	songsPath, skinPath := discoverOsuPaths()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"songsPath": songsPath,
		"skinPath":  skinPath,
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
	r.Body = http.MaxBytesReader(w, r.Body, maxReplayBytes+(1<<20))
	if err := r.ParseMultipartForm(16 << 20); err != nil {
		http.Error(w, "Não foi possível ler os arquivos enviados: "+err.Error(), http.StatusBadRequest)
		return
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	if r.FormValue("skinName") != presetSkinName {
		http.Error(w, "Skin inválida", http.StatusBadRequest)
		return
	}
	songsPath := strings.TrimSpace(r.FormValue("songsPath"))
	skinPath := strings.TrimSpace(r.FormValue("skinPath"))
	defaultSongsPath, defaultSkinPath := discoverOsuPaths()
	if songsPath == "" {
		songsPath = defaultSongsPath
	}
	if skinPath == "" {
		skinPath = defaultSkinPath
	}
	if !isDirectory(songsPath) {
		http.Error(w, "Configure a pasta Songs do osu! nas opções de pastas locais", http.StatusBadRequest)
		return
	}
	if skinPath != "" && !isDirectory(skinPath) {
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

func discoverOsuPaths() (string, string) {
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

func discoverOsuPathsFromRoots(roots []string) (string, string) {
	var songsPath, fallbackSongsPath string
	var songsDatabaseSize int64 = -1
	var skinPath, fallbackSkinPath string
	var skinDatabaseSize int64 = -1
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
		candidateSkin := filepath.Join(root, "Skins", presetSkinName)
		if isDirectory(candidateSkin) {
			if fallbackSkinPath == "" {
				fallbackSkinPath = candidateSkin
			}
			if databaseSize > skinDatabaseSize {
				skinPath = candidateSkin
				skinDatabaseSize = databaseSize
			}
		}
	}
	if songsPath == "" {
		songsPath = fallbackSongsPath
	}
	if skinPath == "" {
		skinPath = fallbackSkinPath
	}
	return songsPath, skinPath
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
:root{color-scheme:dark;--bg:#080b13;--panel:#111725;--line:#25354a;--cyan:#54e4ff;--pink:#d98aff;--muted:#91a1b9;--text:#eef5ff}
*{box-sizing:border-box}body{margin:0;min-height:100vh;background:radial-gradient(ellipse at 12% 0%,#14253b 0,transparent 42%),var(--bg);color:var(--text);font:15px/1.5 "Segoe UI",sans-serif}
main{width:min(920px,calc(100% - 32px));margin:48px auto 70px}.brand{display:flex;align-items:center;gap:14px;margin-bottom:28px}.mark{width:42px;height:42px;border:1px solid var(--cyan);border-radius:12px;display:grid;place-items:center;color:var(--cyan);font-size:22px;box-shadow:0 0 24px #54e4ff33}
h1{font-size:24px;letter-spacing:.08em;margin:0}header p{margin:3px 0 0;color:var(--muted)}form{display:grid;gap:18px}.card{background:linear-gradient(145deg,#141c2b,#0e1420);border:1px solid var(--line);border-radius:16px;padding:22px;box-shadow:0 14px 40px #0003}.card h2{margin:0 0 18px;font-size:13px;text-transform:uppercase;letter-spacing:.16em;color:var(--cyan)}
.grid{display:grid;grid-template-columns:1fr 1fr;gap:16px}.field{display:grid;gap:8px}.field span,.label{font-size:13px;color:#c3cee0}.field small{color:var(--muted)}input[type=file],input[type=text]{width:100%;border:1px solid #304057;border-radius:9px;background:#0a101b;color:var(--text);padding:11px}input[type=number]{width:120px;border:1px solid #304057;border-radius:9px;background:#0a101b;color:var(--text);padding:10px;font-variant-numeric:tabular-nums}input[type=file]::file-selector-button{background:#1b2b40;color:var(--cyan);border:0;border-radius:6px;padding:7px 10px;margin-right:10px}
.setting{padding:14px 0;border-bottom:1px solid #243145}.setting:last-child{border:0;padding-bottom:0}.setting-head{display:flex;justify-content:space-between;align-items:center}.number-control{display:flex;align-items:center;gap:10px;margin-top:8px}.unit{color:var(--muted)}
.select{border:1px solid #304057;border-radius:9px;padding:12px;background:#0a101b;color:var(--text);width:100%;margin-top:8px}.note{font-size:12px;color:var(--muted);margin:7px 0 0}.submit{border:0;border-radius:11px;background:linear-gradient(100deg,#43dff7,#a77bfa);color:#09111d;font-weight:800;font-size:15px;padding:15px;cursor:pointer;letter-spacing:.04em}.submit:disabled{opacity:.55;cursor:wait}#status{display:none;margin-top:18px;padding:14px;border:1px solid var(--line);border-radius:10px;background:#101827}#status.error{border-color:#ff6a8b;color:#ffb5c5}#status a{color:var(--cyan);font-weight:700}.progress-track{height:10px;margin-top:14px;overflow:hidden;border:1px solid #304057;border-radius:8px;background:#080d16}.progress-fill{height:100%;width:0;background:linear-gradient(90deg,#43dff7,#a77bfa);border-radius:inherit;transition:width .25s ease}.progress-fill.indeterminate{width:34%;animation:progress-loading 1.25s ease-in-out infinite}@keyframes progress-loading{from{transform:translateX(-110%)}to{transform:translateX(310%)}}.progress-label{margin-top:6px;text-align:right;font-size:12px;color:var(--muted);font-variant-numeric:tabular-nums}#status.error .progress-fill{background:#ff6a8b}.tag{display:inline-block;font-size:10px;color:var(--cyan);border:1px solid #32566a;border-radius:20px;padding:3px 9px;margin-left:8px}details.card{padding:18px 22px}details summary{cursor:pointer;color:var(--cyan);font-size:13px;font-weight:700;letter-spacing:.1em;text-transform:uppercase}details .grid{margin-top:16px}
@media(max-width:650px){main{margin:26px auto}.grid{grid-template-columns:1fr}.card{padding:17px}h1{font-size:20px}}
</style></head>
<body><main>
<header class="brand"><div class="mark">♪</div><div><h1>SCROLLER RENDERER <span class="tag">LOCAL</span></h1><p>Envie o replay; mapa e áudio vêm da pasta Songs do osu!.</p></div></header>
<form id="form" enctype="multipart/form-data">
<section class="card"><h2>Arquivos do replay</h2><div class="grid">
<label class="field"><span>Replay (.osr)</span><input name="replay" type="file" accept=".osr" required><small>O beatmap e o áudio devem estar na pasta Songs do osu!.</small></label>
<label class="field"><span>Skin selecionada</span><select class="select" name="skinName"><option value="Nameless_GodBuffv2">Nameless_GodBuffv2</option></select><small>Carrega a skin instalada se encontrada; caso contrário, usa a skin padrão integrada.</small></label>
</div></section>
<details class="card"><summary>Pastas locais — configurar se não forem detectadas</summary><div class="grid">
<label class="field"><span>Pasta Songs do osu!</span><input name="songsPath" type="text" placeholder="Ex.: C:\osu!\Songs"><small>Usada para encontrar o .osu e o áudio pelo hash do replay.</small></label>
<label class="field"><span>Pasta instalada da skin (opcional)</span><input name="skinPath" type="text" placeholder="Ex.: C:\osu!\Skins\Nameless_GodBuffv2"><small>Deixe em branco para detectar automaticamente ou usar os elementos padrão.</small></label>
</div></details>
<section class="card"><h2>Controles de render</h2>
<div class="setting"><div class="setting-head"><label for="musicVolume">Volume da música</label></div><div class="number-control"><input id="musicVolume" name="musicVolume" type="number" min="0" max="100" step="1" value="100" required><span class="unit">%</span></div></div>
<div class="setting"><div class="setting-head"><label for="hitsoundVolume">Volume dos hitsounds</label></div><div class="number-control"><input id="hitsoundVolume" name="hitsoundVolume" type="number" min="0" max="100" step="1" value="100" required><span class="unit">%</span></div></div>
<div class="setting"><div class="setting-head"><label for="scrollSpeed">Scroll velocity do replay</label></div><div class="number-control"><input id="scrollSpeed" name="scrollSpeed" type="number" min="5" max="40" step="0.1" value="12" required></div><p class="note">Valores menores deixam as notas mais lentas; ajuste entre 5 e 40.</p></div>
<label class="field" style="margin-top:18px"><span>Nome do arquivo de saída</span><input name="outputName" type="text" value="mania-render.mp4" maxlength="100"></label>
</section><button class="submit" id="submit" type="submit">RENDERIZAR REPLAY</button>
</form><div id="status" role="status" aria-live="polite"><div id="statusMessage"></div><div class="progress-track"><div id="progressBar" class="progress-fill" role="progressbar" aria-label="Progresso do replay" aria-valuemin="0" aria-valuemax="100"></div></div><div id="progressLabel" class="progress-label">Preparando…</div></div>
<script>
const form=document.querySelector('#form'),status=document.querySelector('#status'),statusMessage=document.querySelector('#statusMessage'),progressBar=document.querySelector('#progressBar'),progressLabel=document.querySelector('#progressLabel'),submit=document.querySelector('#submit');
const songsPath=form.elements.songsPath,skinPath=form.elements.skinPath;
function setProgress(result){
 const progress=Math.max(0,Math.min(100,Number(result.progress)||0));
 progressBar.classList.toggle('indeterminate',!result.progressKnown);
 if(result.progressKnown){progressBar.setAttribute('aria-valuenow',String(progress));progressBar.style.width=progress+'%';progressLabel.textContent=progress+'%'}
 else{progressBar.removeAttribute('aria-valuenow');progressBar.style.width='34%';progressLabel.textContent='Preparando…'}
}
fetch('/defaults').then(response=>response.json()).then(paths=>{
 songsPath.value=localStorage.getItem('songsPath')||paths.songsPath||'';
 const savedSkinPath=localStorage.getItem('skinPath')||'';
 const savedSkinName=savedSkinPath.replace(/[\\/]+$/,'').split(/[\\/]/).pop()||'';
 skinPath.value=savedSkinName.toLowerCase()===form.elements.skinName.value.toLowerCase()?savedSkinPath:paths.skinPath||'';
}).catch(()=>{});
for(const input of [songsPath,skinPath])input.addEventListener('change',()=>localStorage.setItem(input.name,input.value));
form.addEventListener('submit',async event=>{
 event.preventDefault();submit.disabled=true;submit.textContent='ENVIANDO ARQUIVOS…';status.className='';status.style.display='block';statusMessage.textContent='Preparando render…';setProgress({progress:0,progressKnown:false});
 try{
  const response=await fetch('/render',{method:'POST',body:new FormData(form)}),body=await response.text();let data;try{data=JSON.parse(body)}catch{data={error:body||'Falha ao iniciar o render.'}};
  if(!response.ok)throw new Error(data.error||'Falha ao iniciar o render.');
  submit.textContent='BUSCANDO BEATMAP…';statusMessage.textContent='Renderizando… isso pode levar alguns minutos.';
  const poll=async()=>{try{const response=await fetch('/status/'+data.id);if(!response.ok)throw new Error('servidor indisponível');const result=await response.json();statusMessage.textContent=result.message||'Renderizando…';setProgress(result);
   if(result.status==='done'){statusMessage.textContent='Render concluído. ';const link=document.createElement('a');link.href=result.download;link.textContent='Baixar vídeo MP4';statusMessage.appendChild(link);submit.disabled=false;submit.textContent='RENDERIZAR OUTRO';return}
   if(result.status==='error'){status.className='error';statusMessage.textContent='Erro: '+result.message;submit.disabled=false;submit.textContent='TENTAR NOVAMENTE';return}
   if(result.message&&result.message.startsWith('Renderizando replay'))submit.textContent='RENDERIZANDO…';
   setTimeout(poll,1200)
  }catch(error){status.className='error';if(progressBar.classList.contains('indeterminate'))setProgress({progress:0,progressKnown:true});statusMessage.textContent='Conexão com o renderizador perdida. Reinicie com -gui e envie o replay novamente.';submit.disabled=false;submit.textContent='TENTAR NOVAMENTE'}
  };setTimeout(poll,800)
 }catch(error){status.className='error';setProgress({progress:0,progressKnown:true});statusMessage.textContent=error.message;submit.disabled=false;submit.textContent='TENTAR NOVAMENTE'}
});
</script></main></body></html>`
