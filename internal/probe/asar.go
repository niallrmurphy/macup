package probe

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strconv"
)

// asarFile reads one top-level file out of an Electron asar archive without
// extracting anything else. The header is a Chromium pickle: a uint32 payload
// size, a uint32 header size, a uint32 string size, then the JSON directory.
func asarFile(path, name string, max int) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var head [16]byte
	if _, err = io.ReadFull(f, head[:]); err != nil {
		return nil, err
	}
	headerSize := binary.LittleEndian.Uint32(head[4:8])
	jsonLen := binary.LittleEndian.Uint32(head[12:16])
	if jsonLen == 0 || jsonLen > 8<<20 || uint64(jsonLen)+8 > uint64(headerSize)+8 {
		return nil, errors.New("asar header out of range")
	}
	dir := make([]byte, jsonLen)
	if _, err = io.ReadFull(f, dir); err != nil {
		return nil, err
	}
	var index struct {
		Files map[string]json.RawMessage `json:"files"`
	}
	if err = json.Unmarshal(dir, &index); err != nil {
		return nil, err
	}
	raw, ok := index.Files[name]
	if !ok {
		return nil, os.ErrNotExist
	}
	var entry struct {
		Size   int64  `json:"size"`
		Offset string `json:"offset"`
	}
	if err = json.Unmarshal(raw, &entry); err != nil {
		return nil, err
	}
	off, err := strconv.ParseInt(entry.Offset, 10, 64)
	if err != nil || entry.Size < 0 || entry.Size > int64(max) {
		return nil, errors.New("asar entry out of range")
	}
	base := int64(8) + int64(headerSize)
	buf := make([]byte, entry.Size)
	if _, err = f.ReadAt(buf, base+off); err != nil {
		return nil, err
	}
	return buf, nil
}
