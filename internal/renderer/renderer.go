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
	Width, Height int
	Map           *beatmap.Map
	Skin          *skin.Skin
	Timeline      *sv.Timeline
	Stats         judge.Stats
	Replay        []replay.Event
	Speed         float64
	ScrollSpeed   float64
	DurationMS    float64

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
	lightingN      *image.NRGBA
	lightingL      *image.NRGBA
	burstGlow      *image.NRGBA
	scoreGlyphs    map[rune]*image.NRGBA
	comboGlyphs    map[rune]*image.NRGBA
	notes          []noteVisual
	comboFace      font.Face
	accFace        font.Face
	scoreFace      font.Face
	judgeFace      font.Face
	metaFace       font.Face
	windows        judge.TimingWindows
}

type noteVisual struct {
	object        beatmap.HitObject
	startPosition float64
	endPosition   float64
	resultIndex   int
}

func (r *Renderer) Frame(timeMS float64) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, r.Width, r.Height))
	r.FrameInto(timeMS, img)
	return img
}

func (r *Renderer) FrameInto(timeMS float64, img *image.RGBA) {
	r.prepare()
	copy(img.Pix, r.baseBackdrop)

	mask := keyMaskAt(r.Replay, timeMS)
	for lane := 0; lane < r.Map.Keys; lane++ {
		if mask&(uint32(1)<<lane) != 0 {
			r.drawColumnLight(img, lane)
		}
	}

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
			if held && bodyBottom > r.receptorY {
				bodyBottom = r.receptorY
			}
			if body != nil && bodyBottom > bodyTop {
				blitStretchedY(img, body, cx-body.Bounds().Dx()/2, bodyTop, bodyBottom-bodyTop, alpha)
			}
			if tail != nil && endY < float64(r.receptorY)+float64(tail.Bounds().Dy()) {
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

	r.drawHitEffects(img, timeMS)
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
	if leftImg := r.Skin.Image(r.Skin.Mania.StageLeft); leftImg != nil {
		targetW := int(math.Round(float64(leftImg.Bounds().Dx()) * r.scale * 0.6))
		if targetW < borderW {
			targetW = borderW
		}
		scaled := scaleNRGBA(leftImg, targetW, r.Height)
		blitNRGBA(bgImg, scaled, r.stageLeft-targetW, 0, 1.0)
	} else {
		fillAlpha(bgImg, r.stageLeft-borderW, 0, r.stageLeft, r.Height, color.NRGBA{R: 205, G: 212, B: 224, A: 245})
	}
	if rightImg := r.Skin.Image(r.Skin.Mania.StageRight); rightImg != nil {
		targetW := int(math.Round(float64(rightImg.Bounds().Dx()) * r.scale * 0.6))
		if targetW < borderW {
			targetW = borderW
		}
		scaled := scaleNRGBA(rightImg, targetW, r.Height)
		blitNRGBA(bgImg, scaled, r.stageRight, 0, 1.0)
	} else {
		fillAlpha(bgImg, r.stageRight, 0, r.stageRight+borderW, r.Height, color.NRGBA{R: 205, G: 212, B: 224, A: 245})
	}

	if src := r.Skin.Image(r.Skin.Mania.StageHint); src != nil {
		h := scaledHeight(src, r.stageWidth)
		r.stageHint = scaleNRGBA(src, r.stageWidth, h)
		blitNRGBA(bgImg, r.stageHint, r.stageLeft, r.receptorY-r.stageHint.Bounds().Dy()/2, 1)
	}
	if src := r.Skin.Image(r.Skin.Mania.StageBottom); src != nil {
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
		noteW := int(math.Round(float64(colW) * 0.92))
		if noteW < 8 {
			noteW = 8
		}
		if src := r.Skin.NoteSprite(i); src != nil {
			h := scaledHeight(src, noteW)
			r.noteSprites[i] = scaleNRGBA(src, noteW, h)
		}
		if src := r.Skin.LongHeadSprite(i); src != nil {
			h := scaledHeight(src, noteW)
			r.headSprites[i] = scaleNRGBA(src, noteW, h)
		}
		if src := r.Skin.LongBodySprite(i); src != nil {
			r.bodySprites[i] = scaleNRGBA(src, noteW, 16)
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
			targetW := int(math.Min(float64(r.stageWidth)*0.65, 160*r.scale))
			if targetW < 40 {
				targetW = 40
			}
			r.hitSprites[g] = scaleNRGBA(src, targetW, scaledHeight(src, targetW))
		}
	}

	if src := r.Skin.Image(r.Skin.Mania.StageLight); src != nil {
		avgW := r.stageWidth / keys
		r.stageLight = scaleNRGBA(src, avgW, int(210*r.scale))
	}
	if src := r.Skin.Image(r.Skin.Mania.LightingN); src != nil {
		sz := int(math.Round(float64(r.stageWidth/keys) * 2.4))
		r.lightingN = scaleNRGBA(src, sz, scaledHeight(src, sz))
	}
	if src := r.Skin.Image(r.Skin.Mania.LightingL); src != nil {
		sz := int(math.Round(float64(r.stageWidth/keys) * 2.4))
		r.lightingL = scaleNRGBA(src, sz, scaledHeight(src, sz))
	}
	r.scoreGlyphs = prepareNumberGlyphs(r.Skin, r.Skin.ScorePrefix, int(math.Max(10, 20*r.scale)))
	r.comboGlyphs = prepareNumberGlyphs(r.Skin, r.Skin.ComboPrefix, int(math.Max(10, 28*r.scale)))
	r.burstGlow = generateBurstGlow(int(math.Round(float64(r.stageWidth/keys) * 2.8)))
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
	x0 := r.laneLefts[lane]
	w := r.laneWidths[lane]
	lightY := int(math.Round(r.Skin.Mania.LightPosition * r.scale))
	if r.stageLight != nil {
		blitAdditive(img, r.stageLight, x0, lightY-r.stageLight.Bounds().Dy()/2, 0.55)
		return
	}
	topY := r.receptorY - int(185*r.scale)
	if topY < 0 {
		topY = 0
	}
	botY := r.receptorY + int(10*r.scale)
	if botY > r.Height {
		botY = r.Height
	}
	span := float64(botY - topY)
	if span <= 1 {
		return
	}
	c := r.Skin.LaneColors[lane]
	for y := topY; y < botY; y++ {
		t := float64(y-topY) / span
		if y > r.receptorY {
			t = 1.0 - float64(y-r.receptorY)/math.Max(1, 10*r.scale)
		}
		intensity := t * t * 0.28
		addR := uint32(float64(c.R)*intensity + 45*intensity)
		addG := uint32(float64(c.G)*intensity + 55*intensity)
		addB := uint32(float64(c.B)*intensity + 70*intensity)
		row := y * img.Stride
		for x := x0; x < x0+w; x++ {
			if x < 0 || x >= r.Width {
				continue
			}
			idx := row + x*4
			img.Pix[idx] = addClamp(img.Pix[idx], addR)
			img.Pix[idx+1] = addClamp(img.Pix[idx+1], addG)
			img.Pix[idx+2] = addClamp(img.Pix[idx+2], addB)
		}
	}
}

func (r *Renderer) drawHitEffects(img *image.RGBA, timeMS float64) {
	const burstDuration = 220.0
	start := sort.Search(len(r.Stats.Results), func(i int) bool { return r.Stats.Results[i].Time >= timeMS-burstDuration-200 })
	for i := start; i < len(r.Stats.Results); i++ {
		res := r.Stats.Results[i]
		if res.Time > timeMS+200 {
			break
		}
		if res.Grade == judge.Miss {
			continue
		}

		cx := r.laneLefts[res.Lane] + r.laneWidths[res.Lane]/2
		cy := r.receptorY

		// Continuous glow while holding a Long Note.
		if res.Long && timeMS >= res.HitTime && timeMS <= res.ReleaseTime {
			pulse := 0.72 + 0.18*math.Sin(timeMS*0.035)
			lighting := r.lightingL
			if lighting == nil {
				lighting = r.lightingN
			}
			if lighting != nil {
				blitAdditive(img, lighting, cx-lighting.Bounds().Dx()/2, cy-lighting.Bounds().Dy()/2, pulse*0.75)
			} else {
				blitAdditive(img, r.burstGlow, cx-r.burstGlow.Bounds().Dx()/2, cy-r.burstGlow.Bounds().Dy()/2, pulse*0.75)
			}
			r.drawShatterParticles(img, cx, cy, math.Mod(timeMS-res.HitTime, 160.0), i*31+int(timeMS/80), 0.75)
		}

		age := timeMS - res.HitTime
		if age < 0 || age > burstDuration {
			continue
		}
		t := age / burstDuration
		alpha := (1.0 - t) * (1.0 - t*0.35)
		if r.lightingN != nil {
			blitAdditive(img, r.lightingN, cx-r.lightingN.Bounds().Dx()/2, cy-r.lightingN.Bounds().Dy()/2, alpha)
		} else {
			blitAdditive(img, r.burstGlow, cx-r.burstGlow.Bounds().Dx()/2, cy-r.burstGlow.Bounds().Dy()/2, alpha*0.95)
			r.drawHorizontalFlare(img, cx, cy, r.laneWidths[res.Lane], alpha)
			r.drawShatterParticles(img, cx, cy, age, i+1, alpha)
		}
	}
}

func (r *Renderer) drawHorizontalFlare(img *image.RGBA, cx, cy, colW int, alpha float64) {
	halfW := int(float64(colW) * 1.9 * (0.7 + 0.3*(1.0-alpha)))
	halfH := int(math.Max(2, 6*r.scale*alpha))
	for dy := -halfH; dy <= halfH; dy++ {
		y := cy + dy
		if y < 0 || y >= r.Height {
			continue
		}
		vy := 1.0 - math.Abs(float64(dy))/float64(halfH+1)
		vy *= vy
		row := y * img.Stride
		for dx := -halfW; dx <= halfW; dx++ {
			x := cx + dx
			if x < 0 || x >= r.Width {
				continue
			}
			vx := 1.0 - math.Abs(float64(dx))/float64(halfW+1)
			power := vx * vx * vy * alpha * 0.75
			if power <= 0.01 {
				continue
			}
			idx := row + x*4
			img.Pix[idx] = addClamp(img.Pix[idx], uint32(190*power))
			img.Pix[idx+1] = addClamp(img.Pix[idx+1], uint32(232*power))
			img.Pix[idx+2] = addClamp(img.Pix[idx+2], uint32(255*power))
		}
	}
}

func (r *Renderer) drawShatterParticles(img *image.RGBA, cx, cy int, ageMS float64, seed int, alpha float64) {
	t := ageMS / 220.0
	if t < 0 || t > 1 {
		return
	}
	ease := 1.0 - (1.0-t)*(1.0-t)
	spread := float64(r.stageWidth) * 0.24 * ease
	if spread < 26*r.scale {
		spread = 26 * r.scale * ease
	}
	const count = 12
	for p := 0; p < count; p++ {
		h := uint32(seed*1103515245 + p*2654435761)
		angle := (float64(h%360) / 180.0 * math.Pi)
		dist := spread * (0.35 + 0.75*float64((h>>8)%100)/100.0)
		px := float64(cx) + math.Cos(angle)*dist
		py := float64(cy) + math.Sin(angle)*dist*0.72 - ease*14*r.scale
		sz := (3.5 + float64((h>>16)%5)) * r.scale * (1.0 - 0.55*t)
		col := color.NRGBA{R: 165, G: 225, B: 255, A: uint8(clampByte(230 * alpha))}
		if p%3 == 0 {
			col = color.NRGBA{R: 255, G: 255, B: 255, A: uint8(clampByte(245 * alpha))}
		}
		drawAdditiveDiamond(img, int(math.Round(px)), int(math.Round(py)), int(math.Round(sz)), col)
	}
}

func (r *Renderer) drawHUD(img *image.RGBA, timeMS float64) {
	duration := r.DurationMS
	if duration <= 0 {
		duration = maxMapTime(r.Map)
	}
	progress := math.Min(1, math.Max(0, timeMS/duration))

	// Top-center song progress pie clock + Accuracy (matching the reference screenshot).
	acc, combo, score, hp := statsAt(r.Stats.Results, timeMS)
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

	// Vertical cyan indicator bar to the right of the playfield (matching reference layout).
	barX := r.stageRight + int(16*r.scale)
	if barX+int(6*r.scale) < r.Width {
		barTop := int(48 * r.scale)
		barBot := r.Height - int(24*r.scale)
		barW := int(math.Max(3, 3.5*r.scale))
		fillAlpha(img, barX, barTop, barX+barW, barBot, color.NRGBA{R: 35, G: 55, B: 70, A: 140})
		fillH := int(float64(barBot-barTop) * hp)
		fillAlpha(img, barX, barBot-fillH, barX+barW, barBot, color.NRGBA{R: 72, G: 230, B: 255, A: 235})
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
			} else {
				label, col := judgmentVisual(res.Grade, alpha)
				lw := measureText(r.judgeFace, label)
				drawCenteredShadowText(img, stageCenterX-lw/2, judgeY, label, r.judgeFace, col)
			}
		}
	}

	// Hit Error Bar below the receptors.
	r.drawHitErrorBar(img, stageCenterX, timeMS)

	// Brief danser-style song info fade-out during the intro.
	if timeMS < 3200 && (r.Map.Title != "" || r.Map.Artist != "") {
		fade := 1.0
		if timeMS > 2400 {
			fade = 1.0 - (timeMS-2400)/800.0
		}
		a := uint8(clampByte(230 * fade))
		title := r.Map.Title
		if r.Map.Artist != "" {
			title = r.Map.Artist + " - " + title
		}
		if r.Map.Version != "" {
			title += " [" + r.Map.Version + "]"
		}
		drawShadowText(img, int(16*r.scale), int(26*r.scale), title, r.metaFace, color.NRGBA{R: 235, G: 240, B: 250, A: a})
	}
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

	// Colored timing windows: 50 (orange) -> 100/200 (green) -> 300/300g (cyan).
	fillAlpha(img, cx-halfW, barY-barH/2, cx+halfW, barY+barH/2, color.NRGBA{R: 218, G: 158, B: 68, A: 210})
	x100 := int(math.Round(r.windows.W100 / maxWin * float64(halfW)))
	fillAlpha(img, cx-x100, barY-barH/2, cx+x100, barY+barH/2, color.NRGBA{R: 96, G: 218, B: 74, A: 220})
	x300 := int(math.Round(r.windows.W300 / maxWin * float64(halfW)))
	fillAlpha(img, cx-x300, barY-barH/2, cx+x300, barY+barH/2, color.NRGBA{R: 80, G: 215, B: 255, A: 240})

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

func drawAdditiveDiamond(img *image.RGBA, cx, cy, r int, c color.NRGBA) {
	if r <= 0 || c.A == 0 {
		return
	}
	alphaBase := float64(c.A) / 255.0
	for dy := -r; dy <= r; dy++ {
		y := cy + dy
		if y < 0 || y >= img.Bounds().Dy() {
			continue
		}
		rem := r - absInt(dy)
		row := y * img.Stride
		for dx := -rem; dx <= rem; dx++ {
			x := cx + dx
			if x < 0 || x >= img.Bounds().Dx() {
				continue
			}
			dist := float64(absInt(dx)+absInt(dy)) / float64(r+1)
			power := (1.0 - dist*0.55) * alphaBase
			idx := row + x*4
			img.Pix[idx] = addClamp(img.Pix[idx], uint32(float64(c.R)*power))
			img.Pix[idx+1] = addClamp(img.Pix[idx+1], uint32(float64(c.G)*power))
			img.Pix[idx+2] = addClamp(img.Pix[idx+2], uint32(float64(c.B)*power))
		}
	}
}

func generateBurstGlow(size int) *image.NRGBA {
	if size < 16 {
		size = 16
	}
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	cx, cy := float64(size-1)/2.0, float64(size-1)/2.0
	radius := float64(size) / 2.0
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			dx, dy := (float64(x)-cx)/radius, (float64(y)-cy)/radius
			d := math.Hypot(dx, dy)
			if d >= 1.0 {
				continue
			}
			outer := math.Pow(1.0-d, 2.2)
			core := math.Exp(-d * d * 11.0)
			r := clampByte(130*outer + 255*core)
			g := clampByte(215*outer + 255*core)
			b := clampByte(255*outer + 255*core)
			a := clampByte(255 * math.Min(1, outer*0.9+core))
			img.SetNRGBA(x, y, color.NRGBA{R: r, G: g, B: b, A: a})
		}
	}
	return img
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

func blitStretchedY(dst *image.RGBA, src *image.NRGBA, dstX, dstY, targetH int, opacity float64) {
	if src == nil || targetH <= 0 || opacity <= 0 {
		return
	}
	sw, sh := src.Bounds().Dx(), src.Bounds().Dy()
	x0, y0 := maxInt(0, dstX), maxInt(0, dstY)
	x1, y1 := minInt(dst.Bounds().Dx(), dstX+sw), minInt(dst.Bounds().Dy(), dstY+targetH)
	if x0 >= x1 || y0 >= y1 {
		return
	}
	op := uint32(math.Round(opacity * 256))
	for y := y0; y < y1; y++ {
		sy := ((y - dstY) * sh) / targetH
		if sy >= sh {
			sy = sh - 1
		}
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
			inv := 255 - a
			dst.Pix[di] = uint8((uint32(src.Pix[si])*a + uint32(dst.Pix[di])*inv) / 255)
			dst.Pix[di+1] = uint8((uint32(src.Pix[si+1])*a + uint32(dst.Pix[di+1])*inv) / 255)
			dst.Pix[di+2] = uint8((uint32(src.Pix[si+2])*a + uint32(dst.Pix[di+2])*inv) / 255)
			dst.Pix[di+3] = 255
		}
	}
}

func blitAdditive(dst *image.RGBA, src *image.NRGBA, dstX, dstY int, opacity float64) {
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
			dst.Pix[di] = addClamp(dst.Pix[di], (uint32(src.Pix[si])*a)>>8)
			dst.Pix[di+1] = addClamp(dst.Pix[di+1], (uint32(src.Pix[si+1])*a)>>8)
			dst.Pix[di+2] = addClamp(dst.Pix[di+2], (uint32(src.Pix[si+2])*a)>>8)
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
	a := clampByte(alpha * 255)
	switch g {
	case judge.MAX:
		return "300g", color.NRGBA{R: 135, G: 235, B: 255, A: a}
	case judge.Hit300:
		return "300", color.NRGBA{R: 90, G: 205, B: 255, A: a}
	case judge.Hit200:
		return "200", color.NRGBA{R: 120, G: 235, B: 125, A: a}
	case judge.Hit100:
		return "100", color.NRGBA{R: 115, G: 185, B: 255, A: a}
	case judge.Hit50:
		return "50", color.NRGBA{R: 245, G: 185, B: 85, A: a}
	default:
		return "MISS", color.NRGBA{R: 240, G: 75, B: 75, A: a}
	}
}

func errorTickColor(g judge.Grade, alpha float64) color.NRGBA {
	a := clampByte(alpha * 220)
	switch g {
	case judge.MAX, judge.Hit300:
		return color.NRGBA{R: 90, G: 225, B: 255, A: a}
	case judge.Hit200, judge.Hit100:
		return color.NRGBA{R: 105, G: 230, B: 95, A: a}
	default:
		return color.NRGBA{R: 235, G: 170, B: 75, A: a}
	}
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
	return r.AccuracyAfter, r.ComboAfter, r.ScoreAfter, r.HPAfter
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

func addClamp(base byte, add uint32) byte {
	v := uint32(base) + add
	if v > 255 {
		return 255
	}
	return byte(v)
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
