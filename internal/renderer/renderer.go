package renderer

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"math"
	"sort"

	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
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
	DurationMS    float64
	notes         []noteVisual
	prepared      bool
}

type noteVisual struct {
	object              beatmap.HitObject
	startPosition, endPosition float64
}

var (
	background = color.RGBA{R: 17, G: 20, B: 31, A: 255}
	laneColor  = color.RGBA{R: 30, G: 35, B: 49, A: 255}
	noteColor  = color.RGBA{R: 85, G: 196, B: 245, A: 255}
)

func (r *Renderer) Frame(timeMS float64) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, r.Width, r.Height))
	r.FrameInto(timeMS, img)
	return img
}

func (r *Renderer) FrameInto(timeMS float64, img *image.RGBA) {
	r.prepareNotes()
	draw.Draw(img, img.Bounds(), &image.Uniform{C: background}, image.Point{}, draw.Src)
	playWidth := int(float64(r.Width) * 0.55)
	left := (r.Width - playWidth) / 2
	receptorY := int(float64(r.Height) * 0.78)
	laneWidth := float64(playWidth) / float64(r.Map.Keys)
	for lane := 0; lane < r.Map.Keys; lane++ {
		x0 := left + int(float64(lane)*laneWidth)
		x1 := left + int(float64(lane+1)*laneWidth) - 1
		fill(img, x0, 0, x1, r.Height, laneColor)
		if lane > 0 {
			fill(img, x0, 0, x0+1, r.Height, color.RGBA{R: 68, G: 73, B: 88, A: 255})
		}
	}
	mask := keyMaskAt(r.Replay, timeMS)
	for lane := 0; lane < r.Map.Keys; lane++ {
		x0 := left + int(float64(lane)*laneWidth)
		x1 := left + int(float64(lane+1)*laneWidth) - 2
		keyName := fmt.Sprintf("mania-key%d", lane+1)
		receptor := color.RGBA{R: 191, G: 198, B: 214, A: 255}
		if mask&(uint32(1)<<lane) != 0 {
			receptor = color.RGBA{R: 255, G: 207, B: 77, A: 255}
			keyName += "D"
			fill(img, x0, receptorY+8, x1, receptorY+28, color.RGBA{R: 255, G: 178, B: 52, A: 75})
		}
		if sprite := r.Skin.Image(keyName); sprite != nil {
			drawSprite(img, sprite, image.Rect(x0+2, receptorY-3, x1, receptorY+28))
		} else {
			fill(img, x0+2, receptorY, x1, receptorY+7, receptor)
		}
	}
	const pixelsPerMS = 0.38
	nowPosition := r.Timeline.Position(timeMS)
	for _, note := range r.notes {
		object := note.object
		y := float64(receptorY) - (note.startPosition-nowPosition)*pixelsPerMS
		if object.Long {
			endY := float64(receptorY) - (note.endPosition-nowPosition)*pixelsPerMS
			if math.Max(y, endY) < -20 || math.Min(y, endY) > float64(receptorY+30) {
				continue
			}
			top, bottom := int(math.Min(y, endY)), int(math.Max(y, endY))
			x0 := left + int(float64(object.Lane)*laneWidth) + int(laneWidth*0.25)
			x1 := left + int(float64(object.Lane+1)*laneWidth) - int(laneWidth*0.25)
			fill(img, x0, top, x1, bottom, color.RGBA{R: 54, G: 143, B: 202, A: 220})
			if sprite := r.Skin.Image(fmt.Sprintf("mania-note%d", object.Lane+1)); sprite != nil {
				drawSprite(img, sprite, image.Rect(x0-2, int(y)-8, x1+2, int(y)+8))
				drawSprite(img, sprite, image.Rect(x0-2, int(endY)-8, x1+2, int(endY)+8))
			} else {
				drawSpriteOrFill(img, r.Skin.Image("mania-note"), image.Rect(x0-2, int(y)-8, x1+2, int(y)+8), noteColor)
				drawSpriteOrFill(img, r.Skin.Image("mania-note"), image.Rect(x0-2, int(endY)-8, x1+2, int(endY)+8), noteColor)
			}
		} else if y > -20 && y < float64(receptorY+40) {
			x0 := left + int(float64(object.Lane)*laneWidth) + 3
			x1 := left + int(float64(object.Lane+1)*laneWidth) - 4
			rect := image.Rect(x0, int(y)-8, x1, int(y)+8)
			drawSpriteOrFill(img, r.Skin.Image(fmt.Sprintf("mania-note%d", object.Lane+1)), rect, noteColor)
			if r.Skin.Image(fmt.Sprintf("mania-note%d", object.Lane+1)) == nil {
				drawSpriteOrFill(img, r.Skin.Image("mania-note"), rect, noteColor)
			}
		}
	}
	// Draw timing judgments and the compact HUD without requiring a windowing system.
	drawText(img, 12, 24, fmt.Sprintf("ACCURACY  %05.2f%%", accuracyAt(r.Stats.Results, timeMS)), color.White)
	drawText(img, 12, 44, fmt.Sprintf("COMBO  %d", comboAt(r.Stats.Results, timeMS)), color.White)
	duration := r.DurationMS
	if duration <= 0 {
		duration = maxMapTime(r.Map)
	}
	progress := math.Min(1, math.Max(0, timeMS/duration))
	fill(img, 0, r.Height-5, int(float64(r.Width)*progress), r.Height, color.RGBA{R: 80, G: 190, B: 255, A: 255})
	start := sort.Search(len(r.Stats.Results), func(i int) bool { return r.Stats.Results[i].Time >= timeMS-280 })
	for _, result := range r.Stats.Results[start:] {
		if result.Time >= timeMS+280 {
			break
		}
		if math.Abs(result.Time-timeMS) < 280 {
			label := string(result.Grade)
			if result.Grade == Miss {
				label = "MISS"
			} else {
				label = string(result.Grade)
				label = label[len("mania-hit"):]
			}
			if sprite := r.Skin.Image(string(result.Grade)); sprite != nil {
				drawSprite(img, sprite, image.Rect(r.Width/2-48, int(float64(r.Height)*0.60)-20, r.Width/2+48, int(float64(r.Height)*0.60)+20))
			} else {
				drawText(img, r.Width/2-24, int(float64(r.Height)*0.60), label, color.RGBA{R: 255, G: 237, B: 156, A: 255})
			}
		}
	}
}

func (r *Renderer) prepareNotes() {
	if r.prepared {
		return
	}
	r.notes = make([]noteVisual, 0, len(r.Map.Objects))
	for _, object := range r.Map.Objects {
		r.notes = append(r.notes, noteVisual{
			object: object, startPosition: r.Timeline.Position(object.Time),
			endPosition: r.Timeline.Position(object.End),
		})
	}
	r.prepared = true
}

func fill(img *image.RGBA, x0, y0, x1, y1 int, c color.Color) {
	rect := image.Rect(x0, y0, x1, y1).Intersect(img.Bounds())
	if rect.Empty() {
		return
	}
	draw.Draw(img, rect, &image.Uniform{C: c}, image.Point{}, draw.Src)
}

func drawSpriteOrFill(dst *image.RGBA, sprite image.Image, rect image.Rectangle, fallback color.Color) {
	if sprite == nil {
		fill(dst, rect.Min.X, rect.Min.Y, rect.Max.X, rect.Max.Y, fallback)
		return
	}
	drawSprite(dst, sprite, rect)
}

func drawSprite(dst *image.RGBA, sprite image.Image, rect image.Rectangle) {
	target := rect
	rect = rect.Intersect(dst.Bounds())
	bounds := sprite.Bounds()
	if rect.Empty() || bounds.Empty() || target.Empty() {
		return
	}
	for y := rect.Min.Y; y < rect.Max.Y; y++ {
		sy := bounds.Min.Y + (y-target.Min.Y)*bounds.Dy()/target.Dy()
		for x := rect.Min.X; x < rect.Max.X; x++ {
			sx := bounds.Min.X + (x-target.Min.X)*bounds.Dx()/target.Dx()
			src := color.NRGBAModel.Convert(sprite.At(sx, sy)).(color.NRGBA)
			if src.A == 255 {
				dst.SetRGBA(x, y, color.RGBA{R: src.R, G: src.G, B: src.B, A: 255})
				continue
			}
			if src.A == 0 {
				continue
			}
			back := dst.RGBAAt(x, y)
			inv := uint32(255 - src.A)
			alpha := uint32(src.A)
			dst.SetRGBA(x, y, color.RGBA{
				R: uint8((uint32(src.R)*alpha + uint32(back.R)*inv) / 255),
				G: uint8((uint32(src.G)*alpha + uint32(back.G)*inv) / 255),
				B: uint8((uint32(src.B)*alpha + uint32(back.B)*inv) / 255),
				A: uint8(alpha + uint32(back.A)*inv/255),
			})
		}
	}
}

func drawText(img *image.RGBA, x, y int, text string, c color.Color) {
	d := font.Drawer{Dst: img, Src: image.NewUniform(c), Face: basicfont.Face7x13, Dot: fixed.P(x, y)}
	d.DrawString(text)
}

func keyMaskAt(events []replay.Event, time float64) uint32 {
	index := sort.Search(len(events), func(i int) bool { return events[i].Time > time })
	if index == 0 {
		return 0
	}
	return events[index-1].Mask
}

func comboAt(results []judge.Result, time float64) int {
	end := sort.Search(len(results), func(i int) bool { return results[i].Time > time })
	if end == 0 {
		return 0
	}
	return results[end-1].ComboAfter
}

func accuracyAt(results []judge.Result, time float64) float64 {
	end := sort.Search(len(results), func(i int) bool { return results[i].Time > time })
	if end == 0 {
		return 0
	}
	return results[end-1].AccuracyAfter
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
