package llm

import (
	"os"
	"path/filepath"
)

type diskCache struct {
	dir string
}

func newDiskCache(dir string) *diskCache {
	return &diskCache{dir: dir}
}

func (c *diskCache) pathForKey(keyHex string) string {
	if len(keyHex) < 4 {
		return filepath.Join(c.dir, keyHex)
	}
	return filepath.Join(c.dir, keyHex[:2], keyHex[2:])
}

func (c *diskCache) get(keyHex string) ([]byte, bool) {
	p := c.pathForKey(keyHex)
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, false
	}
	return b, true
}

func (c *diskCache) set(keyHex string, value []byte) error {
	p := c.pathForKey(keyHex)
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, value, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}
