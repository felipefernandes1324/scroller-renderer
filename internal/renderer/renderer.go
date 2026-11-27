package renderer

import (
	"fmt"
	"image"
	"image/color"
	_ "image/jpeg"
	_ "image/png"
	"math"
	"os"
	"sort"

	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"

	"github.com/felipefernandes1324/scroller-renderer/internal/beatmap"
	"github.com/felipefernandes1324/scroller-renderer/internal/judge"
	"github.com/felipefernandes1324/scroller-renderer/internal/replay"
	"github.com/felipefernandes1324/scroller-renderer/internal/skin"
	"github.com/felipefernandes1324/scroller-renderer/internal/sv"
)

type Renderer struct {
	Width, Height      int
	Map                *beatmap.Map
	Skin               *skin.Skin
	Timeline           *sv.Timeline
	Stats              judge.Stats
	Replay             []replay.Event
	Speed              float64
	ScrollSpeed        float64
	DurationMS         float64
	ShowScoreResult    bool
	ScoreResultStartMS float64

	prepared       bool
	scale          float64
	stageLeft      int
	stageRight     int
	stageWidth     int
	receptorY      int
	laneLefts      []int
	laneWidths     []int
	baseBackdrop   []byte
	noteSprites    []*image.NRGBA
	headSprites    []*image.NRGBA
	bodySprites    []*image.NRGBA
	tailSprites    []*image.NRGBA
	keySprites     []*image.NRGBA
	keyDownSprites []*image.NRGBA
	hitSprites     map[judge.Grade]*image.NRGBA
	stageLight     *image.NRGBA
	stageHint      *image.NRGBA
	stageBottom    *image.NRGBA
	lightingN      []*image.NRGBA
	lightingL      []*image.NRGBA
	scoreGlyphs    map[rune]*image.NRGBA
	resultGlyphs   map[rune]*image.NRGBA
	comboGlyphs    map[rune]*image.NRGBA
	notes          []noteVisual
	comboFace      font.Face
	accFace        font.Face
	scoreFace      font.Face
	judgeFace      font.Face
	metaFace       font.Face
	windows        judge.TimingWindows
	gradeStats     []gradeSnapshot
}

type noteVisual struct {
	object        beatmap.HitObject
	startPosition float64
	endPosition   float64
	resultIndex   int
}

type gradeSnapshot struct {
	max, perfect, great, good, bad, miss int
	hitCount                             int
	hitErrorSum, hitErrorSquareSum       float64
}

func (r *Renderer) Frame(timeMS float64) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, r.Width, r.Height))
	r.FrameInto(timeMS, img)
	return img
}

func (r *Renderer) FrameInto(timeMS float64, img *image.RGBA) {
	r.prepare()
	copy(img.Pix, r.baseBackdrop)
	if r.ShowScoreResult && timeMS >= r.ScoreResultStartMS {
		r.drawScoreResult(img)
		return
	}

	mask := keyMaskAt(r.Replay, timeMS)
	for lane := 0; lane < r.Map.Keys; lane++ {
		if mask&(uint32(1)<<lane) != 0 {
			r.drawColumnLight(img, lane)
		}
	}
	r.drawHitEffects(img, timeMS)

	// Receptors underneath active notes (matches osu!mania classic circle skin layering).
	for lane := 0; lane < r.Map.Keys; lane++ {
		pressed := mask&(uint32(1)<<lane) != 0
		sprite := r.keySprites[lane]
		if pressed {
			sprite = r.keyDownSprites[lane]
		}
		if sprite != nil {
			cx := r.laneLefts[lane] + r.laneWidths[lane]/2
			blitNRGBA(img, sprite, cx-sprite.Bounds().Dx()/2, r.receptorY-sprite.Bounds().Dy()/2, 1.0)
		}
	}

	scroll := r.ScrollSpeed
	if scroll <= 0 {
		scroll = 24
	}
	pixelsPerMS := (scroll / 28.0) * r.scale
	nowPos := r.Timeline.Position(timeMS)

	for _, note := range r.notes {
		object := note.object
		res := r.Stats.Results[note.resultIndex]
		lane := object.Lane
		cx := r.laneLefts[lane] + r.laneWidths[lane]/2

		if object.Long {
			// Completed long notes vanish once released.
			if res.Grade != judge.Miss && timeMS > res.ReleaseTime {
				continue
			}
			y := float64(r.receptorY) - (note.startPosition-nowPos)*pixelsPerMS
			endY := float64(r.receptorY) - (note.endPosition-nowPos)*pixelsPerMS

			held := res.Grade != judge.Miss && timeMS >= res.HitTime && timeMS <= res.ReleaseTime
			if held && y > float64(r.receptorY) {
				y = float64(r.receptorY)
			}

			alpha := 1.0
			if res.Grade == judge.Miss && timeMS > res.HitTime {
				alpha = 0.42
			}
			if math.Max(y, endY) < -float64(r.laneWidths[lane]) || math.Min(y, endY) > float64(r.Height)+float64(r.laneWidths[lane]) {
				continue
			}

			body := r.bodySprites[lane]
			head := r.headSprites[lane]
			tail := r.tailSprites[lane]

			bodyTop := int(math.Round(math.Min(y, endY)))
			bodyBottom := int(math.Round(math.Max(y, endY)))
			if body != nil && bodyBottom > bodyTop {
				noteW := noteSpriteWidth(r.laneWidths[lane])
				blitLongBody(img, body, cx-noteW/2, bodyTop, noteW, bodyBottom-bodyTop, alpha)
			}
			if tail != nil {
				blitNRGBA(img, tail, cx-tail.Bounds().Dx()/2, int(math.Round(endY))-tail.Bounds().Dy()/2, alpha)
			}
			if head != nil && (held || y < float64(r.Height)+float64(head.Bounds().Dy())) {
				blitNRGBA(img, head, cx-head.Bounds().Dx()/2, int(math.Round(y))-head.Bounds().Dy()/2, alpha)
			}
		} else {
			if res.Grade != judge.Miss && timeMS >= res.HitTime {
				continue
			}
			y := float64(r.receptorY) - (note.startPosition-nowPos)*pixelsPerMS
			sprite := r.noteSprites[lane]
			if sprite == nil {
				continue
			}
			halfH := float64(sprite.Bounds().Dy()) / 2.0
			if y < -halfH*2 || y > float64(r.Height)+halfH*2 {
				continue
			}
			alpha := 1.0
			if res.Grade == judge.Miss && timeMS > res.HitTime {
				fade := 1.0 - (timeMS-res.HitTime)/140.0
				if fade <= 0 {
					continue
				}
				alpha = fade
			}
			blitNRGBA(img, sprite, cx-sprite.Bounds().Dx()/2, int(math.Round(y))-sprite.Bounds().Dy()/2, alpha)
		}
	}

	r.drawHUD(img, timeMS)
}

func (r *Renderer) prepare() {
	if r.prepared {
		return
	}
	r.prepared = true
	r.scale = float64(r.Height) / 480.0
	if r.scale < 0.5 {
		r.scale = 0.5
	}
	r.windows = judge.Windows(r.Map.OD)
	r.gradeStats = make([]gradeSnapshot, len(r.Stats.Results)+1)
	for i, result := range r.Stats.Results {
		current := r.gradeStats[i+1]
		switch result.Grade {
		case judge.MAX:
			current.max++
		case judge.Hit300:
			current.perfect++
		case judge.Hit200:
			current.great++
		case judge.Hit100:
			current.good++
		case judge.Hit50:
			current.bad++
		default:
			current.miss++
		}
		if result.Grade != judge.Miss {
			current.hitCount++
			current.hitErrorSum += result.HitError
			current.hitErrorSquareSum += result.HitError * result.HitError
		}
		previous := r.gradeStats[i]
		current.max += previous.max
		current.perfect += previous.perfect
		current.great += previous.great
		current.good += previous.good
		current.bad += previous.bad
		current.miss += previous.miss
		current.hitCount += previous.hitCount
		current.hitErrorSum += previous.hitErrorSum
		current.hitErrorSquareSum += previous.hitErrorSquareSum
		r.gradeStats[i+1] = current
	}

	keys := r.Map.Keys
	r.laneWidths = make([]int, keys)
	r.laneLefts = make([]int, keys)
	totalRaw := 0.0
	for i := 0; i < keys; i++ {
		w := r.Skin.Mania.ColumnWidths[i]
		if w <= 0 {
			w = 44
		}
		totalRaw += w
	}
	fitScale := r.scale
	if totalRaw*fitScale > float64(r.Width)*0.82 {
		fitScale = (float64(r.Width) * 0.82) / totalRaw
	}
	r.stageWidth = 0
	for i := 0; i < keys; i++ {
		w := int(math.Round(r.Skin.Mania.ColumnWidths[i] * fitScale))
		if w < 12 {
			w = 12
		}
		r.laneWidths[i] = w
		r.stageWidth += w
	}
	r.stageLeft = (r.Width - r.stageWidth) / 2
	r.stageRight = r.stageLeft + r.stageWidth
	curX := r.stageLeft
	for i := 0; i < keys; i++ {
		r.laneLefts[i] = curX
		curX += r.laneWidths[i]
	}

	r.receptorY = int(math.Round(r.Skin.Mania.HitPosition * r.scale))
	if r.receptorY > r.Height-int(34*r.scale) {
		r.receptorY = r.Height - int(34*r.scale)
	}

	r.buildBackdrop()
	r.prepareSprites()
	r.prepareFonts()
	r.prepareNotes()
}

func (r *Renderer) buildBackdrop() {
	bgImg := image.NewRGBA(image.Rect(0, 0, r.Width, r.Height))
	loadedBG := false
	if bgPath := r.Map.BackgroundPath(); bgPath != "" {
		if f, err := os.Open(bgPath); err == nil {
			if decoded, _, decErr := image.Decode(f); decErr == nil {
				drawCoverDim(bgImg, decoded, 0.16)
				loadedBG = true
			}
			_ = f.Close()
		}
	}
	if !loadedBG {
		// Subtle dark studio gradient when the beatmap has no background file.
		cx, cy := float64(r.Width)/2.0, float64(r.Height)/2.0
		maxD := math.Hypot(cx, cy)
		for y := 0; y < r.Height; y++ {
			for x := 0; x < r.Width; x++ {
				t := 1.0 - math.Hypot(float64(x)-cx, float64(y)-cy)/maxD
				v := uint8(6 + 12*t*t)
				idx := (y*r.Width + x) * 4
				bgImg.Pix[idx] = v
				bgImg.Pix[idx+1] = v + 2
				bgImg.Pix[idx+2] = v + 5
				bgImg.Pix[idx+3] = 255
			}
		}
	}

	// Draw lane columns with skin colors (nearly opaque black by default).
	for lane := 0; lane < r.Map.Keys; lane++ {
		c := r.Skin.Mania.ColumnColors[lane]
		fillAlpha(bgImg, r.laneLefts[lane], 0, r.laneLefts[lane]+r.laneWidths[lane], r.Height, c)
		if lane > 0 && r.Skin.Mania.ColumnLineWidth[lane] > 0 {
			lw := int(math.Max(1, math.Round(r.Skin.Mania.ColumnLineWidth[lane]*r.scale)))
			fillAlpha(bgImg, r.laneLefts[lane], 0, r.laneLefts[lane]+lw, r.Height, color.NRGBA{R: 70, G: 76, B: 88, A: 140})
		}
	}

	// Clean outer stage borders (skin sprites if available, otherwise crisp light lines).
	borderW := int(math.Max(2, math.Round(2.2*r.scale)))
	if leftImg := r.Skin.Image(r.Skin.Mania.StageLeft); usableSkinSprite(leftImg) {
		targetW := int(math.Round(float64(leftImg.Bounds().Dx()) * r.scale * 0.6))
		if targetW < borderW {
			targetW = borderW
		}
		scaled := scaleNRGBA(leftImg, targetW, r.Height)
		blitNRGBA(bgImg, scaled, r.stageLeft-targetW, 0, 1.0)
	} else {
		fillAlpha(bgImg, r.stageLeft-borderW, 0, r.stageLeft, r.Height, color.NRGBA{R: 205, G: 212, B: 224, A: 245})
	}
	if rightImg := r.Skin.Image(r.Skin.Mania.StageRight); usableSkinSprite(rightImg) {
		targetW := int(math.Round(float64(rightImg.Bounds().Dx()) * r.scale * 0.6))
		if targetW < borderW {
			targetW = borderW
		}
		scaled := scaleNRGBA(rightImg, targetW, r.Height)
		blitNRGBA(bgImg, scaled, r.stageRight, 0, 1.0)
	} else {
		fillAlpha(bgImg, r.stageRight, 0, r.stageRight+borderW, r.Height, color.NRGBA{R: 205, G: 212, B: 224, A: 245})
	}

	if src := r.Skin.Image(r.Skin.Mania.StageHint); usableSkinSprite(src) {
		h := scaledHeight(src, r.stageWidth)
		r.stageHint = scaleNRGBA(src, r.stageWidth, h)
		blitNRGBA(bgImg, r.stageHint, r.stageLeft, r.receptorY-r.stageHint.Bounds().Dy()/2, 1)
	}
	if src := r.Skin.Image(r.Skin.Mania.StageBottom); usableSkinSprite(src) {
		w := int(math.Round(float64(src.Bounds().Dx()) * r.scale))
		h := int(math.Round(float64(src.Bounds().Dy()) * r.scale))
		r.stageBottom = scaleNRGBA(src, w, h)
		blitNRGBA(bgImg, r.stageBottom, (r.Width-w)/2, r.Height-h, 1)
	}

	r.baseBackdrop = append([]byte(nil), bgImg.Pix...)
}

func (r *Renderer) prepareSprites() {
	keys := r.Map.Keys
	r.noteSprites = make([]*image.NRGBA, keys)
	r.headSprites = make([]*image.NRGBA, keys)
	r.bodySprites = make([]*image.NRGBA, keys)
	r.tailSprites = make([]*image.NRGBA, keys)
	r.keySprites = make([]*image.NRGBA, keys)
	r.keyDownSprites = make([]*image.NRGBA, keys)

	for i := 0; i < keys; i++ {
		colW := r.laneWidths[i]
		noteW := noteSpriteWidth(colW)
		if src := r.Skin.NoteSprite(i); src != nil {
			h := scaledHeight(src, noteW)
			r.noteSprites[i] = scaleNRGBA(src, noteW, h)
		}
		if src := r.Skin.LongHeadSprite(i); src != nil {
			h := scaledHeight(src, noteW)
			r.headSprites[i] = scaleNRGBA(src, noteW, h)
		}
		if src := r.Skin.LongBodySprite(i); src != nil {
			r.bodySprites[i] = src
		}
		if src := r.Skin.LongTailSprite(i); src != nil {
			h := scaledHeight(src, noteW)
			r.tailSprites[i] = scaleNRGBA(src, noteW, h)
		}
		keyW := int(math.Round(float64(colW) * 0.96))
		if keyW < 8 {
			keyW = 8
		}
		if src := r.Skin.KeySprite(i, false); src != nil {
			r.keySprites[i] = scaleNRGBA(src, keyW, scaledHeight(src, keyW))
		}
		if src := r.Skin.KeySprite(i, true); src != nil {
			r.keyDownSprites[i] = scaleNRGBA(src, keyW, scaledHeight(src, keyW))
		}
	}

	r.hitSprites = make(map[judge.Grade]*image.NRGBA)
	for _, g := range []judge.Grade{judge.MAX, judge.Hit300, judge.Hit200, judge.Hit100, judge.Hit50, judge.Miss} {
		if src := r.Skin.HitSprite(string(g)); src != nil {
			r.hitSprites[g] = scaleJudgmentSprite(src, r.scale)
		}
	}

	if src := r.Skin.Image(r.Skin.Mania.StageLight); usableSkinSprite(src) {
		width := maxInt(1, int(math.Round(float64(src.Bounds().Dx())*r.scale)))
		height := maxInt(1, int(math.Round(float64(src.Bounds().Dy())*r.scale)))
		r.stageLight = scaleNRGBA(src, width, height)
	}
	avgW := r.stageWidth / keys
	lightWidth := int(math.Round(float64(avgW) * 1.4))
	r.lightingN = r.prepareLightingFrames(r.Skin.Mania.LightingN, lightWidth)
	r.lightingL = r.prepareLightingFrames(r.Skin.Mania.LightingL, lightWidth)
	r.scoreGlyphs = prepareNumberGlyphs(r.Skin, r.Skin.ScorePrefix, int(math.Max(10, 20*r.scale)))
	r.resultGlyphs = prepareNumberGlyphs(r.Skin, r.Skin.ScorePrefix, int(math.Max(16, 34*r.scale)))
	r.comboGlyphs = prepareNumberGlyphs(r.Skin, r.Skin.ComboPrefix, int(math.Max(10, 28*r.scale)))
}

func (r *Renderer) prepareLightingFrames(prefix string, targetWidth int) []*image.NRGBA {
	sources := r.Skin.AnimationFrames(prefix)
	if len(sources) == 0 {
		if src := r.Skin.Image(prefix); usableSkinSprite(src) {
			sources = append(sources, src)
		}
	}
	frames := make([]*image.NRGBA, 0, len(sources))
	for _, src := range sources {
		if !usableSkinSprite(src) {
			continue
		}
		frames = append(frames, scaleNRGBA(src, targetWidth, scaledHeight(src, targetWidth)))
	}
	return frames
}

func scaleJudgmentSprite(src *image.NRGBA, scale float64) *image.NRGBA {
	targetHeight := maxInt(12, int(math.Round(24*scale)))
	sourceWidth := maxInt(1, src.Bounds().Dx())
	sourceHeight := maxInt(1, src.Bounds().Dy())
	targetWidth := maxInt(1, int(math.Round(float64(sourceWidth)*float64(targetHeight)/float64(sourceHeight))))
	return scaleNRGBA(src, targetWidth, targetHeight)
}

func usableSkinSprite(src *image.NRGBA) bool {
	return src != nil && src.Bounds().Dx() > 4 && src.Bounds().Dy() > 4
}

func noteSpriteWidth(columnWidth int) int {
	width := int(math.Round(float64(columnWidth) * 0.92))
	if width < 8 {
		return 8
	}
	return width
}

func (r *Renderer) prepareFonts() {
	ttf, err := opentype.Parse(gobold.TTF)
	if err != nil {
		return
	}
	makeFace := func(size float64) font.Face {
		face, _ := opentype.NewFace(ttf, &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingFull})
		return face
	}
	r.comboFace = makeFace(math.Max(14, 28*r.scale))
	r.accFace = makeFace(math.Max(11, 15*r.scale))
	r.scoreFace = makeFace(math.Max(12, 20*r.scale))
	r.judgeFace = makeFace(math.Max(13, 21*r.scale))
	r.metaFace = makeFace(math.Max(10, 12*r.scale))
}

func prepareNumberGlyphs(s *skin.Skin, prefix string, targetHeight int) map[rune]*image.NRGBA {
	glyphs := make(map[rune]*image.NRGBA)
	if s == nil || prefix == "" {
		return glyphs
	}
	reference := s.Image(prefix + "-0")
	if reference == nil || reference.Bounds().Dy() == 0 {
		return glyphs
	}
	factor := float64(targetHeight) / float64(reference.Bounds().Dy())
	names := map[rune]string{
		'0': "0", '1': "1", '2': "2", '3': "3", '4': "4",
		'5': "5", '6': "6", '7': "7", '8': "8", '9': "9",
		'.': "dot", ',': "comma", '%': "percent",
	}
	for char, suffix := range names {
		src := s.Image(prefix + "-" + suffix)
		if src == nil {
			continue
		}
		width := int(math.Round(float64(src.Bounds().Dx()) * factor))
		height := int(math.Round(float64(src.Bounds().Dy()) * factor))
		if width < 1 {
			width = 1
		}
		if height < 1 {
			height = 1
		}
		glyphs[char] = scaleNRGBA(src, width, height)
	}
	return glyphs
}

func (r *Renderer) prepareNotes() {
	r.notes = make([]noteVisual, 0, len(r.Stats.Results))
	for idx, res := range r.Stats.Results {
		r.notes = append(r.notes, noteVisual{
			object: beatmap.HitObject{
				Time: res.Time, End: res.EndTime, Lane: res.Lane, Long: res.Long, Sound: res.Sound, Sample: res.Sample,
			},
			startPosition: r.Timeline.Position(res.Time),
			endPosition:   r.Timeline.Position(res.EndTime),
			resultIndex:   idx,
		})
	}
}

func (r *Renderer) drawColumnLight(img *image.RGBA, lane int) {
	if r.stageLight == nil {
		return
	}
	x := r.laneLefts[lane] + (r.laneWidths[lane]-r.stageLight.Bounds().Dx())/2
	y := int(math.Round(r.Skin.Mania.LightPosition*r.scale)) - r.stageLight.Bounds().Dy()/2
	blitNRGBA(img, r.stageLight, x, y, 1)
}

func (r *Renderer) drawHitEffects(img *image.RGBA, timeMS float64) {
	for _, res := range r.Stats.Results {
		if !res.Long || res.Grade == judge.Miss || timeMS < res.HitTime || timeMS > res.ReleaseTime {
			continue
		}
		lighting := r.animationFrame(r.lightingL, timeMS-res.HitTime, true)
		if lighting == nil {
			continue
		}
		cx := r.laneLefts[res.Lane] + r.laneWidths[res.Lane]/2
		blitNRGBA(img, lighting, cx-lighting.Bounds().Dx()/2, r.receptorY-lighting.Bounds().Dy()/2, 1)
	}

	if len(r.lightingN) == 0 {
		return
	}
	burstDuration := math.Max(220, float64(len(r.lightingN))*1000/r.animationFrameRate())
	start := sort.Search(len(r.Stats.Results), func(i int) bool {
		return r.Stats.Results[i].Time >= timeMS-burstDuration-200
	})
	for i := start; i < len(r.Stats.Results); i++ {
		res := r.Stats.Results[i]
		if res.Time > timeMS+200 {
			break
		}
		if res.Grade == judge.Miss {
			continue
		}
		age := timeMS - res.HitTime
		if age < 0 || age > burstDuration {
			continue
		}
		lighting := r.animationFrame(r.lightingN, age, false)
		if lighting == nil && len(r.lightingN) == 1 && age <= 220 {
			lighting = r.lightingN[0]
		}
		if lighting == nil {
			continue
		}
		cx := r.laneLefts[res.Lane] + r.laneWidths[res.Lane]/2
		blitNRGBA(img, lighting, cx-lighting.Bounds().Dx()/2, r.receptorY-lighting.Bounds().Dy()/2, 1)
	}
}

func (r *Renderer) animationFrame(frames []*image.NRGBA, elapsedMS float64, loop bool) *image.NRGBA {
	if len(frames) == 0 {
		return nil
	}
	frame := int(math.Max(0, elapsedMS) * r.animationFrameRate() / 1000)
	if loop {
		frame %= len(frames)
	} else if frame >= len(frames) {
		return nil
	}
	return frames[frame]
}

func (r *Renderer) animationFrameRate() float64 {
	if r.Skin == nil || r.Skin.AnimationFrameRate <= 0 {
		return 30
	}
	return float64(r.Skin.AnimationFrameRate)
}

func (r *Renderer) drawHUD(img *image.RGBA, timeMS float64) {
	duration := r.DurationMS
	if duration <= 0 {
		duration = maxMapTime(r.Map)
	}
	progress := math.Min(1, math.Max(0, timeMS/duration))

	// Top-center song progress pie clock + Accuracy (matching the reference screenshot).
	acc, combo, score, hp := statsAt(r.Stats.Results, timeMS)
	r.drawStatsPanel(img, timeMS, acc)
	r.drawMapInfoPanel(img, timeMS)
	stageCenterX := (r.stageLeft + r.stageRight) / 2
	topY := int(28 * r.scale)
	accY := topY
	if r.Skin.Mania.HasAccuracyPosition {
		accY = int(math.Round(r.Skin.Mania.AccuracyPosition * r.scale))
	}
	accText := fmt.Sprintf("%05.2f%%", acc)
	clockR := int(math.Max(7, 10*r.scale))
	accWidth, hasAccSprites := r.skinTextWidth(accText, r.scoreGlyphs, r.Skin.ScoreOverlap)
	if !hasAccSprites {
		accWidth = measureText(r.accFace, accText)
	}
	groupW := clockR*2 + int(10*r.scale) + accWidth
	clockX := stageCenterX - groupW/2 + clockR
	drawPieClock(img, clockX, accY, clockR, progress)
	accX := clockX + clockR + int(10*r.scale)
	if !r.drawSkinText(img, accText, r.scoreGlyphs, accX, accY, r.Skin.ScoreOverlap, textAlignLeft) {
		drawCenteredShadowText(img, accX, accY, accText, r.accFace, color.White)
	}

	// Classic 8-digit score at top right.
	scoreText := fmt.Sprintf("%08d", score)
	scoreW, hasScoreSprites := r.skinTextWidth(scoreText, r.scoreGlyphs, r.Skin.ScoreOverlap)
	if !hasScoreSprites {
		scoreW = measureText(r.scoreFace, scoreText)
	}
	scoreX := r.Width - int(16*r.scale)
	if !r.drawSkinText(img, scoreText, r.scoreGlyphs, scoreX, topY, r.Skin.ScoreOverlap, textAlignRight) {
		drawCenteredShadowText(img, scoreX-scoreW, topY, scoreText, r.scoreFace, color.White)
	}

	// Keep the health meter in the left gutter so the side panel remains unobstructed.
	healthBar := r.healthBarRect()
	if !healthBar.Empty() {
		fillAlpha(img, healthBar.Min.X, healthBar.Min.Y, healthBar.Max.X, healthBar.Max.Y, color.NRGBA{R: 35, G: 55, B: 70, A: 140})
		fillH := int(float64(healthBar.Dy()) * hp)
		fillAlpha(img, healthBar.Min.X, healthBar.Max.Y-fillH, healthBar.Max.X, healthBar.Max.Y, color.NRGBA{R: 72, G: 230, B: 255, A: 235})
	}

	// Combo inside the upper-middle playfield.
	if combo > 0 {
		comboY := int(math.Round(r.Skin.Mania.ComboPosition * r.scale))
		comboText := fmt.Sprintf("%d", combo)
		comboW, hasComboSprites := r.skinTextWidth(comboText, r.comboGlyphs, r.Skin.ComboOverlap)
		if !hasComboSprites {
			comboW = measureText(r.comboFace, comboText)
		}
		if !r.drawSkinText(img, comboText, r.comboGlyphs, stageCenterX, comboY, r.Skin.ComboOverlap, textAlignCenter) {
			drawCenteredShadowText(img, stageCenterX-comboW/2, comboY, comboText, r.comboFace, color.White)
		}
	}

	// Latest judgment burst below combo.
	if latestIdx, ok := latestResultIndex(r.Stats.Results, timeMS); ok {
		res := r.Stats.Results[latestIdx]
		age := timeMS - res.HitTime
		if age >= 0 && age < 340 {
			alpha := 1.0
			if age > 200 {
				alpha = 1.0 - (age-200)/140.0
			}
			judgeY := int(math.Round(r.Skin.Mania.ScorePosition * r.scale))
			if sprite := r.hitSprites[res.Grade]; sprite != nil {
				blitNRGBA(img, sprite, stageCenterX-sprite.Bounds().Dx()/2, judgeY-sprite.Bounds().Dy()/2, alpha)
			} else if res.Grade != judge.MAX {
				label, col := judgmentVisual(res.Grade, alpha)
				lw := measureText(r.judgeFace, label)
				drawCenteredShadowText(img, stageCenterX-lw/2, judgeY, label, r.judgeFace, col)
			}
		}
	}

	// Hit Error Bar below the receptors.
	r.drawHitErrorBar(img, stageCenterX, timeMS)

}

func (r *Renderer) drawScoreResult(img *image.RGBA) {
	counts := gradeSnapshot{}
	if len(r.gradeStats) > 0 {
		counts = r.gradeStats[len(r.gradeStats)-1]
	}
	rank := resultRank(r.Stats.Accuracy, counts)
	rankSprite := r.Skin.Image("ranking-" + rank)
	fullBackground := isFullResultBackground(rankSprite)

	fillAlpha(img, 0, 0, r.Width, r.Height, color.NRGBA{R: 3, G: 6, B: 12, A: 215})
	if fullBackground {
		drawResultBackground(img, rankSprite)
	}

	if !fullBackground {
		iconHeight := int(float64(r.Height) * 0.54)
		if usableSkinSprite(rankSprite) {
			drawFittedResultSprite(img, rankSprite, int(float64(r.Width)*0.24), r.Height/2, int(float64(r.Width)*0.40), iconHeight)
		} else {
			label := rank
			labelWidth := measureText(r.scoreFace, label)
			drawCenteredShadowText(img, int(float64(r.Width)*0.24)-labelWidth/2, r.Height/2, label, r.scoreFace, color.NRGBA{R: 78, G: 221, B: 255, A: 255})
		}
	}

	panelSprite := r.Skin.Image("ranking-panel")
	panelAspect := 622.0 / 505.0
	if usableSkinSprite(panelSprite) {
		panelAspect = float64(panelSprite.Bounds().Dx()) / float64(panelSprite.Bounds().Dy())
	}
	panelWidth := minInt(int(float64(r.Width)*0.40), int(float64(r.Height)*0.88*panelAspect))
	panelHeight := int(math.Round(float64(panelWidth) / panelAspect))
	panelX := r.Width - panelWidth - int(float64(r.Width)*0.045)
	panelY := (r.Height - panelHeight) / 2
	if usableSkinSprite(panelSprite) {
		panel := scaleNRGBA(panelSprite, panelWidth, panelHeight)
		blitNRGBA(img, panel, panelX, panelY, 1)
	} else {
		drawHUDPanel(img, panelX, panelY, panelWidth, panelHeight)
	}

	centerX := panelX + panelWidth/2
	titleY := panelY + int(float64(panelHeight)*0.105)
	if !drawFittedResultSprite(img, r.Skin.Image("ranking-title"), centerX, titleY, int(float64(panelWidth)*0.58), int(float64(panelHeight)*0.10)) {
		drawCenteredShadowText(img, centerX-measureText(r.judgeFace, "SCORE RESULT")/2, titleY, "SCORE RESULT", r.judgeFace, color.White)
	}
	if counts.miss == 0 && len(r.Stats.Results) > 0 {
		drawFittedResultSprite(img, r.Skin.Image("ranking-perfect"), centerX, panelY+int(float64(panelHeight)*0.205), int(float64(panelWidth)*0.38), int(float64(panelHeight)*0.085))
	}

	score := 0
	if len(r.Stats.Results) > 0 {
		score = r.Stats.Results[len(r.Stats.Results)-1].ScoreAfter
	}
	r.drawResultLabel(img, centerX, panelY+int(float64(panelHeight)*0.285), "SCORE", color.NRGBA{R: 220, G: 231, B: 243, A: 255})
	r.drawResultValue(img, centerX, panelY+int(float64(panelHeight)*0.355), fmt.Sprintf("%08d", score))

	accuracyLabelY := panelY + int(float64(panelHeight)*0.455)
	if !drawFittedResultSprite(img, r.Skin.Image("ranking-accuracy"), centerX, accuracyLabelY, int(float64(panelWidth)*0.36), int(float64(panelHeight)*0.06)) {
		r.drawResultLabel(img, centerX, accuracyLabelY, "ACCURACY", color.NRGBA{R: 220, G: 231, B: 243, A: 255})
	}
	r.drawResultValue(img, centerX, panelY+int(float64(panelHeight)*0.525), fmt.Sprintf("%.2f%%", r.Stats.Accuracy))

	comboY := panelY + int(float64(panelHeight)*0.645)
	if !drawFittedResultSprite(img, r.Skin.Image("ranking-maxcombo"), centerX, comboY, int(float64(panelWidth)*0.34), int(float64(panelHeight)*0.065)) {
		r.drawResultLabel(img, centerX, comboY, "MAX COMBO", color.NRGBA{R: 220, G: 231, B: 243, A: 255})
	}
	r.drawResultValue(img, centerX, panelY+int(float64(panelHeight)*0.705), fmt.Sprintf("%d", r.Stats.MaxCombo))

	labels := []struct {
		name  string
		count int
		grade judge.Grade
	}{
		{"320", counts.max, judge.MAX}, {"300", counts.perfect, judge.Hit300},
		{"200", counts.great, judge.Hit200}, {"100", counts.good, judge.Hit100},
		{"50", counts.bad, judge.Hit50}, {"Miss", counts.miss, judge.Miss},
	}
	gridTop := panelY + int(float64(panelHeight)*0.805)
	rowHeight := int(float64(panelHeight) * 0.075)
	columnWidth := panelWidth / 2
	for i, entry := range labels {
		column := i / 3
		row := i % 3
		x := panelX + int(float64(panelWidth)*0.10) + column*columnWidth
		y := gridTop + row*rowHeight
		label, tint := judgmentVisual(entry.grade, 1)
		if entry.name != "Miss" {
			label = entry.name
		}
		drawCenteredShadowText(img, x, y, fmt.Sprintf("%s  %d", label, entry.count), r.metaFace, tint)
	}
}

func (r *Renderer) drawResultLabel(img *image.RGBA, centerX, centerY int, text string, tint color.NRGBA) {
	drawCenteredShadowText(img, centerX-measureText(r.metaFace, text)/2, centerY, text, r.metaFace, tint)
}

func (r *Renderer) drawResultValue(img *image.RGBA, centerX, centerY int, text string) {
	if r.drawSkinText(img, text, r.resultGlyphs, centerX, centerY, r.Skin.ScoreOverlap, textAlignCenter) {
		return
	}
	width := measureText(r.scoreFace, text)
	drawCenteredShadowText(img, centerX-width/2, centerY, text, r.scoreFace, color.White)
}

func resultRank(accuracy float64, counts gradeSnapshot) string {
	switch {
	case accuracy >= 100 && counts.miss == 0:
		return "X"
	case accuracy >= 95 && counts.miss == 0:
		return "S"
	case accuracy >= 90:
		return "A"
	case accuracy >= 80:
		return "B"
	case accuracy >= 70:
		return "C"
	default:
		return "D"
	}
}

func isFullResultBackground(sprite *image.NRGBA) bool {
	if !usableSkinSprite(sprite) || sprite.Bounds().Dx() < 1000 || sprite.Bounds().Dy() < 600 {
		return false
	}
	imageAspect := float64(sprite.Bounds().Dx()) / float64(sprite.Bounds().Dy())
	return imageAspect > 1.45
}

func drawResultBackground(dst *image.RGBA, src *image.NRGBA) {
	screen := dst.Bounds()
	imageAspect := float64(src.Bounds().Dx()) / float64(src.Bounds().Dy())
	screenAspect := float64(screen.Dx()) / float64(screen.Dy())
	width, height := screen.Dx(), screen.Dy()
	if imageAspect > screenAspect {
		width = int(math.Round(float64(height) * imageAspect))
	} else {
		height = int(math.Round(float64(width) / imageAspect))
	}
	scaled := scaleNRGBA(src, width, height)
	blitNRGBA(dst, scaled, (screen.Dx()-width)/2, (screen.Dy()-height)/2, 1)
}

func drawFittedResultSprite(dst *image.RGBA, src *image.NRGBA, centerX, centerY, maxWidth, maxHeight int) bool {
	if !usableSkinSprite(src) || maxWidth <= 0 || maxHeight <= 0 {
		return false
	}
	bounds := src.Bounds()
	factor := math.Min(float64(maxWidth)/float64(bounds.Dx()), float64(maxHeight)/float64(bounds.Dy()))
	width := maxInt(1, int(math.Round(float64(bounds.Dx())*factor)))
	height := maxInt(1, int(math.Round(float64(bounds.Dy())*factor)))
	blitNRGBA(dst, scaleNRGBA(src, width, height), centerX-width/2, centerY-height/2, 1)
	return true
}

func (r *Renderer) drawStatsPanel(img *image.RGBA, timeMS, accuracy float64) {
	index := sort.Search(len(r.Stats.Results), func(i int) bool { return r.Stats.Results[i].Time > timeMS })
	if index >= len(r.gradeStats) {
		index = len(r.gradeStats) - 1
	}
	counts := r.gradeStats[index]
	scale := r.scale
	panel := r.statsPanelRect()
	x, y := panel.Min.X, panel.Min.Y
	lineHeight := int(15 * scale)
	drawHUDPanel(img, x, y, panel.Dx(), panel.Dy())
	cyan := color.NRGBA{R: 75, G: 220, B: 255, A: 255}
	white := color.NRGBA{R: 238, G: 242, B: 250, A: 255}
	drawShadowText(img, x+int(7*scale), y+int(15*scale), fmt.Sprintf("%05.2f%%", accuracy), r.metaFace, cyan)
	ur := 0.0
	if counts.hitCount > 0 {
		mean := counts.hitErrorSum / float64(counts.hitCount)
		variance := counts.hitErrorSquareSum/float64(counts.hitCount) - mean*mean
		ur = math.Sqrt(math.Max(0, variance)) * 10
	}
	drawShadowText(img, x+int(7*scale), y+int(15*scale)+lineHeight, fmt.Sprintf("UR: %.2f", ur), r.metaFace, white)
	rest := counts.perfect + counts.great + counts.good + counts.bad + counts.miss
	ratio := float64(counts.max)
	if rest > 0 {
		ratio /= float64(rest)
	}
	drawShadowText(img, x+int(7*scale), y+int(15*scale)+2*lineHeight, fmt.Sprintf("Ratio: %.1f:1", ratio), r.metaFace, white)
	labels := []struct {
		name  string
		count int
		grade judge.Grade
	}{
		{"320", counts.max, judge.MAX}, {"300", counts.perfect, judge.Hit300},
		{"200", counts.great, judge.Hit200}, {"100", counts.good, judge.Hit100},
		{"50", counts.bad, judge.Hit50}, {"Miss", counts.miss, judge.Miss},
	}
	for i, label := range labels {
		_, tint := judgmentVisual(label.grade, 1)
		drawShadowText(img, x+int(7*scale), y+int(15*scale)+(i+3)*lineHeight, fmt.Sprintf("%s: %d", label.name, label.count), r.metaFace, tint)
	}
}

func (r *Renderer) drawMapInfoPanel(img *image.RGBA, timeMS float64) {
	bpm := currentBPM(r.Map.TimingPoints, timeMS)
	text := mapInfoPanelText(r.Map, bpm)
	panel := r.mapInfoPanelRect()
	drawHUDPanel(img, panel.Min.X, panel.Min.Y, panel.Dx(), panel.Dy())
	drawShadowText(img, panel.Min.X+int(7*r.scale), panel.Min.Y+int(16*r.scale), text, r.metaFace, color.NRGBA{R: 220, G: 235, B: 250, A: 255})
}

func mapInfoPanelText(m *beatmap.Map, bpm float64) string {
	starRating := "--"
	if m.StarRating > 0 && !math.IsNaN(m.StarRating) && !math.IsInf(m.StarRating, 0) {
		starRating = fmt.Sprintf("%.2f", m.StarRating)
	}
	return fmt.Sprintf("BPM %.0f   OD %.1f   HP %.1f   SR %s", bpm, m.OD, m.HP, starRating)
}

func (r *Renderer) mapInfoPanelRect() image.Rectangle {
	gap := int(math.Max(8, math.Round(8*r.scale)))
	rightX := r.stageRight + gap
	rightSpace := r.Width - rightX - gap
	leftSpace := r.stageLeft - 2*gap
	x, available := rightX, rightSpace
	if rightSpace < int(190*r.scale) && leftSpace > rightSpace {
		x, available = gap, leftSpace
	}
	width := minInt(int(math.Round(280*r.scale)), available)
	height := int(math.Round(24 * r.scale))
	if width < 1 {
		width = 1
	}
	if height < 1 {
		height = 1
	}
	anchor := math.Max(r.Skin.Mania.ScorePosition, r.Skin.Mania.ComboPosition)
	y := int(math.Round(anchor*r.scale)) + int(math.Round(34*r.scale))
	if y+height > r.Height-gap {
		y = r.Height - height - gap
	}
	if y < gap {
		y = gap
	}
	return image.Rect(x, y, x+width, y+height)
}

func (r *Renderer) statsPanelRect() image.Rectangle {
	scale := r.scale
	return image.Rect(
		int(12*scale),
		int(56*scale),
		int(12*scale)+int(144*scale),
		int(56*scale)+int(150*scale),
	)
}

func (r *Renderer) healthBarRect() image.Rectangle {
	barW := int(math.Max(3, 3.5*r.scale))
	top := int(48 * r.scale)
	bottom := r.Height - int(24*r.scale)
	if bottom <= top {
		return image.Rectangle{}
	}

	x := r.stageLeft - int(16*r.scale) - barW
	stats := r.statsPanelRect()
	if x < 0 || (x < stats.Max.X && x+barW > stats.Min.X) {
		x = r.stageRight + int(16*r.scale)
	}
	if x+barW > r.Width {
		return image.Rectangle{}
	}
	return image.Rect(x, top, x+barW, bottom)
}

func drawHUDPanel(img *image.RGBA, x, y, width, height int) {
	fillAlpha(img, x, y, x+width, y+height, color.NRGBA{R: 8, G: 12, B: 22, A: 188})
	fillAlpha(img, x, y, x+width, y+1, color.NRGBA{R: 60, G: 213, B: 255, A: 220})
	fillAlpha(img, x, y+height-1, x+width, y+height, color.NRGBA{R: 60, G: 213, B: 255, A: 180})
	fillAlpha(img, x, y, x+1, y+height, color.NRGBA{R: 60, G: 213, B: 255, A: 180})
	fillAlpha(img, x+width-1, y, x+width, y+height, color.NRGBA{R: 60, G: 213, B: 255, A: 180})
}

func currentBPM(points []beatmap.TimingPoint, timeMS float64) float64 {
	index := sort.Search(len(points), func(i int) bool { return points[i].Time > timeMS })
	for i := index - 1; i >= 0; i-- {
		if points[i].Uninherited && points[i].BeatLength > 0 {
			return 60000 / points[i].BeatLength
		}
	}
	for i := index; i < len(points); i++ {
		if points[i].Uninherited && points[i].BeatLength > 0 {
			return 60000 / points[i].BeatLength
		}
	}
	return 0
}

func (r *Renderer) drawHitErrorBar(img *image.RGBA, cx int, timeMS float64) {
	barY := r.receptorY + int(28*r.scale)
	if barY+int(8*r.scale) >= r.Height {
		barY = r.Height - int(10*r.scale)
	}
	halfW := int(math.Min(float64(r.stageWidth)*0.45, 115*r.scale))
	if halfW < 30 {
		return
	}
	barH := int(math.Max(3, 3.5*r.scale))
	tickH := int(math.Max(7, 8.5*r.scale))

	// Dark backing.
	fillAlpha(img, cx-halfW-2, barY-tickH/2, cx+halfW+2, barY+tickH/2, color.NRGBA{R: 0, G: 0, B: 0, A: 140})

	maxWin := r.windows.W50
	if maxWin <= 0 {
		maxWin = 120
	}
	toX := func(ms float64) int {
		ratio := ms / maxWin
		if ratio < -1 {
			ratio = -1
		} else if ratio > 1 {
			ratio = 1
		}
		return cx + int(math.Round(ratio*float64(halfW)))
	}

	// Each nested timing window uses its matching skin judgment color.
	for _, window := range []struct {
		width float64
		grade judge.Grade
	}{
		{r.windows.W50, judge.Hit50},
		{r.windows.W100, judge.Hit100},
		{r.windows.W200, judge.Hit200},
		{r.windows.W300, judge.Hit300},
		{r.windows.W320, judge.MAX},
	} {
		halfWindow := int(math.Round(window.width / maxWin * float64(halfW)))
		tint := judgmentColor(window.grade)
		tint.A = 190
		fillAlpha(img, cx-halfWindow, barY-barH/2, cx+halfWindow, barY+barH/2, tint)
	}

	// Recent hit error ticks.
	const errorLife = 2200.0
	start := sort.Search(len(r.Stats.Results), func(i int) bool { return r.Stats.Results[i].Time >= timeMS-errorLife-250 })
	for i := start; i < len(r.Stats.Results); i++ {
		res := r.Stats.Results[i]
		if res.Time > timeMS+250 {
			break
		}
		age := timeMS - res.HitTime
		if res.Grade == judge.Miss || age < 0 || age > errorLife {
			continue
		}
		fade := 1.0 - age/errorLife
		tx := toX(res.HitError)
		col := errorTickColor(res.Grade, fade)
		fillAlpha(img, tx-1, barY-tickH/2, tx+1, barY+tickH/2, col)
	}

	// Center line.
	fillAlpha(img, cx-1, barY-tickH/2-1, cx+1, barY+tickH/2+1, color.NRGBA{R: 255, G: 255, B: 255, A: 235})
}

func drawPieClock(img *image.RGBA, cx, cy, radius int, progress float64) {
	angleLimit := progress * 2 * math.Pi
	for dy := -radius - 1; dy <= radius+1; dy++ {
		y := cy + dy
		if y < 0 || y >= img.Bounds().Dy() {
			continue
		}
		for dx := -radius - 1; dx <= radius+1; dx++ {
			x := cx + dx
			if x < 0 || x >= img.Bounds().Dx() {
				continue
			}
			dist := math.Hypot(float64(dx), float64(dy))
			outerCov := clamp01(float64(radius) + 0.75 - dist)
			if outerCov <= 0 {
				continue
			}
			inner := float64(radius) - 1.5
			ringCov := clamp01(outerCov - clamp01(inner+0.75-dist))

			ang := math.Atan2(float64(dx), -float64(dy))
			if ang < 0 {
				ang += 2 * math.Pi
			}
			pieCov := 0.0
			if dist <= inner+0.5 && ang <= angleLimit {
				pieCov = clamp01(inner - dist + 0.5)
			}
			alpha := math.Max(ringCov*0.9, pieCov*0.75)
			if alpha <= 0 {
				continue
			}
			blendPixel(img, x, y, 235, 240, 248, alpha)
		}
	}
}

func drawCoverDim(dst *image.RGBA, src image.Image, dimFactor float64) {
	sb := src.Bounds()
	db := dst.Bounds()
	if sb.Empty() || db.Empty() {
		return
	}
	srcAspect := float64(sb.Dx()) / float64(sb.Dy())
	dstAspect := float64(db.Dx()) / float64(db.Dy())
	crop := sb
	if srcAspect > dstAspect {
		newW := int(math.Round(float64(sb.Dy()) * dstAspect))
		crop.Min.X = sb.Min.X + (sb.Dx()-newW)/2
		crop.Max.X = crop.Min.X + newW
	} else {
		newH := int(math.Round(float64(sb.Dx()) / dstAspect))
		crop.Min.Y = sb.Min.Y + (sb.Dy()-newH)/2
		crop.Max.Y = crop.Min.Y + newH
	}
	xdraw.BiLinear.Scale(dst, db, src, crop, xdraw.Src, nil)
	for i := 0; i < len(dst.Pix); i += 4 {
		dst.Pix[i] = uint8(float64(dst.Pix[i]) * dimFactor)
		dst.Pix[i+1] = uint8(float64(dst.Pix[i+1]) * dimFactor)
		dst.Pix[i+2] = uint8(float64(dst.Pix[i+2]) * dimFactor)
		dst.Pix[i+3] = 255
	}
}

func scaleNRGBA(src image.Image, w, h int) *image.NRGBA {
	if w < 1 {
		w = 1
	}
	if h < 1 {
		h = 1
	}
	dst := image.NewNRGBA(image.Rect(0, 0, w, h))
	xdraw.BiLinear.Scale(dst, dst.Bounds(), src, src.Bounds(), xdraw.Src, nil)
	return dst
}

func scaledHeight(src image.Image, targetW int) int {
	b := src.Bounds()
	if b.Dx() <= 0 {
		return targetW
	}
	h := int(math.Round(float64(b.Dy()) * float64(targetW) / float64(b.Dx())))
	if h < 1 {
		h = 1
	}
	return h
}

func blitNRGBA(dst *image.RGBA, src *image.NRGBA, dstX, dstY int, opacity float64) {
	if src == nil || opacity <= 0 {
		return
	}
	sw, sh := src.Bounds().Dx(), src.Bounds().Dy()
	x0, y0 := maxInt(0, dstX), maxInt(0, dstY)
	x1, y1 := minInt(dst.Bounds().Dx(), dstX+sw), minInt(dst.Bounds().Dy(), dstY+sh)
	if x0 >= x1 || y0 >= y1 {
		return
	}
	op := uint32(math.Round(opacity * 256))
	for y := y0; y < y1; y++ {
		sy := y - dstY
		srcRow := sy * src.Stride
		dstRow := y * dst.Stride
		for x := x0; x < x1; x++ {
			sx := x - dstX
			si := srcRow + sx*4
			a := (uint32(src.Pix[si+3]) * op) >> 8
			if a == 0 {
				continue
			}
			di := dstRow + x*4
			if a >= 255 {
				dst.Pix[di] = src.Pix[si]
				dst.Pix[di+1] = src.Pix[si+1]
				dst.Pix[di+2] = src.Pix[si+2]
				dst.Pix[di+3] = 255
				continue
			}
			inv := 255 - a
			dst.Pix[di] = uint8((uint32(src.Pix[si])*a + uint32(dst.Pix[di])*inv) / 255)
			dst.Pix[di+1] = uint8((uint32(src.Pix[si+1])*a + uint32(dst.Pix[di+1])*inv) / 255)
			dst.Pix[di+2] = uint8((uint32(src.Pix[si+2])*a + uint32(dst.Pix[di+2])*inv) / 255)
			dst.Pix[di+3] = 255
		}
	}
}

func blitLongBody(dst *image.RGBA, src *image.NRGBA, dstX, dstY, targetW, targetH int, opacity float64) {
	if src == nil || targetW <= 0 || targetH <= 0 || opacity <= 0 {
		return
	}
	bounds := src.Bounds()
	sw, sh := bounds.Dx(), bounds.Dy()
	if sw == 0 || sh == 0 {
		return
	}
	x0, y0 := maxInt(0, dstX), maxInt(0, dstY)
	x1, y1 := minInt(dst.Bounds().Dx(), dstX+targetW), minInt(dst.Bounds().Dy(), dstY+targetH)
	if x0 >= x1 || y0 >= y1 {
		return
	}
	op := uint32(math.Round(opacity * 256))
	for y := y0; y < y1; y++ {
		sy := (float64(y-dstY)+0.5)*float64(sh)/float64(targetH) - 0.5
		sy0 := int(math.Floor(sy))
		fy := sy - float64(sy0)
		if sy0 < 0 {
			sy0, fy = 0, 0
		}
		if sy0 >= sh-1 {
			sy0, fy = sh-1, 0
		}
		sy1 := minInt(sh-1, sy0+1)
		srcRow0 := src.PixOffset(bounds.Min.X, bounds.Min.Y+sy0)
		srcRow1 := src.PixOffset(bounds.Min.X, bounds.Min.Y+sy1)
		dstRow := y * dst.Stride
		for x := x0; x < x1; x++ {
			sx := (float64(x-dstX)+0.5)*float64(sw)/float64(targetW) - 0.5
			sx0 := int(math.Floor(sx))
			fx := sx - float64(sx0)
			if sx0 < 0 {
				sx0, fx = 0, 0
			}
			if sx0 >= sw-1 {
				sx0, fx = sw-1, 0
			}
			sx1 := minInt(sw-1, sx0+1)
			i00 := srcRow0 + sx0*4
			i10 := srcRow0 + sx1*4
			i01 := srcRow1 + sx0*4
			i11 := srcRow1 + sx1*4
			topAlpha := float64(src.Pix[i00+3])*(1-fx) + float64(src.Pix[i10+3])*fx
			bottomAlpha := float64(src.Pix[i01+3])*(1-fx) + float64(src.Pix[i11+3])*fx
			a := (uint32(math.Round(topAlpha*(1-fy)+bottomAlpha*fy)) * op) >> 8
			if a == 0 {
				continue
			}
			di := dstRow + x*4
			if a >= 255 {
				for channel := 0; channel < 3; channel++ {
					top := float64(src.Pix[i00+channel])*(1-fx) + float64(src.Pix[i10+channel])*fx
					bottom := float64(src.Pix[i01+channel])*(1-fx) + float64(src.Pix[i11+channel])*fx
					dst.Pix[di+channel] = uint8(math.Round(top*(1-fy) + bottom*fy))
				}
				dst.Pix[di+3] = 255
				continue
			}
			inv := 255 - a
			for channel := 0; channel < 3; channel++ {
				top := float64(src.Pix[i00+channel])*(1-fx) + float64(src.Pix[i10+channel])*fx
				bottom := float64(src.Pix[i01+channel])*(1-fx) + float64(src.Pix[i11+channel])*fx
				value := uint32(math.Round(top*(1-fy) + bottom*fy))
				dst.Pix[di+channel] = uint8((value*a + uint32(dst.Pix[di+channel])*inv) / 255)
			}
			dst.Pix[di+3] = 255
		}
	}
}

func fillAlpha(img *image.RGBA, x0, y0, x1, y1 int, c color.NRGBA) {
	x0, y0 = maxInt(0, x0), maxInt(0, y0)
	x1, y1 = minInt(img.Bounds().Dx(), x1), minInt(img.Bounds().Dy(), y1)
	if x0 >= x1 || y0 >= y1 || c.A == 0 {
		return
	}
	a := uint32(c.A)
	inv := 255 - a
	for y := y0; y < y1; y++ {
		row := y * img.Stride
		for x := x0; x < x1; x++ {
			idx := row + x*4
			if a == 255 {
				img.Pix[idx] = c.R
				img.Pix[idx+1] = c.G
				img.Pix[idx+2] = c.B
				img.Pix[idx+3] = 255
			} else {
				img.Pix[idx] = uint8((uint32(c.R)*a + uint32(img.Pix[idx])*inv) / 255)
				img.Pix[idx+1] = uint8((uint32(c.G)*a + uint32(img.Pix[idx+1])*inv) / 255)
				img.Pix[idx+2] = uint8((uint32(c.B)*a + uint32(img.Pix[idx+2])*inv) / 255)
				img.Pix[idx+3] = 255
			}
		}
	}
}

func blendPixel(img *image.RGBA, x, y int, r, g, b uint8, alpha float64) {
	a := uint32(clampByte(alpha * 255))
	if a == 0 {
		return
	}
	inv := 255 - a
	idx := y*img.Stride + x*4
	img.Pix[idx] = uint8((uint32(r)*a + uint32(img.Pix[idx])*inv) / 255)
	img.Pix[idx+1] = uint8((uint32(g)*a + uint32(img.Pix[idx+1])*inv) / 255)
	img.Pix[idx+2] = uint8((uint32(b)*a + uint32(img.Pix[idx+2])*inv) / 255)
	img.Pix[idx+3] = 255
}

func drawShadowText(img *image.RGBA, x, y int, text string, face font.Face, c color.Color) {
	if face == nil || text == "" {
		return
	}
	_, _, _, a := c.RGBA()
	shadowAlpha := uint8((a >> 8) * 3 / 4)
	shadow := font.Drawer{Dst: img, Src: image.NewUniform(color.NRGBA{A: shadowAlpha}), Face: face, Dot: fixed.P(x+2, y+2)}
	shadow.DrawString(text)
	d := font.Drawer{Dst: img, Src: image.NewUniform(c), Face: face, Dot: fixed.P(x, y)}
	d.DrawString(text)
}

func drawCenteredShadowText(img *image.RGBA, x, centerY int, text string, face font.Face, c color.Color) {
	if face == nil {
		return
	}
	metrics := face.Metrics()
	baseline := centerY + (metrics.Ascent.Round()-metrics.Descent.Round())/2
	drawShadowText(img, x, baseline, text, face, c)
}

type textAlignment uint8

const (
	textAlignLeft textAlignment = iota
	textAlignCenter
	textAlignRight
)

func (r *Renderer) skinTextWidth(text string, glyphs map[rune]*image.NRGBA, overlap int) (int, bool) {
	width := 0
	count := 0
	for _, char := range text {
		glyph := glyphs[char]
		if glyph == nil {
			return 0, false
		}
		if count > 0 {
			width += int(math.Round(float64(overlap) * r.scale))
		}
		width += glyph.Bounds().Dx()
		count++
	}
	if width < 0 {
		width = 0
	}
	return width, true
}

func (r *Renderer) drawSkinText(img *image.RGBA, text string, glyphs map[rune]*image.NRGBA, anchorX, centerY, overlap int, align textAlignment) bool {
	width, ok := r.skinTextWidth(text, glyphs, overlap)
	if !ok {
		return false
	}
	x := anchorX
	switch align {
	case textAlignCenter:
		x -= width / 2
	case textAlignRight:
		x -= width
	}
	spacing := int(math.Round(float64(overlap) * r.scale))
	for _, char := range text {
		glyph := glyphs[char]
		y := centerY - glyph.Bounds().Dy()/2
		blitNRGBA(img, glyph, x, y, 1)
		x += glyph.Bounds().Dx() + spacing
	}
	return true
}

func measureText(face font.Face, text string) int {
	if face == nil {
		return len(text) * 8
	}
	return font.MeasureString(face, text).Round()
}

func judgmentVisual(g judge.Grade, alpha float64) (string, color.NRGBA) {
	tint := judgmentColor(g)
	tint.A = clampByte(alpha * 255)
	switch g {
	case judge.MAX:
		return "320", tint
	case judge.Hit300:
		return "300", tint
	case judge.Hit200:
		return "200", tint
	case judge.Hit100:
		return "100", tint
	case judge.Hit50:
		return "50", tint
	default:
		return "MISS", tint
	}
}

func judgmentColor(g judge.Grade) color.NRGBA {
	switch g {
	case judge.MAX:
		return color.NRGBA{R: 145, G: 225, B: 255, A: 255}
	case judge.Hit300:
		return color.NRGBA{R: 255, G: 147, B: 18, A: 255}
	case judge.Hit200:
		return color.NRGBA{R: 153, G: 220, B: 35, A: 255}
	case judge.Hit100:
		return color.NRGBA{R: 36, G: 130, B: 255, A: 255}
	case judge.Hit50:
		return color.NRGBA{R: 166, G: 42, B: 255, A: 255}
	default:
		return color.NRGBA{R: 240, G: 75, B: 75, A: 255}
	}
}

func errorTickColor(g judge.Grade, alpha float64) color.NRGBA {
	tint := judgmentColor(g)
	tint.A = clampByte(alpha * 220)
	return tint
}

func keyMaskAt(events []replay.Event, time float64) uint32 {
	index := sort.Search(len(events), func(i int) bool { return events[i].Time > time })
	if index == 0 {
		return 0
	}
	return events[index-1].Mask
}

func statsAt(results []judge.Result, time float64) (acc float64, combo, score int, hp float64) {
	end := sort.Search(len(results), func(i int) bool { return results[i].Time > time })
	if end == 0 {
		return 100.0, 0, 0, 1.0
	}
	r := results[end-1]
	acc, combo, score, hp = r.AccuracyAfter, r.ComboAfter, r.ScoreAfter, r.HPAfter
	if end < len(results) {
		next := results[end]
		span := next.Time - r.Time
		if span > 0 {
			progress := math.Max(0, math.Min(1, (time-r.Time)/span))
			acc += (next.AccuracyAfter - acc) * progress
			score = int(math.Round(float64(score) + float64(next.ScoreAfter-score)*progress))
			hp += (next.HPAfter - hp) * progress
		}
	}
	return acc, combo, score, hp
}

func latestResultIndex(results []judge.Result, time float64) (int, bool) {
	bestIdx := -1
	bestHit := -1e18
	start := sort.Search(len(results), func(i int) bool { return results[i].Time >= time-600 })
	for i := start; i < len(results); i++ {
		r := results[i]
		if r.Time > time+220 {
			break
		}
		if r.HitTime <= time && r.HitTime >= bestHit {
			bestHit = r.HitTime
			bestIdx = i
		}
	}
	return bestIdx, bestIdx >= 0
}

func maxMapTime(m *beatmap.Map) float64 {
	end := 1.0
	for _, object := range m.Objects {
		if object.End > end {
			end = object.End
		}
	}
	return end
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func clampByte(v float64) uint8 {
	if v <= 0 {
		return 0
	}
	if v >= 255 {
		return 255
	}
	return uint8(math.Round(v))
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
