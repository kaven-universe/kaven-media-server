package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

const (
	HFSRootsEnvironment = "KAVEN_HFS_ROOTS"
	maxHFSRootsJSONSize = 64 * 1024
	maxHFSRootCount     = 64
)

type HFSRoot struct {
	Name   string `json:"name"`
	Path   string `json:"path"`
	Public bool   `json:"public"`
	// ReadOnly permits an absolute external root and disables mutations.
	ReadOnly bool `json:"readOnly,omitempty"`
}

func DefaultHFSRoots() []HFSRoot {
	return []HFSRoot{{Name: "uploaded", Path: "hfs/uploaded"}}
}

func HFSRootsFromEnvironment() ([]HFSRoot, error) {
	return LoadHFSRoots(os.LookupEnv)
}

func LoadHFSRoots(lookup EnvironmentLookup) ([]HFSRoot, error) {
	value, configured := lookup(HFSRootsEnvironment)
	if !configured {
		return DefaultHFSRoots(), nil
	}
	if len(value) == 0 || len(value) > maxHFSRootsJSONSize {
		return nil, fmt.Errorf("%s must contain 1-%d bytes", HFSRootsEnvironment, maxHFSRootsJSONSize)
	}

	decoder := json.NewDecoder(strings.NewReader(value))
	decoder.DisallowUnknownFields()
	var roots []HFSRoot
	if err := decoder.Decode(&roots); err != nil {
		return nil, fmt.Errorf("parse %s: %w", HFSRootsEnvironment, err)
	}
	if err := ensureJSONEnd(decoder); err != nil {
		return nil, fmt.Errorf("parse %s: %w", HFSRootsEnvironment, err)
	}
	if roots == nil {
		return nil, fmt.Errorf("%s must be a JSON array", HFSRootsEnvironment)
	}
	if len(roots) > maxHFSRootCount {
		return nil, fmt.Errorf("%s supports at most %d roots", HFSRootsEnvironment, maxHFSRootCount)
	}
	return roots, nil
}

func ensureJSONEnd(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); errors.Is(err, io.EOF) {
		return nil
	} else if err != nil {
		return err
	}
	return errors.New("unexpected data after JSON value")
}
