package rojo

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"sync"
)

const resolverCacheFormatVersion = 1

type resolverCacheManifestEntry struct {
	Path          string `json:"path"`
	MtimeUnixNano int64  `json:"mtimeUnixNano"`
}

type resolverCacheFile struct {
	Version         int                          `json:"version"`
	CompilerVersion string                       `json:"compilerVersion"`
	Key             string                       `json:"key"`
	State           ResolverState                `json:"state"`
	MtimeManifest   []resolverCacheManifestEntry `json:"mtimeManifest"`
}

type resolverCacheEntry struct {
	Resolver *RojoResolver
	Manifest []resolverCacheManifestEntry
}

// RojoResolverCache keeps parsed resolver states in memory and on disk.
type RojoResolverCache struct {
	cacheDir        string
	compilerVersion string
	deferPersist    bool

	mu      sync.Mutex
	l1      map[string]resolverCacheEntry
	pending map[string]resolverCacheEntry
}

// NewRojoResolverCache creates a cache whose disk entries are stored in cacheDir.
func NewRojoResolverCache(cacheDir, compilerVersion string) *RojoResolverCache {
	return NewRojoResolverCacheWithDeferredPersist(cacheDir, compilerVersion, false)
}

func NewRojoResolverCacheWithDeferredPersist(cacheDir, compilerVersion string, deferPersist bool) *RojoResolverCache {
	return &RojoResolverCache{
		cacheDir:        cacheDir,
		compilerVersion: compilerVersion,
		deferPersist:    deferPersist,
		l1:              make(map[string]resolverCacheEntry),
		pending:         map[string]resolverCacheEntry{},
	}
}

// Load returns a resolver for rojoConfigFilePath, preferring valid L1 then L2 state.
func (c *RojoResolverCache) Load(rojoConfigFilePath string) *RojoResolver {
	key := cacheKey(rojoConfigFilePath)
	c.mu.Lock()
	defer c.mu.Unlock()

	if entry, ok := c.l1[key]; ok && manifestStillValid(entry.Manifest) {
		return entry.Resolver
	}
	delete(c.l1, key)

	if resolver, manifest, ok := c.loadDisk(key); ok {
		c.l1[key] = resolverCacheEntry{Resolver: resolver, Manifest: manifest}
		return resolver
	}

	resolver := FromPath(key)
	manifest, ok := resolverMtimeManifest(resolver.GetState())
	if !ok {
		return resolver
	}
	c.l1[key] = resolverCacheEntry{Resolver: resolver, Manifest: manifest}
	entry := c.l1[key]
	if c.deferPersist {
		c.pending[key] = entry
	} else {
		c.writeDisk(key, resolver.GetState(), manifest)
	}
	return resolver
}

func (c *RojoResolverCache) Persist() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for key, entry := range c.pending {
		c.writeDisk(key, entry.Resolver.GetState(), entry.Manifest)
	}
	c.pending = map[string]resolverCacheEntry{}
}

func (c *RojoResolverCache) loadDisk(key string) (*RojoResolver, []resolverCacheManifestEntry, bool) {
	path := c.cachePath(key)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, false
	}

	var file resolverCacheFile
	if err := json.Unmarshal(data, &file); err != nil {
		if !c.deferPersist {
			_ = os.Remove(path)
		}
		return nil, nil, false
	}
	if file.Version != resolverCacheFormatVersion || file.CompilerVersion != c.compilerVersion || file.Key != key ||
		!stateMatchesManifest(file.State, file.MtimeManifest) {
		if !c.deferPersist {
			_ = os.Remove(path)
		}
		return nil, nil, false
	}
	if !manifestStillValid(file.MtimeManifest) {
		return nil, nil, false
	}
	resolver, err := FromState(file.State)
	if err != nil {
		if !c.deferPersist {
			_ = os.Remove(path)
		}
		return nil, nil, false
	}
	return resolver, slices.Clone(file.MtimeManifest), true
}

func (c *RojoResolverCache) writeDisk(key string, state ResolverState, manifest []resolverCacheManifestEntry) {
	data, err := json.MarshalIndent(resolverCacheFile{
		Version:         resolverCacheFormatVersion,
		CompilerVersion: c.compilerVersion,
		Key:             key,
		State:           state,
		MtimeManifest:   manifest,
	}, "", "\t")
	if err != nil {
		return
	}
	data = append(data, '\n')
	_ = writeResolverCacheFileAtomic(c.cachePath(key), data)
}

func (c *RojoResolverCache) cachePath(key string) string {
	sum := sha256.Sum256([]byte(key))
	return filepath.Join(c.cacheDir, hex.EncodeToString(sum[:])+".rojocache.json")
}

func cacheKey(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}
	return filepath.Clean(abs)
}

func resolverMtimeManifest(state ResolverState) ([]resolverCacheManifestEntry, bool) {
	paths := append(slices.Clone(state.WalkedConfigs), state.WalkedDirectories...)
	paths = sortedStrings(paths)
	if len(paths) == 0 {
		return nil, false
	}
	manifest := make([]resolverCacheManifestEntry, 0, len(paths))
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil {
			return nil, false
		}
		manifest = append(manifest, resolverCacheManifestEntry{
			Path:          path,
			MtimeUnixNano: info.ModTime().UnixNano(),
		})
	}
	return manifest, true
}

func manifestStillValid(manifest []resolverCacheManifestEntry) bool {
	if len(manifest) == 0 {
		return false
	}
	for _, entry := range manifest {
		info, err := os.Stat(entry.Path)
		if err != nil || info.ModTime().UnixNano() != entry.MtimeUnixNano {
			return false
		}
	}
	return true
}

func stateMatchesManifest(state ResolverState, manifest []resolverCacheManifestEntry) bool {
	expected, ok := resolverMtimeManifest(state)
	return ok && slices.Equal(expected, manifest)
}

func writeResolverCacheFileAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".rojo-cache-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	_, writeErr := tmp.Write(data)
	closeErr := tmp.Close()
	if writeErr != nil || closeErr != nil {
		_ = os.Remove(tmpName)
		if writeErr != nil {
			return writeErr
		}
		return closeErr
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return nil
}
