package transfer

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
)

type checkpointState struct {
	Version        int           `json:"version"`
	SourceKey      string        `json:"source_key"`
	SourceRevision string        `json:"source_revision,omitempty"`
	Size           int64         `json:"size"`
	SegmentSize    int64         `json:"segment_size"`
	Segments       map[int]int64 `json:"segments"`
}

type FileCheckpoint struct {
	mu             sync.Mutex
	path           string
	sourceKey      string
	sourceRevision string
	size           int64
	segmentSize    int64
	loaded         bool
	state          checkpointState
}

func NewFileCheckpoint(path, sourceKey, sourceRevision string, size, segmentSize int64) *FileCheckpoint {
	return &FileCheckpoint{path: path, sourceKey: sourceKey, sourceRevision: sourceRevision, size: size, segmentSize: segmentSize}
}

func SourceKey(accountID, sourceFileID string) string {
	digest := sha1.Sum([]byte(accountID + "\x00" + sourceFileID))
	return hex.EncodeToString(digest[:])
}

func (c *FileCheckpoint) Load() (map[int]int64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.loadLocked(); err != nil {
		return nil, err
	}
	return cloneSegments(c.state.Segments), nil
}

func (c *FileCheckpoint) Save(index int, size int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.loadLocked(); err != nil {
		return err
	}
	c.state.Segments[index] = size
	return c.persistLocked()
}

func (c *FileCheckpoint) Remove() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := os.Remove(c.path); err != nil && !os.IsNotExist(err) {
		return err
	}
	c.loaded = false
	c.state = checkpointState{}
	return nil
}

func (c *FileCheckpoint) loadLocked() error {
	if c.loaded {
		return nil
	}
	c.state = checkpointState{Version: ProtocolVersion, SourceKey: c.sourceKey, SourceRevision: c.sourceRevision, Size: c.size, SegmentSize: c.segmentSize, Segments: map[int]int64{}}
	file, err := os.Open(c.path)
	if os.IsNotExist(err) {
		c.loaded = true
		return nil
	}
	if err != nil {
		return err
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, 1<<20))
	var stored checkpointState
	if err := decoder.Decode(&stored); err != nil {
		return fmt.Errorf("decode transfer checkpoint: %w", err)
	}
	if stored.Version != ProtocolVersion || stored.SourceKey != c.sourceKey || stored.SourceRevision != c.sourceRevision || stored.Size != c.size || stored.SegmentSize != c.segmentSize || stored.Segments == nil {
		return fmt.Errorf("transfer checkpoint identity is ambiguous")
	}
	c.state = stored
	c.loaded = true
	return nil
}

func (c *FileCheckpoint) persistLocked() error {
	if err := os.MkdirAll(filepath.Dir(c.path), 0o700); err != nil {
		return err
	}
	temporary := c.path + ".tmp"
	if err := os.Remove(temporary); err != nil && !os.IsNotExist(err) {
		return err
	}
	file, err := os.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	encodeErr := json.NewEncoder(file).Encode(c.state)
	syncErr := file.Sync()
	closeErr := file.Close()
	if encodeErr != nil {
		_ = os.Remove(temporary)
		return encodeErr
	}
	if syncErr != nil {
		_ = os.Remove(temporary)
		return syncErr
	}
	if closeErr != nil {
		_ = os.Remove(temporary)
		return closeErr
	}
	if err := os.Rename(temporary, c.path); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	directory, err := os.Open(filepath.Dir(c.path))
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

func cloneSegments(source map[int]int64) map[int]int64 {
	result := make(map[int]int64, len(source))
	for index, size := range source {
		result[index] = size
	}
	return result
}
