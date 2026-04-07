package operations

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"docksmith/cache"
	"docksmith/imagestore"
	"docksmith/isolation"
	"docksmith/layers"
)

type BuildOpts struct {
	Tag     string
	Context string
	NoCache bool
}

type buildInstruction struct {
	Op  string
	Raw string
	Arg string
}

type fsSnapshot struct {
	files map[string]string
	dirs  map[string]struct{}
}

// Build executes a Docksmithfile from a context directory and writes an image manifest.
func Build(opts *BuildOpts) error {
	if opts == nil {
		return fmt.Errorf("build options cannot be nil")
	}
	if strings.TrimSpace(opts.Tag) == "" {
		return fmt.Errorf("build tag is required")
	}
	contextDir := strings.TrimSpace(opts.Context)
	if contextDir == "" {
		contextDir = "."
	}
	contextAbs, err := filepath.Abs(contextDir)
	if err != nil {
		return fmt.Errorf("failed to resolve context path: %w", err)
	}
	if st, err := os.Stat(contextAbs); err != nil || !st.IsDir() {
		return fmt.Errorf("build context %s is not a directory", contextAbs)
	}

	docksmithfile := filepath.Join(contextAbs, "Docksmithfile")
	instructions, err := parseDocksmithfile(docksmithfile)
	if err != nil {
		return err
	}

	name, tag, err := imagestore.ParseReference(opts.Tag)
	if err != nil {
		return err
	}

	storePath, err := layers.DefaultStorePath()
	if err != nil {
		return err
	}
	if err := layers.EnsureStoreExists(storePath); err != nil {
		return err
	}
	imagesPath, err := imagestore.DefaultImagesPath()
	if err != nil {
		return err
	}
	if err := imagestore.EnsureImagesPath(imagesPath); err != nil {
		return err
	}
	cacheEngine, err := cache.NewEngine(opts.NoCache)
	if err != nil {
		return err
	}

	startTotal := time.Now()
	rootfs, err := os.MkdirTemp("", "docksmith-build-rootfs-*")
	if err != nil {
		return fmt.Errorf("failed to create build rootfs: %w", err)
	}
	defer os.RemoveAll(rootfs)

	b := &builderState{
		contextDir: contextAbs,
		rootfs:     rootfs,
		storePath:  storePath,
		cache:      cacheEngine,
		env:        map[string]string{},
	}

	for i, inst := range instructions {
		stepStart := time.Now()
		if err := b.executeInstruction(i+1, inst, imagesPath); err != nil {
			return err
		}
		if !isLayerProducing(inst.Op) {
			fmt.Printf("STEP %02d %-7s [OK] %s\n", i+1, inst.Op, time.Since(stepStart).Round(time.Millisecond))
		}
	}

	manifest := imagestore.Manifest{
		Name:    name,
		Tag:     tag,
		Created: time.Now().UTC().Format(time.RFC3339),
		Config: imagestore.ManifestConfig{
			Env:        b.sortedEnvPairs(),
			Cmd:        append([]string(nil), b.cmd...),
			WorkingDir: b.workDir,
		},
		Layers: append([]imagestore.LayerEntry(nil), b.manifestLayers...),
	}
	finalManifest, err := imagestore.SaveManifest(imagesPath, manifest)
	if err != nil {
		return err
	}

	fmt.Printf("BUILT %s:%s %s in %s\n", name, tag, finalManifest.Digest, time.Since(startTotal).Round(time.Millisecond))
	return nil
}

type builderState struct {
	contextDir      string
	rootfs          string
	storePath       string
	cache           *cache.Engine
	manifestLayers  []imagestore.LayerEntry
	prevLayerDigest string
	workDir         string
	env             map[string]string
	cmd             []string
	forceMiss       bool
}

// executeInstruction routes a parsed instruction to its dedicated handler.
func (b *builderState) executeInstruction(step int, inst buildInstruction, imagesPath string) error {
	switch inst.Op {
	case "FROM":
		return b.handleFrom(inst, imagesPath)
	case "WORKDIR":
		return b.handleWorkdir(inst)
	case "ENV":
		return b.handleEnv(inst)
	case "CMD":
		return b.handleCmd(inst)
	case "COPY":
		return b.handleCopyOrRun(step, inst)
	case "RUN":
		return b.handleCopyOrRun(step, inst)
	default:
		return fmt.Errorf("unsupported instruction %q", inst.Op)
	}
}

// handleFrom loads a base image manifest and materializes its layers into the build rootfs.
func (b *builderState) handleFrom(inst buildInstruction, imagesPath string) error {
	if len(b.manifestLayers) > 0 || b.prevLayerDigest != "" {
		return fmt.Errorf("FROM must appear before other layer-producing state")
	}
	base, _, err := imagestore.LoadManifest(imagesPath, strings.TrimSpace(inst.Arg))
	if err != nil {
		return fmt.Errorf("FROM %s failed: %w", inst.Arg, err)
	}
	for _, layer := range base.Layers {
		if err := layers.ExtractLayer(layer.Digest, b.storePath, b.rootfs); err != nil {
			return fmt.Errorf("failed to extract base layer %s: %w", layer.Digest, err)
		}
		b.manifestLayers = append(b.manifestLayers, imagestore.LayerEntry(layer))
	}
	b.prevLayerDigest = base.Digest
	b.workDir = base.Config.WorkingDir
	b.cmd = append([]string(nil), base.Config.Cmd...)
	b.env = envMapFromPairs(base.Config.Env)
	return nil
}

// handleWorkdir updates the active working directory and ensures it exists in rootfs.
func (b *builderState) handleWorkdir(inst buildInstruction) error {
	arg := strings.TrimSpace(inst.Arg)
	if arg == "" {
		return fmt.Errorf("WORKDIR requires a path")
	}
	if filepath.IsAbs(arg) {
		b.workDir = filepath.Clean(arg)
	} else if b.workDir == "" {
		b.workDir = filepath.Clean("/" + arg)
	} else {
		b.workDir = filepath.Clean(filepath.Join(b.workDir, arg))
	}
	return os.MkdirAll(filepath.Join(b.rootfs, trimLeadingSlash(b.workDir)), 0755)
}

// handleEnv applies one or more KEY=VALUE pairs to the accumulated build environment.
func (b *builderState) handleEnv(inst buildInstruction) error {
	parts := strings.Fields(inst.Arg)
	if len(parts) == 0 {
		return fmt.Errorf("ENV requires KEY=VALUE")
	}
	for _, part := range parts {
		k, v, ok := strings.Cut(part, "=")
		if !ok || strings.TrimSpace(k) == "" {
			return fmt.Errorf("invalid ENV entry %q", part)
		}
		b.env[k] = v
	}
	return nil
}

// handleCmd stores the image default command from shell form or simple JSON-array form.
func (b *builderState) handleCmd(inst buildInstruction) error {
	arg := strings.TrimSpace(inst.Arg)
	if arg == "" {
		return fmt.Errorf("CMD requires a value")
	}
	if strings.HasPrefix(arg, "[") && strings.HasSuffix(arg, "]") {
		trimmed := strings.Trim(arg, "[]")
		parts := strings.Split(trimmed, ",")
		cmd := make([]string, 0, len(parts))
		for _, p := range parts {
			p = strings.TrimSpace(p)
			p = strings.Trim(p, "\"")
			if p != "" {
				cmd = append(cmd, p)
			}
		}
		if len(cmd) == 0 {
			return fmt.Errorf("CMD array is empty")
		}
		b.cmd = cmd
		return nil
	}
	b.cmd = strings.Fields(arg)
	if len(b.cmd) == 0 {
		return fmt.Errorf("CMD requires at least one token")
	}
	return nil
}

// handleCopyOrRun performs cache lookup, executes COPY/RUN on miss, and records a layer.
func (b *builderState) handleCopyOrRun(step int, inst buildInstruction) error {
	copyFiles := []string{}
	if inst.Op == "COPY" {
		var err error
		copyFiles, err = b.copySourceFiles(inst.Arg)
		if err != nil {
			return err
		}
	}

	state := cache.BuildState{
		PrevLayerDigest: b.prevLayerDigest,
		WorkDir:         b.workDir,
		Env:             copyEnvMap(b.env),
	}
	key, err := cache.ComputeCacheKey(cache.Instruction{Type: inst.Op, Raw: inst.Raw}, state, copyFiles)
	if err != nil {
		return fmt.Errorf("failed to compute cache key for %s: %w", inst.Raw, err)
	}

	start := time.Now()
	if !b.forceMiss {
		if digest, ok := b.cache.Lookup(key); ok && layers.LayerExists(digest, b.storePath) {
			if err := layers.ExtractLayer(digest, b.storePath, b.rootfs); err != nil {
				return err
			}
			info, err := layers.GetLayerInfo(digest, b.storePath)
			if err != nil {
				return err
			}
			b.manifestLayers = append(b.manifestLayers, imagestore.LayerEntry{
				Digest:    digest,
				Size:      info.Size,
				CreatedBy: inst.Raw,
			})
			b.prevLayerDigest = digest
			fmt.Printf("STEP %02d %-7s [CACHE HIT] %s\n", step, inst.Op, time.Since(start).Round(time.Millisecond))
			return nil
		}
	}

	b.forceMiss = true
	before, err := snapshotRootFS(b.rootfs)
	if err != nil {
		return err
	}

	if inst.Op == "COPY" {
		if err := b.applyCopy(inst.Arg); err != nil {
			return err
		}
	} else {
		if err := b.applyRun(inst.Arg); err != nil {
			return err
		}
	}

	after, err := snapshotRootFS(b.rootfs)
	if err != nil {
		return err
	}
	deltaDir, err := os.MkdirTemp("", "docksmith-build-delta-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(deltaDir)

	if err := writeDelta(before, after, b.rootfs, deltaDir); err != nil {
		return err
	}

	meta, err := layers.CreateLayer(deltaDir, b.storePath, inst.Raw)
	if err != nil {
		return err
	}
	if err := b.cache.Store(key, meta.Digest); err != nil {
		return err
	}
	b.manifestLayers = append(b.manifestLayers, imagestore.LayerEntry{
		Digest:    meta.Digest,
		Size:      meta.Size,
		CreatedBy: inst.Raw,
	})
	b.prevLayerDigest = meta.Digest
	fmt.Printf("STEP %02d %-7s [CACHE MISS] %s\n", step, inst.Op, time.Since(start).Round(time.Millisecond))
	return nil
}

// copySourceFiles expands the COPY source argument into a deterministic list of files for cache hashing.
func (b *builderState) copySourceFiles(arg string) ([]string, error) {
	parts := strings.Fields(arg)
	if len(parts) != 2 {
		return nil, fmt.Errorf("COPY supports exactly two arguments: COPY <src> <dest>")
	}
	return expandCopySources(filepath.Join(b.contextDir, parts[0]))
}

// applyCopy applies a COPY instruction from context into the build rootfs.
func (b *builderState) applyCopy(arg string) error {
	parts := strings.Fields(arg)
	if len(parts) != 2 {
		return fmt.Errorf("COPY supports exactly two arguments: COPY <src> <dest>")
	}
	srcPath := filepath.Join(b.contextDir, parts[0])
	if _, err := os.Stat(srcPath); err != nil {
		return fmt.Errorf("COPY source not found: %s", parts[0])
	}

	destSpec := parts[1]
	destAbs := resolveContainerPath(b.rootfs, b.workDir, destSpec)
	st, err := os.Stat(srcPath)
	if err != nil {
		return err
	}

	if st.IsDir() {
		if err := copyDirectoryContents(srcPath, destAbs); err != nil {
			return err
		}
		return nil
	}

	if strings.HasSuffix(destSpec, "/") {
		destAbs = filepath.Join(destAbs, filepath.Base(srcPath))
	}
	return copyPath(srcPath, destAbs)
}

// applyRun executes a RUN command in the build rootfs using the shared isolation primitive.
func (b *builderState) applyRun(arg string) error {
	command := strings.TrimSpace(arg)
	if command == "" {
		return fmt.Errorf("RUN requires a command")
	}

	exitCode, err := isolation.Execute(isolation.Spec{
		RootFS:     b.rootfs,
		WorkingDir: defaultWorkingDir(b.workDir),
		Env:        append([]string(nil), b.sortedEnvPairs()...),
		Cmd:        []string{"/bin/sh", "-c", command},
	})
	if err != nil {
		return fmt.Errorf("RUN isolation failed: %w", err)
	}
	if exitCode != 0 {
		return fmt.Errorf("RUN failed with exit code %d", exitCode)
	}
	return nil
}

// parseDocksmithfile parses supported instructions from Docksmithfile in source order.
func parseDocksmithfile(path string) ([]buildInstruction, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read Docksmithfile: %w", err)
	}
	lines := strings.Split(string(data), "\n")
	out := make([]buildInstruction, 0, len(lines))
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		op, arg, found := strings.Cut(trimmed, " ")
		if !found {
			return nil, fmt.Errorf("invalid instruction at line %d: %q", i+1, line)
		}
		op = strings.ToUpper(strings.TrimSpace(op))
		arg = strings.TrimSpace(arg)
		switch op {
		case "FROM", "COPY", "RUN", "WORKDIR", "ENV", "CMD":
			out = append(out, buildInstruction{Op: op, Arg: arg, Raw: trimmed})
		default:
			return nil, fmt.Errorf("unsupported instruction %q at line %d", op, i+1)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("Docksmithfile has no instructions")
	}
	if out[0].Op != "FROM" {
		return nil, fmt.Errorf("first instruction must be FROM")
	}
	return out, nil
}

// snapshotRootFS captures file digests and directory paths for delta computation.
func snapshotRootFS(root string) (fsSnapshot, error) {
	s := fsSnapshot{
		files: map[string]string{},
		dirs:  map[string]struct{}{},
	}
	err := filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if info.IsDir() {
			s.dirs[rel] = struct{}{}
			return nil
		}
		// Skip symlinks
		if info.Mode()&os.ModeSymlink != 0 {
			return nil
		}
		hash, err := layers.ComputeFileDigest(path)
		if err != nil {
			return err
		}
		s.files[rel] = hash
		return nil
	})
	if err != nil {
		return fsSnapshot{}, fmt.Errorf("failed to snapshot filesystem: %w", err)
	}
	return s, nil
}

// writeDelta writes only changed and newly-created filesystem entries into a delta directory.
func writeDelta(before, after fsSnapshot, rootfs, delta string) error {
	for dir := range after.dirs {
		if _, ok := before.dirs[dir]; ok {
			continue
		}
		if err := os.MkdirAll(filepath.Join(delta, filepath.FromSlash(dir)), 0755); err != nil {
			return err
		}
	}
	for rel, newHash := range after.files {
		if oldHash, ok := before.files[rel]; ok && oldHash == newHash {
			continue
		}
		src := filepath.Join(rootfs, filepath.FromSlash(rel))
		dst := filepath.Join(delta, filepath.FromSlash(rel))
		if err := copyPath(src, dst); err != nil {
			return err
		}
	}
	return nil
}

// copyPath copies a file, directory, or symlink from src to dst preserving basic metadata.
func copyPath(src, dst string) error {
	info, err := os.Lstat(src)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return os.MkdirAll(dst, info.Mode())
	}
	if info.Mode()&os.ModeSymlink != 0 {
		if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
			return err
		}
		target, err := os.Readlink(src)
		if err != nil {
			return err
		}
		_ = os.Remove(dst)
		return os.Symlink(target, dst)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, info.Mode())
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return nil
}

// copyDirectoryContents recursively copies the content of srcDir into destDir.
func copyDirectoryContents(srcDir, destDir string) error {
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return err
	}
	return filepath.Walk(srcDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		target := filepath.Join(destDir, rel)
		return copyPath(path, target)
	})
}

// expandCopySources returns all regular COPY source files in stable sorted order.
func expandCopySources(src string) ([]string, error) {
	st, err := os.Stat(src)
	if err != nil {
		return nil, err
	}
	if !st.IsDir() {
		return []string{src}, nil
	}
	out := []string{}
	err = filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		out = append(out, path)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(out)
	return out, nil
}

// resolveContainerPath resolves a container path against rootfs and current workdir.
func resolveContainerPath(rootfs, workDir, pathSpec string) string {
	if filepath.IsAbs(pathSpec) {
		return filepath.Join(rootfs, trimLeadingSlash(filepath.Clean(pathSpec)))
	}
	base := workDir
	if base == "" {
		base = "/"
	}
	joined := filepath.Join(base, pathSpec)
	return filepath.Join(rootfs, trimLeadingSlash(filepath.Clean(joined)))
}

// trimLeadingSlash converts an absolute-style path into a rootfs-relative path.
func trimLeadingSlash(v string) string {
	return strings.TrimPrefix(filepath.ToSlash(v), "/")
}

// defaultWorkingDir returns / when image/build working directory is not set.
func defaultWorkingDir(workDir string) string {
	if strings.TrimSpace(workDir) == "" {
		return "/"
	}
	return workDir
}

// envMapFromPairs converts KEY=VALUE strings into a map, skipping invalid entries.
func envMapFromPairs(pairs []string) map[string]string {
	out := map[string]string{}
	for _, pair := range pairs {
		k, v, ok := strings.Cut(pair, "=")
		if !ok || strings.TrimSpace(k) == "" {
			continue
		}
		out[k] = v
	}
	return out
}

// copyEnvMap returns a shallow copy of an environment map.
func copyEnvMap(src map[string]string) map[string]string {
	out := make(map[string]string, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}

// sortedEnvPairs serializes the environment map into lexicographically sorted KEY=VALUE pairs.
func (b *builderState) sortedEnvPairs() []string {
	if len(b.env) == 0 {
		return nil
	}
	keys := make([]string, 0, len(b.env))
	for k := range b.env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	pairs := make([]string, 0, len(keys))
	for _, k := range keys {
		pairs = append(pairs, k+"="+b.env[k])
	}
	return pairs
}

// isLayerProducing reports whether an instruction type should create a new layer.
func isLayerProducing(op string) bool {
	return op == "COPY" || op == "RUN"
}
