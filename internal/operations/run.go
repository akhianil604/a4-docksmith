package operations

import (
	"fmt"
	"os"
	"sort"

	"docksmith/imagestore"
	"docksmith/isolation"
	"docksmith/layers"
)

type RunOpts struct {
	Reference string
	Cmd       []string
	Env       map[string]string
}

func Run(opts *RunOpts) error {
	if opts == nil {
		return fmt.Errorf("run options cannot be nil")
	}
	imagesPath, err := imagestore.DefaultImagesPath()
	if err != nil {
		return err
	}
	manifest, _, err := imagestore.LoadManifest(imagesPath, opts.Reference)
	if err != nil {
		return err
	}

	cmd := opts.Cmd
	if len(cmd) == 0 {
		cmd = manifest.Config.Cmd
	}
	if len(cmd) == 0 {
		return fmt.Errorf("no CMD defined in image %s and no command override provided", opts.Reference)
	}

	storePath, err := layers.DefaultStorePath()
	if err != nil {
		return err
	}
	rootfs, err := os.MkdirTemp("", "docksmith-run-rootfs-*")
	if err != nil {
		return fmt.Errorf("failed to create run rootfs: %w", err)
	}
	defer os.RemoveAll(rootfs)

	for _, layer := range manifest.Layers {
		if err := layers.ExtractLayer(layer.Digest, storePath, rootfs); err != nil {
			return err
		}
	}

	env := envMapFromPairs(manifest.Config.Env)
	for k, v := range opts.Env {
		env[k] = v
	}

	exitCode, err := isolation.Execute(isolation.Spec{
		RootFS:     rootfs,
		WorkingDir: defaultWorkingDir(manifest.Config.WorkingDir),
		Env:        envPairsFromMap(env),
		Cmd:        cmd,
	})
	if err != nil {
		return err
	}
	fmt.Printf("EXIT CODE: %d\n", exitCode)
	if exitCode != 0 {
		return fmt.Errorf("container exited with code %d", exitCode)
	}

	return nil
}

func envPairsFromMap(env map[string]string) []string {
	if len(env) == 0 {
		return nil
	}
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, k+"="+env[k])
	}
	return out
}
