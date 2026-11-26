package replay

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/ulikunitz/xz/lzma"

	"github.com/felipefernandes1324/scroller-renderer/internal/beatmap"
)

type Event struct {
	Time float64
	Mask uint32
}

type keyChange struct {
	time float64
	lane int
	down bool
}

func Parse(path string, keys int) ([]Event, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	r := bytes.NewReader(data)
	mode, err := r.ReadByte()
	if err != nil {
		return nil, fmt.Errorf("read replay mode: %w", err)
	}
	if mode != 3 {
		return nil, fmt.Errorf("replay mode is %d; expected mania (3)", mode)
	}
	var version int32
	if err := binary.Read(r, binary.LittleEndian, &version); err != nil {
		return nil, fmt.Errorf("read replay header: %w", err)
	}
	for i := 0; i < 3; i++ {
		if _, err := readString(r); err != nil {
			return nil, fmt.Errorf("read replay metadata: %w", err)
		}
	}
	var counts [6]int16
	for i := range counts {
		if err := binary.Read(r, binary.LittleEndian, &counts[i]); err != nil {
			return nil, fmt.Errorf("read replay score counts: %w", err)
		}
	}
	var score int32
	var combo int16
	var perfect byte
	var mods int32
	if binary.Read(r, binary.LittleEndian, &score) != nil ||
		binary.Read(r, binary.LittleEndian, &combo) != nil {
		return nil, fmt.Errorf("truncated replay score header")
	}
	_ = score
	_ = combo
	if perfect, err = r.ReadByte(); err != nil {
		return nil, fmt.Errorf("read replay perfect flag: %w", err)
	}
	_ = perfect
	if err := binary.Read(r, binary.LittleEndian, &mods); err != nil {
		return nil, fmt.Errorf("read replay mods: %w", err)
	}
	_ = mods
	if _, err := readString(r); err != nil {
		return nil, fmt.Errorf("read replay life bar: %w", err)
	}
	var timestamp int64
	var compressedLength int32
	if binary.Read(r, binary.LittleEndian, &timestamp) != nil ||
		binary.Read(r, binary.LittleEndian, &compressedLength) != nil {
		return nil, fmt.Errorf("truncated replay data header")
	}
	_ = timestamp
	if compressedLength < 0 || int64(compressedLength) > int64(r.Len()) {
		return nil, fmt.Errorf("invalid replay compressed length %d", compressedLength)
	}
	compressed := make([]byte, compressedLength)
	if _, err := io.ReadFull(r, compressed); err != nil {
		return nil, fmt.Errorf("read compressed replay: %w", err)
	}
	decoder, err := lzma.NewReader(bytes.NewReader(compressed))
	if err != nil {
		return nil, fmt.Errorf("initialize LZMA replay decoder: %w", err)
	}
	eventsText, err := io.ReadAll(decoder)
	if err != nil {
		return nil, fmt.Errorf("decompress replay: %w", err)
	}
	return decodeEvents(string(eventsText), keys)
}

func readString(r *bytes.Reader) (string, error) {
	tag, err := r.ReadByte()
	if err != nil {
		return "", err
	}
	if tag == 0 {
		return "", nil
	}
	if tag != 0x0b {
		return "", fmt.Errorf("invalid osu string tag 0x%x", tag)
	}
	length, err := readULEB128(r)
	if err != nil {
		return "", err
	}
	if length > uint64(r.Len()) {
		return "", io.ErrUnexpectedEOF
	}
	buf := make([]byte, int(length))
	_, err = io.ReadFull(r, buf)
	return string(buf), err
}

func readULEB128(r io.ByteReader) (uint64, error) {
	var value uint64
	for shift := uint(0); shift < 64; shift += 7 {
		b, err := r.ReadByte()
		if err != nil {
			return 0, err
		}
		value |= uint64(b&0x7f) << shift
		if b&0x80 == 0 {
			return value, nil
		}
	}
	return 0, fmt.Errorf("invalid ULEB128 length")
}

func decodeEvents(text string, keys int) ([]Event, error) {
	var result []Event
	var time float64
	for _, row := range strings.Split(strings.TrimSpace(text), ",") {
		if row == "" || strings.HasPrefix(row, "-") {
			continue
		}
		parts := strings.Split(row, "|")
		if len(parts) < 3 {
			return nil, fmt.Errorf("malformed replay event %q", row)
		}
		delta, err := strconv.ParseFloat(parts[0], 64)
		if err != nil {
			return nil, fmt.Errorf("invalid replay event time %q: %w", parts[0], err)
		}
		mask64, err := strconv.ParseUint(parts[1], 10, 32)
		if err != nil {
			return nil, fmt.Errorf("invalid replay key mask %q: %w", parts[1], err)
		}
		time += delta
		mask := uint32(mask64) & uint32((uint64(1)<<keys)-1)
		result = append(result, Event{Time: time, Mask: mask})
	}
	return result, nil
}

func Auto(objects []beatmap.HitObject, keys int) []Event {
	changes := make([]keyChange, 0, len(objects)*2)
	for _, object := range objects {
		changes = append(changes, keyChange{time: object.Time, lane: object.Lane, down: true})
		end := object.Time + 1
		if object.Long {
			end = object.End
		}
		changes = append(changes, keyChange{time: end, lane: object.Lane, down: false})
	}
	sort.SliceStable(changes, func(i, j int) bool {
		if changes[i].time == changes[j].time {
			return !changes[i].down && changes[j].down
		}
		return changes[i].time < changes[j].time
	})
	var events []Event
	mask := uint32(0)
	for _, c := range changes {
		if c.lane < 0 || c.lane >= keys {
			continue
		}
		bit := uint32(1) << c.lane
		if c.down {
			mask |= bit
		} else {
			mask &^= bit
		}
		events = append(events, Event{Time: c.time, Mask: mask})
	}
	return events
}
