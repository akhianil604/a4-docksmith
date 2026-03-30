package image

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type Layer struct {
	Digest    string `json:"digest"`
	Size      int64  `json:"size"`
	CreatedBy string `json:"createdBy"`
}

type Config struct {
	Env        []string `json:"Env,omitempty"`
	Cmd        []string `json:"Cmd,omitempty"`
	WorkingDir string   `json:"WorkingDir,omitempty"`
}

type Manifest struct {
	Name    string  `json:"name"`
	Tag     string  `json:"tag"`
	Digest  string  `json:"digest"`
	Created string  `json:"created"`
	Config  Config  `json:"config"`
	Layers  []Layer `json:"layers"`
}

type Store struct {
	dir string
}

func NewStore(dir string) Store {
	return Store{dir: dir}
}

func (s Store) List() ([]Manifest, error) {
	entries, err := os.ReadDir(s.dir)
	if os.IsNotExist(err) {
		return []Manifest{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read images directory: %w", err)
	}

	manifests := make([]Manifest, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		manifest, err := s.readFile(filepath.Join(s.dir, entry.Name()))
		if err != nil {
			return nil, err
		}
		manifests = append(manifests, manifest)
	}
	return manifests, nil
}

func (s Store) Load(name, tag string) (Manifest, string, error) {
	entries, err := os.ReadDir(s.dir)
	if os.IsNotExist(err) {
		return Manifest{}, "", fmt.Errorf("image %s:%s not found", name, tag)
	}
	if err != nil {
		return Manifest{}, "", fmt.Errorf("read images directory: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		path := filepath.Join(s.dir, entry.Name())
		manifest, err := s.readFile(path)
		if err != nil {
			return Manifest{}, "", err
		}
		if manifest.Name == name && manifest.Tag == tag {
			return manifest, path, nil
		}
	}

	return Manifest{}, "", fmt.Errorf("image %s:%s not found", name, tag)
}

func (s Store) readFile(path string) (Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Manifest{}, err
	}
	var manifest Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return Manifest{}, fmt.Errorf("parse manifest %s: %w", path, err)
	}
	return manifest, nil
}
