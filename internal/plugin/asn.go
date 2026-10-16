package plugin

import (
	"archive/zip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"strconv"
	"sync"
)

// ASNIndex keeps only the ZIP directory and at most 64 decoded ASN entries.
// Entries are <decimal-ASN>.json with the upstream ipv4/ipv6 arrays.
type ASNIndex struct {
	mu    sync.Mutex
	path  string
	cache map[uint32][]string
}

func NewASNIndex(path string) *ASNIndex { return &ASNIndex{path: path, cache: map[uint32][]string{}} }
func (a *ASNIndex) Resolve(asn uint32) ([]string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if cached, ok := a.cache[asn]; ok {
		return append([]string(nil), cached...), nil
	}
	zr, err := zip.OpenReader(a.path)
	if err != nil {
		return nil, fmt.Errorf("ASN index unavailable: %w", err)
	}
	defer zr.Close()
	file, err := zr.Open(strconv.FormatUint(uint64(asn), 10) + ".json")
	if errors.Is(err, fs.ErrNotExist) {
		return []string{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("ASN %d unavailable: %w", asn, err)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 16*1024*1024+1))
	if err != nil {
		return nil, err
	}
	if len(data) > 16*1024*1024 {
		return nil, fmt.Errorf("oversized ASN entry")
	}
	var prefixes struct {
		IPv4 []string `json:"ipv4"`
		IPv6 []string `json:"ipv6"`
	}
	if err = json.Unmarshal(data, &prefixes); err != nil {
		return nil, err
	}
	result := append(prefixes.IPv4, prefixes.IPv6...)
	for _, p := range result {
		if _, _, err := validateAddress(p); err != nil {
			return nil, err
		}
	}
	if len(a.cache) >= 64 {
		a.cache = map[uint32][]string{}
	}
	a.cache[asn] = append([]string(nil), result...)
	return result, nil
}
