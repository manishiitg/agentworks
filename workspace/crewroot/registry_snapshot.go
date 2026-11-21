package crewroot

import (
	"encoding/json"
	"os"
	"reflect"
	"sync"
)

// One immutable registry snapshot, not resolved roots or access decisions.
// Both services use this path resolver, including for non-Crew documents.
// Re-decoding every project's record on every path produced gigabytes of
// allocation per minute (#2989). All callers of records only read this map.
var registrySnapshot struct {
	sync.Mutex
	path    string
	stamp   os.FileInfo
	records map[string]Record
}

func registryChangeTime(info os.FileInfo) ([2]int64, bool) {
	if info == nil {
		return [2]int64{}, false
	}
	value := reflect.ValueOf(info.Sys())
	if value.Kind() == reflect.Pointer {
		value = value.Elem()
	}
	if !value.IsValid() || value.Kind() != reflect.Struct {
		return [2]int64{}, false
	}
	for _, name := range []string{"Ctim", "Ctimespec"} {
		field := value.FieldByName(name)
		if field.IsValid() && field.Kind() == reflect.Struct {
			sec, nsec := field.FieldByName("Sec"), field.FieldByName("Nsec")
			if sec.IsValid() && nsec.IsValid() && sec.CanInt() && nsec.CanInt() {
				return [2]int64{sec.Int(), nsec.Int()}, true
			}
		}
	}
	return [2]int64{}, false
}

func sameRegistryFile(a, b os.FileInfo) bool {
	ac, aok := registryChangeTime(a)
	bc, bok := registryChangeTime(b)
	return aok && bok && ac == bc && os.SameFile(a, b) && a.Size() == b.Size() && a.Mode() == b.Mode() && a.ModTime().Equal(b.ModTime())
}

func registryRecords(path string) (map[string]Record, error) {
	registrySnapshot.Lock()
	defer registrySnapshot.Unlock()
	before, err := os.Stat(path)
	if err != nil {
		registrySnapshot.path, registrySnapshot.stamp, registrySnapshot.records = "", nil, nil
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	if registrySnapshot.path == path && sameRegistryFile(registrySnapshot.stamp, before) {
		return registrySnapshot.records, nil
	}
	// Never fall back to a previous successful snapshot on a failed read/decode.
	registrySnapshot.path, registrySnapshot.stamp, registrySnapshot.records = "", nil, nil
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var doc struct {
		Projects map[string]Record `json:"projects"`
	}
	if err = json.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	after, err := os.Stat(path)
	if err == nil && sameRegistryFile(before, after) {
		registrySnapshot.path, registrySnapshot.stamp, registrySnapshot.records = path, after, doc.Projects
	}
	return doc.Projects, nil
}
