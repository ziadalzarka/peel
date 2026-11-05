package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"time"
)

type Jump struct {
	File string    `json:"file"`
	Line int       `json:"line,omitempty"`
	At   time.Time `json:"at"`
}

func WriteJump(path string, j Jump) error {
	return writeJSONFile(path, "jump", j)
}

func ReadJump(path string) (Jump, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Jump{}, nil
	}
	if err != nil {
		return Jump{}, fmt.Errorf("read %s: %w", path, err)
	}
	var j Jump
	if err := json.Unmarshal(b, &j); err != nil {
		return Jump{}, fmt.Errorf("parse %s: %w", path, err)
	}
	return j, nil
}
