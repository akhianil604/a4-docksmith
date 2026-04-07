package imagestore

// ManifestConfig stores runtime configuration persisted with an image.
type ManifestConfig struct {
	Env        []string `json:"Env"`
	Cmd        []string `json:"Cmd"`
	WorkingDir string   `json:"WorkingDir"`
}

// LayerEntry represents one immutable tar layer referenced by a manifest.
type LayerEntry struct {
	Digest    string `json:"digest"`
	Size      int64  `json:"size"`
	CreatedBy string `json:"createdBy"`
}

// Manifest is the on-disk image record stored in ~/.docksmith/images.
type Manifest struct {
	Name    string         `json:"name"`
	Tag     string         `json:"tag"`
	Digest  string         `json:"digest"`
	Created string         `json:"created"`
	Config  ManifestConfig `json:"config"`
	Layers  []LayerEntry   `json:"layers"`
}
