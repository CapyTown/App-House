package history

import (
	"bufio"
	"bytes"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"
)

type Entry struct {
	Time    time.Time `json:"time"`
	Serial  string    `json:"serial"`
	UserID  int       `json:"userID"`
	Action  string    `json:"action"`
	Package string    `json:"package"`
	Message string    `json:"message"`
	OK      bool      `json:"ok"`
}

// Serialize this process's reads and appends, including torn-tail separation.
var fileMu sync.Mutex

func Load(path string) ([]Entry, error) {
	fileMu.Lock()
	defer fileMu.Unlock()
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open history: %w", err)
	}
	defer file.Close()
	var entries []Entry
	var badLines []int
	badCount := 0
	reader := bufio.NewReader(file)
	for lineNumber := 1; ; lineNumber++ {
		line, readErr := reader.ReadBytes('\n')
		if line = bytes.TrimSpace(line); len(line) > 0 {
			var entry Entry
			if line[0] != '{' || json.Unmarshal(line, &entry) != nil {
				badCount++
				if len(badLines) < 8 {
					badLines = append(badLines, lineNumber)
				}
			} else {
				entries = append(entries, entry)
			}
		}
		if readErr != nil {
			if !errors.Is(readErr, io.EOF) {
				err = fmt.Errorf("read history at line %d: %w", lineNumber, readErr)
			}
			break
		}
	}
	if badCount > 0 {
		err = errors.Join(err, fmt.Errorf("history contains %d malformed records; first affected lines: %v; recovered %d valid records", badCount, badLines, len(entries)))
	}
	slices.SortStableFunc(entries, func(a, b Entry) int { return a.Time.Compare(b.Time) })
	return entries, err
}

// Append preserves every existing byte, even an incomplete final record.
func Append(path string, entry Entry) error {
	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("encode history entry: %w", err)
	}
	fileMu.Lock()
	defer fileMu.Unlock()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create history directory: %w", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open history for append: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return fmt.Errorf("inspect history file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return errors.New("history must be a regular file")
	}
	if err := file.Chmod(0o600); err != nil {
		return fmt.Errorf("protect history file: %w", err)
	}
	if info.Size() > 0 {
		var last [1]byte
		if _, err := file.ReadAt(last[:], info.Size()-1); err != nil {
			return fmt.Errorf("read history tail: %w", err)
		}
		if last[0] != '\n' {
			data = append([]byte{'\n'}, data...)
		}
	}
	data = append(data, '\n')
	if n, err := file.Write(data); err != nil {
		return fmt.Errorf("append history: %w", err)
	} else if n != len(data) {
		return fmt.Errorf("append history: %w", io.ErrShortWrite)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("flush history: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close history: %w", err)
	}
	return nil
}
