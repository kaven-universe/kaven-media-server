package config

type HFSRoot struct {
	Name   string `json:"name"`
	Path   string `json:"path"`
	Public bool   `json:"public"`
	// ReadOnly disables mutations without changing how Path is resolved.
	ReadOnly bool `json:"readOnly,omitempty"`
}

func DefaultHFSRoots() []HFSRoot {
	return DefaultHFSRootsForUploadDirectory(DefaultUploadDirectory)
}

func DefaultHFSRootsForUploadDirectory(uploadDirectory string) []HFSRoot {
	return []HFSRoot{{Name: "uploaded", Path: uploadDirectory}}
}
