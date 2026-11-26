package skin

import (
	"bufio"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type Skin struct {
	Dir       string
	ColumnWidth float64
	Images    map[string]image.Image
}

var imageNames = []string{
	"mania-note", "mania-note-alt", "mania-hit0", "mania-hit50", "mania-hit100",
	"mania-hit200", "mania-hit300", "mania-hit300g", "mania-key1", "mania-key1D",
	"mania-key2", "mania-key2D", "mania-keyS", "mania-keySD",
	"mania-stage-left", "mania-stage-right",
}

func Load(dir string, keys int) (*Skin, error) {
	s := &Skin{Dir: dir, ColumnWidth: 1.0 / float64(keys), Images: make(map[string]image.Image)}
	if dir != "" {
		info, err := os.Stat(dir)
		if err != nil {
			return nil, err
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("skin path %s is not a directory", dir)
		}
	} else {
		return s, nil
	}
	config := filepath.Join(dir, "skin.ini")
	file, err := os.Open(config)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	if err == nil {
		defer file.Close()
		section := ""
		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
				section = strings.Trim(line, "[]")
				continue
			}
			key, value, ok := strings.Cut(line, ":")
			if !ok || (section != "Mania" && section != fmt.Sprintf("Mania%dK", keys)) {
				continue
			}
			if strings.TrimSpace(key) == "ColumnWidth" {
				widthText := strings.TrimSpace(strings.Split(value, ",")[0])
				width, parseErr := strconv.ParseFloat(widthText, 64)
				if parseErr != nil || width <= 0 {
					return nil, fmt.Errorf("invalid ColumnWidth in %s", config)
				}
				s.ColumnWidth = width
			}
		}
		if err := scanner.Err(); err != nil {
			return nil, err
		}
	}
	for _, name := range imageNames {
		path := filepath.Join(dir, name+".png")
		imageFile, openErr := os.Open(path)
		if openErr != nil {
			if os.IsNotExist(openErr) {
				continue
			}
			return nil, openErr
		}
		img, decodeErr := png.Decode(imageFile)
		closeErr := imageFile.Close()
		if decodeErr != nil {
			return nil, fmt.Errorf("decode skin image %s: %w", path, decodeErr)
		}
		if closeErr != nil {
			return nil, closeErr
		}
		s.Images[name] = img
	}
	for lane := 1; lane <= keys; lane++ {
		for _, name := range []string{
			fmt.Sprintf("mania-note%d", lane), fmt.Sprintf("mania-key%d", lane),
			fmt.Sprintf("mania-key%dD", lane),
		} {
			path := filepath.Join(dir, name+".png")
			imageFile, openErr := os.Open(path)
			if openErr != nil {
				if os.IsNotExist(openErr) {
					continue
				}
				return nil, openErr
			}
			img, decodeErr := png.Decode(imageFile)
			closeErr := imageFile.Close()
			if decodeErr != nil {
				return nil, fmt.Errorf("decode skin image %s: %w", path, decodeErr)
			}
			if closeErr != nil {
				return nil, closeErr
			}
			s.Images[name] = img
		}
	}
	return s, nil
}

func (s *Skin) Image(name string) image.Image { return s.Images[name] }
