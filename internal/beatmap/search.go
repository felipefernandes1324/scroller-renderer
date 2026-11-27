package beatmap

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

func FindByMD5(root, expected string) (string, error) {
	return FindByMD5Progress(root, expected, nil)
}

func FindByMD5Progress(root, expected string, progress func(int)) (string, error) {
	path, _, err := FindByMD5ProgressWithRating(root, expected, progress)
	return path, err
}

func FindByMD5ProgressWithRating(root, expected string, progress func(int)) (string, float64, error) {
	info, err := os.Stat(root)
	if err != nil {
		return "", 0, fmt.Errorf("osu! songs folder: %w", err)
	}
	if !info.IsDir() {
		return "", 0, fmt.Errorf("osu! songs path %q is not a directory", root)
	}
	expected = strings.ToLower(expected)
	databaseEntry, hasDatabaseEntry, databaseErr := findOsuDatabaseEntry(root, expected)
	if databaseErr == nil && hasDatabaseEntry {
		databasePath := databaseEntryPath(root, databaseEntry)
		if databasePath != "" {
			digest, hashErr := hashFile(databasePath, md5.New())
			if hashErr != nil && !os.IsNotExist(hashErr) {
				return "", 0, fmt.Errorf("hash beatmap %s: %w", databasePath, hashErr)
			}
			if hashErr == nil && hex.EncodeToString(digest) == expected {
				return databasePath, databaseEntry.ManiaStarRating, nil
			}
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	paths := make(chan string, 32)
	results := make(chan fileHash, 32)
	walkErrors := make(chan error, 1)

	workers := runtime.GOMAXPROCS(0)
	if workers > 8 {
		workers = 8
	}
	if workers < 1 {
		workers = 1
	}

	go func() {
		err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".osu") {
				return nil
			}
			select {
			case paths <- path:
				return nil
			case <-ctx.Done():
				return filepath.SkipAll
			}
		})
		close(paths)
		walkErrors <- err
	}()

	var workerGroup sync.WaitGroup
	workerGroup.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer workerGroup.Done()
			for path := range paths {
				digest, hashErr := hashFile(path, md5.New())
				select {
				case results <- fileHash{path: path, digest: hex.EncodeToString(digest), err: hashErr}:
				case <-ctx.Done():
					return
				}
			}
		}()
	}
	go func() {
		workerGroup.Wait()
		close(results)
	}()

	scanned := 0
	var match string
	var searchErr error
	for result := range results {
		if result.err != nil {
			searchErr = fmt.Errorf("hash beatmap %s: %w", result.path, result.err)
			cancel()
			continue
		}
		scanned++
		if result.digest == expected {
			match = result.path
			cancel()
			continue
		}
		if progress != nil && (scanned == 1 || scanned%10 == 0) {
			progress(scanned)
		}
	}
	walkErr := <-walkErrors
	if searchErr != nil {
		return "", 0, searchErr
	}
	if walkErr != nil {
		return "", 0, fmt.Errorf("search osu! songs folder: %w", walkErr)
	}
	if match != "" {
		if hasDatabaseEntry {
			return match, databaseEntry.ManiaStarRating, nil
		}
		return match, 0, nil
	}
	if progress != nil && scanned > 0 {
		progress(scanned)
	}
	if databaseErr != nil {
		return "", 0, fmt.Errorf("beatmap with MD5 %s was not found after checking %d beatmaps in %s; osu! database lookup failed: %w", expected, scanned, root, databaseErr)
	}
	return "", 0, fmt.Errorf("beatmap with MD5 %s was not found after checking %d beatmaps in %s", expected, scanned, root)
}

func findOsuDatabaseEntry(root, expected string) (osuDatabaseEntry, bool, error) {
	databasePath := filepath.Join(filepath.Dir(root), "osu!.db")
	data, err := os.ReadFile(databasePath)
	if err != nil {
		if os.IsNotExist(err) {
			return osuDatabaseEntry{}, false, nil
		}
		return osuDatabaseEntry{}, false, fmt.Errorf("read osu! beatmap database: %w", err)
	}
	entry, found, err := parseOsuDatabase(data, expected)
	if err != nil {
		return osuDatabaseEntry{}, false, fmt.Errorf("parse osu! beatmap database: %w", err)
	}
	return entry, found, nil
}

func databaseEntryPath(root string, entry osuDatabaseEntry) string {
	filename := strings.TrimSpace(entry.FileName)
	folder := strings.Trim(entry.FolderName, `/\`)
	if filename == "" || strings.ContainsAny(filename, `/\`) || !strings.EqualFold(filepath.Ext(filename), ".osu") {
		return ""
	}
	if strings.ContainsAny(folder, `/\`) || folder == "." || folder == ".." {
		return ""
	}
	return filepath.Join(root, folder, filename)
}

type fileHash struct {
	path   string
	digest string
	err    error
}

func hashFile(path string, h hash.Hash) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	if _, err := io.Copy(h, file); err != nil {
		return nil, err
	}
	return h.Sum(nil), nil
}
