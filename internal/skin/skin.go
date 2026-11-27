package skin

import (
	"bufio"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type ManiaConfig struct {
	Keys                int
	HitPosition         float64
	ComboPosition       float64
	ScorePosition       float64
	AccuracyPosition    float64
	HasAccuracyPosition bool
	LightPosition       float64
	ColumnWidths        []float64
	ColumnLineWidth     []float64
	ColumnColors        []color.NRGBA
	NoteImages          []string
	NoteHeadImages      []string
	NoteBodyImages      []string
	NoteTailImages      []string
	KeyImages           []string
	KeyDownImages       []string
	StageLeft           string
	StageRight          string
	StageBottom         string
	StageHint           string
	StageLight          string
	LightingN           string
	LightingL           string
	HitImages           map[string]string
}

type Skin struct {
	Dir          string
	Keys         int
	ColumnWidth  float64
	Mania        ManiaConfig
	ScorePrefix  string
	ComboPrefix  string
	ScoreOverlap int
	ComboOverlap int
	Images       map[string]*image.NRGBA
	FallbackNote []*image.NRGBA
	FallbackHead []*image.NRGBA
	FallbackBody []*image.NRGBA
	FallbackTail []*image.NRGBA
	FallbackKey  []*image.NRGBA
	FallbackKeyD []*image.NRGBA
	LaneColors   []color.NRGBA
}

var defaultHitNames = []string{
	"mania-hit0", "mania-hit50", "mania-hit100", "mania-hit200", "mania-hit300", "mania-hit300g",
}

func Load(dir string, keys int) (*Skin, error) {
	defaultWidth := 46.0
	if keys >= 7 {
		defaultWidth = 40.0
	}
	if keys >= 9 {
		defaultWidth = 34.0
	}
	s := &Skin{
		Dir:         dir,
		Keys:        keys,
		ColumnWidth: defaultWidth,
		ScorePrefix: "score",
		ComboPrefix: "score",
		Images:      make(map[string]*image.NRGBA),
		Mania:       defaultManiaConfig(keys, defaultWidth),
		LaneColors:  defaultPalette(keys),
	}
	s.generateFallbacks()

	if dir == "" {
		return s, nil
	}
	info, err := os.Stat(dir)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("skin path %s is not a directory", dir)
	}

	if err := s.parseINI(filepath.Join(dir, "skin.ini")); err != nil {
		return nil, err
	}
	if len(s.Mania.ColumnWidths) > 0 {
		s.ColumnWidth = s.Mania.ColumnWidths[0]
	}

	names := make(map[string]struct{})
	for _, n := range defaultHitNames {
		names[n] = struct{}{}
	}
	for _, mapped := range s.Mania.HitImages {
		if mapped != "" {
			names[mapped] = struct{}{}
		}
	}
	for _, name := range []string{
		s.Mania.StageLeft, s.Mania.StageRight, s.Mania.StageBottom, s.Mania.StageHint,
		s.Mania.StageLight, s.Mania.LightingN, s.Mania.LightingL,
		"mania-stage-left", "mania-stage-right", "mania-stage-bottom", "mania-stage-hint",
		"mania-stage-light", "lightingN", "lightingL",
		"mania-note", "mania-note-alt", "mania-note1", "mania-note2", "mania-noteS",
		"mania-note1H", "mania-note2H", "mania-noteSH",
		"mania-note1L", "mania-note2L", "mania-noteSL",
		"mania-note1T", "mania-note2T", "mania-noteST",
		"mania-key1", "mania-key1D", "mania-key2", "mania-key2D", "mania-keyS", "mania-keySD",
	} {
		if name != "" {
			names[name] = struct{}{}
		}
	}
	for i := 0; i < keys; i++ {
		for _, n := range []string{
			s.Mania.NoteImages[i], s.Mania.NoteHeadImages[i], s.Mania.NoteBodyImages[i], s.Mania.NoteTailImages[i],
			s.Mania.KeyImages[i], s.Mania.KeyDownImages[i],
			fmt.Sprintf("mania-note%d", i+1), fmt.Sprintf("mania-note%dH", i+1),
			fmt.Sprintf("mania-note%dL", i+1), fmt.Sprintf("mania-note%dT", i+1),
			fmt.Sprintf("mania-key%d", i+1), fmt.Sprintf("mania-key%dD", i+1),
		} {
			if n != "" {
				names[n] = struct{}{}
			}
		}
	}
	for _, prefix := range []string{s.ScorePrefix, s.ComboPrefix} {
		if prefix == "" {
			continue
		}
		for d := 0; d <= 9; d++ {
			names[fmt.Sprintf("%s-%d", prefix, d)] = struct{}{}
		}
		names[prefix+"-dot"] = struct{}{}
		names[prefix+"-comma"] = struct{}{}
		names[prefix+"-percent"] = struct{}{}
	}

	for name := range names {
		if _, err := s.loadSprite(name); err != nil {
			return nil, err
		}
	}
	return s, nil
}

func defaultManiaConfig(keys int, colWidth float64) ManiaConfig {
	cfg := ManiaConfig{
		Keys:             keys,
		HitPosition:      402,
		ComboPosition:    155,
		ScorePosition:    270,
		AccuracyPosition: 28,
		LightPosition:    413,
		ColumnWidths:     make([]float64, keys),
		ColumnLineWidth:  make([]float64, keys+1),
		ColumnColors:     make([]color.NRGBA, keys),
		NoteImages:       make([]string, keys),
		NoteHeadImages:   make([]string, keys),
		NoteBodyImages:   make([]string, keys),
		NoteTailImages:   make([]string, keys),
		KeyImages:        make([]string, keys),
		KeyDownImages:    make([]string, keys),
		StageLeft:        "mania-stage-left",
		StageRight:       "mania-stage-right",
		StageBottom:      "mania-stage-bottom",
		StageHint:        "mania-stage-hint",
		StageLight:       "mania-stage-light",
		LightingN:        "lightingN",
		LightingL:        "lightingL",
		HitImages:        make(map[string]string),
	}
	for i := 0; i < keys; i++ {
		cfg.ColumnWidths[i] = colWidth
		cfg.ColumnColors[i] = color.NRGBA{R: 0, G: 0, B: 0, A: 235}
	}
	cfg.ColumnLineWidth[0] = 2
	cfg.ColumnLineWidth[keys] = 2
	return cfg
}

func defaultPalette(keys int) []color.NRGBA {
	blue := color.NRGBA{R: 124, G: 181, B: 221, A: 255}
	pink := color.NRGBA{R: 214, G: 140, B: 218, A: 255}
	gold := color.NRGBA{R: 235, G: 205, B: 112, A: 255}

	colors := make([]color.NRGBA, keys)
	switch keys {
	case 4:
		colors[0], colors[1], colors[2], colors[3] = blue, pink, pink, blue
	case 5:
		colors[0], colors[1], colors[2], colors[3], colors[4] = blue, pink, gold, pink, blue
	case 6:
		colors[0], colors[1], colors[2], colors[3], colors[4], colors[5] = blue, pink, blue, blue, pink, blue
	case 7:
		colors[0], colors[1], colors[2], colors[3], colors[4], colors[5], colors[6] = blue, blue, pink, pink, pink, blue, blue
	default:
		for i := 0; i < keys; i++ {
			dist := i
			if keys-1-i < dist {
				dist = keys - 1 - i
			}
			if dist%2 == 0 {
				colors[i] = blue
			} else {
				colors[i] = pink
			}
		}
	}
	return colors
}

func (s *Skin) generateFallbacks() {
	const size = 128
	s.FallbackNote = make([]*image.NRGBA, s.Keys)
	s.FallbackHead = make([]*image.NRGBA, s.Keys)
	s.FallbackBody = make([]*image.NRGBA, s.Keys)
	s.FallbackTail = make([]*image.NRGBA, s.Keys)
	s.FallbackKey = make([]*image.NRGBA, s.Keys)
	s.FallbackKeyD = make([]*image.NRGBA, s.Keys)

	ringColor := color.NRGBA{R: 162, G: 168, B: 178, A: 240}
	ringDown := color.NRGBA{R: 235, G: 245, B: 255, A: 255}
	for i := 0; i < s.Keys; i++ {
		c := s.LaneColors[i]
		s.FallbackNote[i] = renderCircleSprite(size, c)
		s.FallbackHead[i] = renderCircleSprite(size, c)
		s.FallbackTail[i] = renderCircleSprite(size, color.NRGBA{R: c.R, G: c.G, B: c.B, A: 215})
		s.FallbackBody[i] = renderLongBodySprite(size, color.NRGBA{R: c.R, G: c.G, B: c.B, A: 175})
		s.FallbackKey[i] = renderRingSprite(size, ringColor, color.NRGBA{R: 18, G: 22, B: 30, A: 110})
		s.FallbackKeyD[i] = renderRingSprite(size, ringDown, color.NRGBA{R: c.R, G: c.G, B: c.B, A: 145})
	}
}

func renderCircleSprite(size int, c color.NRGBA) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	cx, cy := float64(size-1)/2.0, float64(size-1)/2.0
	radius := float64(size)*0.45 - 1.0
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			dx, dy := float64(x)-cx, float64(y)-cy
			dist := math.Hypot(dx, dy)
			cov := clamp01(radius + 0.75 - dist)
			if cov <= 0 {
				continue
			}
			// Subtle spherical highlight to keep notes crisp without looking noisy.
			shade := 0.94 + 0.08*clamp01(1.0-math.Hypot(dx+radius*0.2, dy+radius*0.25)/radius)
			a := uint8(math.Round(float64(c.A) * cov))
			img.SetNRGBA(x, y, color.NRGBA{
				R: clampByte(float64(c.R) * shade),
				G: clampByte(float64(c.G) * shade),
				B: clampByte(float64(c.B) * shade),
				A: a,
			})
		}
	}
	return img
}

func renderRingSprite(size int, stroke, fill color.NRGBA) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	cx, cy := float64(size-1)/2.0, float64(size-1)/2.0
	outer := float64(size)*0.46 - 1.0
	inner := outer - math.Max(3.5, float64(size)*0.055)
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			dist := math.Hypot(float64(x)-cx, float64(y)-cy)
			outerCov := clamp01(outer + 0.75 - dist)
			if outerCov <= 0 {
				continue
			}
			innerCov := clamp01(inner + 0.75 - dist)
			ringCov := clamp01(outerCov - innerCov)
			bgCov := innerCov
			r := (float64(stroke.R)*ringCov*float64(stroke.A) + float64(fill.R)*bgCov*float64(fill.A)) / 255.0
			g := (float64(stroke.G)*ringCov*float64(stroke.A) + float64(fill.G)*bgCov*float64(fill.A)) / 255.0
			b := (float64(stroke.B)*ringCov*float64(stroke.A) + float64(fill.B)*bgCov*float64(fill.A)) / 255.0
			a := (ringCov*float64(stroke.A) + bgCov*float64(fill.A))
			if a > 255 {
				a = 255
			}
			if a <= 0 {
				continue
			}
			inv := 255.0 / a
			img.SetNRGBA(x, y, color.NRGBA{
				R: clampByte(r * inv),
				G: clampByte(g * inv),
				B: clampByte(b * inv),
				A: clampByte(a),
			})
		}
	}
	return img
}

func renderLongBodySprite(size int, c color.NRGBA) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, size, 16))
	cx := float64(size-1) / 2.0
	halfW := float64(size) * 0.36
	for x := 0; x < size; x++ {
		dx := math.Abs(float64(x) - cx)
		cov := clamp01(halfW + 0.75 - dx)
		if cov <= 0 {
			continue
		}
		edgeFade := 0.85 + 0.15*(1.0-dx/halfW)
		px := color.NRGBA{
			R: clampByte(float64(c.R) * edgeFade),
			G: clampByte(float64(c.G) * edgeFade),
			B: clampByte(float64(c.B) * edgeFade),
			A: uint8(math.Round(float64(c.A) * cov)),
		}
		for y := 0; y < 16; y++ {
			img.SetNRGBA(x, y, px)
		}
	}
	return img
}

func (s *Skin) parseINI(path string) error {
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer file.Close()

	type rawBlock struct {
		name string
		kv   map[string]string
	}
	var blocks []rawBlock
	var current *rawBlock

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(strings.TrimPrefix(scanner.Text(), "\ufeff"))
		if line == "" || strings.HasPrefix(line, "//") {
			continue
		}
		if idx := strings.Index(line, "//"); idx > 0 {
			line = strings.TrimSpace(line[:idx])
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			name := strings.TrimSpace(line[1 : len(line)-1])
			blocks = append(blocks, rawBlock{name: name, kv: make(map[string]string)})
			current = &blocks[len(blocks)-1]
			continue
		}
		if current == nil {
			continue
		}
		if key, value, ok := strings.Cut(line, ":"); ok {
			current.kv[strings.TrimSpace(key)] = strings.TrimSpace(value)
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}

	var matchedMania *rawBlock
	var genericMania *rawBlock
	for i := range blocks {
		b := &blocks[i]
		if strings.EqualFold(b.name, "Fonts") {
			if v, ok := b.kv["ScorePrefix"]; ok && v != "" {
				s.ScorePrefix = cleanSpriteRef(v)
			}
			if v, ok := b.kv["ComboPrefix"]; ok && v != "" {
				s.ComboPrefix = cleanSpriteRef(v)
			}
			if v, ok := b.kv["ScoreOverlap"]; ok {
				if n, err := strconv.Atoi(v); err == nil {
					s.ScoreOverlap = n
				}
			}
			if v, ok := b.kv["ComboOverlap"]; ok {
				if n, err := strconv.Atoi(v); err == nil {
					s.ComboOverlap = n
				}
			}
		}
		if strings.EqualFold(b.name, fmt.Sprintf("Mania%dK", s.Keys)) {
			matchedMania = b
		} else if strings.EqualFold(b.name, "Mania") {
			if kStr, ok := b.kv["Keys"]; ok {
				if k, err := strconv.Atoi(strings.TrimSpace(kStr)); err == nil && k == s.Keys {
					matchedMania = b
				}
			} else if genericMania == nil {
				genericMania = b
			}
		}
	}
	if matchedMania == nil {
		matchedMania = genericMania
	}
	if matchedMania == nil {
		return nil
	}

	kv := matchedMania.kv
	if v, ok := kv["HitPosition"]; ok {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f >= 100 && f <= 480 {
			s.Mania.HitPosition = f
		}
	}
	if v, ok := kv["ComboPosition"]; ok {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f >= 0 && f <= 480 {
			s.Mania.ComboPosition = f
		}
	}
	if v, ok := kv["ScorePosition"]; ok {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f >= 0 && f <= 480 {
			s.Mania.ScorePosition = f
		}
	}
	if v, ok := kv["AccuracyPosition"]; ok {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f >= 0 && f <= 480 {
			s.Mania.AccuracyPosition = f
			s.Mania.HasAccuracyPosition = true
		}
	}
	if v, ok := kv["LightPosition"]; ok {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f >= 0 && f <= 480 {
			s.Mania.LightPosition = f
		}
	}
	if v, ok := kv["ColumnWidth"]; ok {
		parts := strings.Split(v, ",")
		for i := 0; i < s.Keys; i++ {
			idx := i
			if idx >= len(parts) {
				idx = len(parts) - 1
			}
			if w, err := strconv.ParseFloat(strings.TrimSpace(parts[idx]), 64); err == nil && w > 0 {
				s.Mania.ColumnWidths[i] = w
			}
		}
	}
	if v, ok := kv["ColumnLineWidth"]; ok {
		parts := strings.Split(v, ",")
		for i := 0; i <= s.Keys && i < len(parts); i++ {
			if w, err := strconv.ParseFloat(strings.TrimSpace(parts[i]), 64); err == nil && w >= 0 {
				s.Mania.ColumnLineWidth[i] = w
			}
		}
	}
	for i := 0; i < s.Keys; i++ {
		if v, ok := kv[fmt.Sprintf("Colour%d", i+1)]; ok {
			if c, okColor := parseRGBAColor(v); okColor {
				s.Mania.ColumnColors[i] = c
			}
		}
		if v, ok := kv[fmt.Sprintf("NoteImage%d", i)]; ok {
			s.Mania.NoteImages[i] = cleanSpriteRef(v)
		}
		if v, ok := kv[fmt.Sprintf("NoteImage%dH", i)]; ok {
			s.Mania.NoteHeadImages[i] = cleanSpriteRef(v)
		}
		if v, ok := kv[fmt.Sprintf("NoteImage%dL", i)]; ok {
			s.Mania.NoteBodyImages[i] = cleanSpriteRef(v)
		}
		if v, ok := kv[fmt.Sprintf("NoteImage%dT", i)]; ok {
			s.Mania.NoteTailImages[i] = cleanSpriteRef(v)
		}
		if v, ok := kv[fmt.Sprintf("KeyImage%d", i)]; ok {
			s.Mania.KeyImages[i] = cleanSpriteRef(v)
		}
		if v, ok := kv[fmt.Sprintf("KeyImage%dD", i)]; ok {
			s.Mania.KeyDownImages[i] = cleanSpriteRef(v)
		}
	}
	for iniKey, defaultKey := range map[string]*string{
		"StageLeft":   &s.Mania.StageLeft,
		"StageRight":  &s.Mania.StageRight,
		"StageBottom": &s.Mania.StageBottom,
		"StageHint":   &s.Mania.StageHint,
		"StageLight":  &s.Mania.StageLight,
		"LightingN":   &s.Mania.LightingN,
		"LightingL":   &s.Mania.LightingL,
	} {
		if v, ok := kv[iniKey]; ok && v != "" {
			*defaultKey = cleanSpriteRef(v)
		}
	}
	for iniKey, stdKey := range map[string]string{
		"Hit0":    "mania-hit0",
		"Hit50":   "mania-hit50",
		"Hit100":  "mania-hit100",
		"Hit200":  "mania-hit200",
		"Hit300":  "mania-hit300",
		"Hit300g": "mania-hit300g",
	} {
		if v, ok := kv[iniKey]; ok && v != "" {
			s.Mania.HitImages[stdKey] = cleanSpriteRef(v)
		}
	}
	return nil
}

func cleanSpriteRef(v string) string {
	v = strings.TrimSpace(v)
	v = strings.TrimSuffix(v, ".png")
	v = strings.ReplaceAll(v, `\`, "/")
	return v
}

func parseRGBAColor(v string) (color.NRGBA, bool) {
	parts := strings.Split(v, ",")
	if len(parts) < 3 {
		return color.NRGBA{}, false
	}
	vals := make([]uint8, 4)
	vals[3] = 255
	for i := 0; i < 4 && i < len(parts); i++ {
		n, err := strconv.Atoi(strings.TrimSpace(parts[i]))
		if err != nil {
			return color.NRGBA{}, false
		}
		if n < 0 {
			n = 0
		} else if n > 255 {
			n = 255
		}
		vals[i] = uint8(n)
	}
	return color.NRGBA{R: vals[0], G: vals[1], B: vals[2], A: vals[3]}, true
}

func (s *Skin) loadSprite(name string) (*image.NRGBA, error) {
	if existing, ok := s.Images[name]; ok {
		return existing, nil
	}
	rel := filepath.FromSlash(name)
	for _, suffix := range []string{"@2x.png", ".png"} {
		path := filepath.Join(s.Dir, rel+suffix)
		file, err := os.Open(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		decoded, decErr := png.Decode(file)
		closeErr := file.Close()
		if decErr != nil {
			return nil, fmt.Errorf("decode skin image %s: %w", path, decErr)
		}
		if closeErr != nil {
			return nil, closeErr
		}
		nrgba := toNRGBA(decoded)
		s.Images[name] = nrgba
		return nrgba, nil
	}
	return nil, nil
}

func toNRGBA(src image.Image) *image.NRGBA {
	if nrgba, ok := src.(*image.NRGBA); ok {
		return nrgba
	}
	b := src.Bounds()
	dst := image.NewNRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(dst, dst.Bounds(), src, b.Min, draw.Src)
	return dst
}

func (s *Skin) Image(name string) *image.NRGBA {
	if name == "" {
		return nil
	}
	return s.Images[name]
}

func (s *Skin) NoteSprite(lane int) *image.NRGBA {
	if lane >= 0 && lane < s.Keys {
		for _, candidate := range []string{
			s.Mania.NoteImages[lane],
			fmt.Sprintf("mania-note%d", lane+1),
			defaultRoleSprite(lane, s.Keys, "mania-note"),
			"mania-note",
		} {
			if img := s.Image(candidate); img != nil {
				return img
			}
		}
		return s.FallbackNote[lane]
	}
	return nil
}

func (s *Skin) LongHeadSprite(lane int) *image.NRGBA {
	if lane >= 0 && lane < s.Keys {
		for _, candidate := range []string{
			s.Mania.NoteHeadImages[lane],
			fmt.Sprintf("mania-note%dH", lane+1),
			defaultRoleSprite(lane, s.Keys, "mania-note") + "H",
			s.Mania.NoteImages[lane],
			fmt.Sprintf("mania-note%d", lane+1),
		} {
			if img := s.Image(candidate); img != nil {
				return img
			}
		}
		return s.FallbackHead[lane]
	}
	return nil
}

func (s *Skin) LongBodySprite(lane int) *image.NRGBA {
	if lane >= 0 && lane < s.Keys {
		for _, candidate := range []string{
			s.Mania.NoteBodyImages[lane],
			fmt.Sprintf("mania-note%dL", lane+1),
			defaultRoleSprite(lane, s.Keys, "mania-note") + "L",
		} {
			if img := s.Image(candidate); img != nil {
				return img
			}
		}
		return s.FallbackBody[lane]
	}
	return nil
}

func (s *Skin) LongTailSprite(lane int) *image.NRGBA {
	if lane >= 0 && lane < s.Keys {
		for _, candidate := range []string{
			s.Mania.NoteTailImages[lane],
			fmt.Sprintf("mania-note%dT", lane+1),
			defaultRoleSprite(lane, s.Keys, "mania-note") + "T",
			s.Mania.NoteHeadImages[lane],
			fmt.Sprintf("mania-note%dH", lane+1),
			s.Mania.NoteImages[lane],
			fmt.Sprintf("mania-note%d", lane+1),
		} {
			if img := s.Image(candidate); img != nil {
				return img
			}
		}
		return s.FallbackTail[lane]
	}
	return nil
}

func (s *Skin) KeySprite(lane int, pressed bool) *image.NRGBA {
	if lane < 0 || lane >= s.Keys {
		return nil
	}
	suffix := ""
	if pressed {
		suffix = "D"
	}
	var candidates []string
	if pressed {
		candidates = append(candidates, s.Mania.KeyDownImages[lane])
	} else {
		candidates = append(candidates, s.Mania.KeyImages[lane])
	}
	candidates = append(candidates,
		fmt.Sprintf("mania-key%d%s", lane+1, suffix),
		defaultRoleSprite(lane, s.Keys, "mania-key")+suffix,
	)
	for _, candidate := range candidates {
		if img := s.Image(candidate); img != nil {
			return img
		}
	}
	if pressed {
		return s.FallbackKeyD[lane]
	}
	return s.FallbackKey[lane]
}

func (s *Skin) HitSprite(grade string) *image.NRGBA {
	if mapped := s.Mania.HitImages[grade]; mapped != "" {
		if img := s.Image(mapped); img != nil {
			return img
		}
	}
	return s.Image(grade)
}

func defaultRoleSprite(lane, keys int, base string) string {
	if keys%2 == 1 && lane == keys/2 {
		return base + "S"
	}
	dist := lane
	if keys-1-lane < dist {
		dist = keys - 1 - lane
	}
	if dist%2 == 0 {
		return base + "1"
	}
	return base + "2"
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
