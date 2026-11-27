package beatmap

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"strings"
)

type osuDatabaseEntry struct {
	Hash            string
	FileName        string
	FolderName      string
	ManiaStarRating float64
}

func parseOsuDatabase(data []byte, expected string) (osuDatabaseEntry, bool, error) {
	reader := bytes.NewReader(data)
	version, err := readOsuDBValue[int32](reader)
	if err != nil {
		return osuDatabaseEntry{}, false, err
	}
	if version <= 0 {
		return osuDatabaseEntry{}, false, fmt.Errorf("invalid database version %d", version)
	}
	if _, err := readOsuDBValue[int32](reader); err != nil {
		return osuDatabaseEntry{}, false, err
	}
	if _, err := readOsuDBValue[uint8](reader); err != nil {
		return osuDatabaseEntry{}, false, err
	}
	if _, err := readOsuDBValue[int64](reader); err != nil {
		return osuDatabaseEntry{}, false, err
	}
	if _, err := readOsuDBString(reader); err != nil {
		return osuDatabaseEntry{}, false, err
	}
	beatmapCount, err := readOsuDBValue[int32](reader)
	if err != nil {
		return osuDatabaseEntry{}, false, err
	}
	if beatmapCount < 0 || beatmapCount > 1_000_000 {
		return osuDatabaseEntry{}, false, fmt.Errorf("invalid beatmap count %d", beatmapCount)
	}

	for i := int32(0); i < beatmapCount; i++ {
		if version < 20191107 {
			if _, err := readOsuDBValue[int32](reader); err != nil {
				return osuDatabaseEntry{}, false, err
			}
		}
		entry, err := readOsuDatabaseMap(reader, version)
		if err != nil {
			return osuDatabaseEntry{}, false, fmt.Errorf("read beatmap entry %d: %w", i, err)
		}
		if strings.EqualFold(entry.Hash, expected) {
			return entry, true, nil
		}
	}
	return osuDatabaseEntry{}, false, nil
}

func readOsuDatabaseMap(reader *bytes.Reader, version int32) (osuDatabaseEntry, error) {
	var entry osuDatabaseEntry
	var stringsInEntry [9]string
	for i := range stringsInEntry {
		value, err := readOsuDBString(reader)
		if err != nil {
			return entry, err
		}
		stringsInEntry[i] = value
	}
	entry.Hash = stringsInEntry[7]
	entry.FileName = stringsInEntry[8]

	if err := skipOsuDBValues[uint8](reader, 1); err != nil {
		return entry, err
	}
	if err := skipOsuDBValues[int16](reader, 3); err != nil {
		return entry, err
	}
	if err := skipOsuDBValues[int64](reader, 1); err != nil {
		return entry, err
	}
	if version < 20140609 {
		if err := skipOsuDBValues[uint8](reader, 4); err != nil {
			return entry, err
		}
	} else if err := skipOsuDBValues[float32](reader, 4); err != nil {
		return entry, err
	}
	if err := skipOsuDBValues[float64](reader, 1); err != nil {
		return entry, err
	}

	if version >= 20140609 {
		for mode := 0; mode < 4; mode++ {
			ratingCount, err := readOsuDBValue[int32](reader)
			if err != nil {
				return entry, err
			}
			if ratingCount < 0 || ratingCount > 100_000 {
				return entry, fmt.Errorf("invalid star rating count %d", ratingCount)
			}
			for i := int32(0); i < ratingCount; i++ {
				if err := skipOsuDBValues[uint8](reader, 1); err != nil {
					return entry, err
				}
				mods, err := readOsuDBValue[int32](reader)
				if err != nil {
					return entry, err
				}
				if err := skipOsuDBValues[uint8](reader, 1); err != nil {
					return entry, err
				}
				var rating float64
				if version > 20250107 {
					value, err := readOsuDBValue[float32](reader)
					if err != nil {
						return entry, err
					}
					rating = float64(value)
				} else {
					rating, err = readOsuDBValue[float64](reader)
					if err != nil {
						return entry, err
					}
				}
				if mode == 3 && mods == 0 && !math.IsNaN(rating) && !math.IsInf(rating, 0) && rating > 0 {
					entry.ManiaStarRating = rating
				}
			}
		}
	}

	if err := skipOsuDBValues[int32](reader, 3); err != nil {
		return entry, err
	}
	timingPointCount, err := readOsuDBValue[int32](reader)
	if err != nil {
		return entry, err
	}
	if timingPointCount < 0 || timingPointCount > 1_000_000 {
		return entry, fmt.Errorf("invalid timing point count %d", timingPointCount)
	}
	if _, err := io.CopyN(io.Discard, reader, int64(timingPointCount)*17); err != nil {
		return entry, err
	}
	if err := skipOsuDBValues[int32](reader, 3); err != nil {
		return entry, err
	}
	if err := skipOsuDBValues[uint8](reader, 4); err != nil {
		return entry, err
	}
	if err := skipOsuDBValues[int16](reader, 1); err != nil {
		return entry, err
	}
	if err := skipOsuDBValues[float32](reader, 1); err != nil {
		return entry, err
	}
	if err := skipOsuDBValues[uint8](reader, 1); err != nil {
		return entry, err
	}
	if _, err := readOsuDBString(reader); err != nil {
		return entry, err
	}
	if _, err := readOsuDBString(reader); err != nil {
		return entry, err
	}
	if err := skipOsuDBValues[int16](reader, 1); err != nil {
		return entry, err
	}
	if _, err := readOsuDBString(reader); err != nil {
		return entry, err
	}
	if err := skipOsuDBValues[uint8](reader, 1); err != nil {
		return entry, err
	}
	if err := skipOsuDBValues[int64](reader, 1); err != nil {
		return entry, err
	}
	if err := skipOsuDBValues[uint8](reader, 1); err != nil {
		return entry, err
	}
	entry.FolderName, err = readOsuDBString(reader)
	if err != nil {
		return entry, err
	}
	if err := skipOsuDBValues[int64](reader, 1); err != nil {
		return entry, err
	}
	if err := skipOsuDBValues[uint8](reader, 5); err != nil {
		return entry, err
	}
	if version < 20140609 {
		if err := skipOsuDBValues[int16](reader, 1); err != nil {
			return entry, err
		}
	}
	if err := skipOsuDBValues[int32](reader, 1); err != nil {
		return entry, err
	}
	if err := skipOsuDBValues[uint8](reader, 1); err != nil {
		return entry, err
	}
	return entry, nil
}

func readOsuDBString(reader *bytes.Reader) (string, error) {
	marker, err := reader.ReadByte()
	if err != nil {
		return "", err
	}
	if marker == 0 {
		return "", nil
	}
	if marker != 0x0b {
		return "", fmt.Errorf("invalid string marker %#x", marker)
	}
	length, err := binary.ReadUvarint(reader)
	if err != nil {
		return "", err
	}
	if length > 16<<20 || length > uint64(reader.Len()) {
		return "", fmt.Errorf("invalid string length %d", length)
	}
	value := make([]byte, int(length))
	if _, err := io.ReadFull(reader, value); err != nil {
		return "", err
	}
	return string(value), nil
}

func readOsuDBValue[T any](reader io.Reader) (T, error) {
	var value T
	err := binary.Read(reader, binary.LittleEndian, &value)
	return value, err
}

func skipOsuDBValues[T any](reader io.Reader, count int) error {
	for i := 0; i < count; i++ {
		if _, err := readOsuDBValue[T](reader); err != nil {
			return err
		}
	}
	return nil
}
