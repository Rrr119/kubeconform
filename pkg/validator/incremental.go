package validator

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"hash"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

const (
	defaultIncrementalCacheFile = ".kubeconform-cache.json"
	incrementalCacheVersion     = 1
)

type incrementalEntry struct {
	Status           Status            `json:"status"`
	Err              string            `json:"err,omitempty"`
	ValidationErrors []ValidationError `json:"validationErrors,omitempty"`
}

type incrementalState struct {
	Version int                         `json:"version"`
	Entries map[string]incrementalEntry `json:"entries"`
}

type incrementalCache struct {
	sync.RWMutex
	path    string
	entries map[string]incrementalEntry
	dirty   bool
}

func newIncrementalCache(path string) (*incrementalCache, error) {
	if path == "" {
		path = defaultIncrementalCacheFile
	}

	if fi, err := os.Stat(path); (err == nil && fi.IsDir()) || strings.HasSuffix(path, string(os.PathSeparator)) {
		path = filepath.Join(path, "validation-cache.json")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, err
	}

	c := &incrementalCache{
		path:    path,
		entries: map[string]incrementalEntry{},
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return c, nil
		}
		return nil, err
	}

	var state incrementalState
	if err := json.Unmarshal(data, &state); err == nil && state.Version == incrementalCacheVersion && state.Entries != nil {
		c.entries = state.Entries
	}

	return c, nil
}

func (c *incrementalCache) Get(key string) (incrementalEntry, bool) {
	c.RLock()
	defer c.RUnlock()

	entry, ok := c.entries[key]
	if !ok {
		return incrementalEntry{}, false
	}
	if entry.Status != Valid && entry.Status != Invalid {
		return incrementalEntry{}, false
	}
	entry.ValidationErrors = append([]ValidationError(nil), entry.ValidationErrors...)
	return entry, true
}

func (c *incrementalCache) Set(key string, entry incrementalEntry) {
	c.Lock()
	defer c.Unlock()

	c.entries[key] = entry
	c.dirty = true
}

func (c *incrementalCache) Flush() error {
	c.Lock()
	defer c.Unlock()

	if !c.dirty {
		return nil
	}

	data, err := json.MarshalIndent(incrementalState{
		Version: incrementalCacheVersion,
		Entries: c.entries,
	}, "", "  ")
	if err != nil {
		return fmt.Errorf("failed encoding incremental validation cache: %w", err)
	}

	tmp, err := os.CreateTemp(filepath.Dir(c.path), ".kubeconform-cache-*.tmp")
	if err != nil {
		return fmt.Errorf("failed creating incremental validation cache: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("failed writing incremental validation cache: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("failed writing incremental validation cache: %w", err)
	}
	if err := os.Rename(tmpName, c.path); err != nil {
		return fmt.Errorf("failed replacing incremental validation cache: %w", err)
	}

	c.dirty = false
	return nil
}

func incrementalContextHash(opts Opts, schemaLocations []string) string {
	h := sha256.New()
	fmt.Fprintf(h, "%d\n%s\n%t\n%t\n", incrementalCacheVersion, opts.KubernetesVersion, opts.Strict, opts.IgnoreMissingSchemas)
	for _, location := range schemaLocations {
		fmt.Fprintf(h, "schema:%s\n", location)
	}
	writeSortedKeys(h, opts.SkipKinds)
	writeSortedKeys(h, opts.RejectKinds)
	return fmt.Sprintf("%x", h.Sum(nil))
}

func writeSortedKeys(h hash.Hash, values map[string]struct{}) {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		fmt.Fprintf(h, "key:%s\n", key)
	}
}

func incrementalResourceKey(contextHash string, resourceBytes []byte) string {
	sum := sha256.Sum256(resourceBytes)
	return fmt.Sprintf("%s:%x", contextHash, sum)
}
