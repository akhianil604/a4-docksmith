package operations

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
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
	createdAt := time.Now().UTC().Format(time.RFC3339)
	if existing, _, err := imagestore.LoadManifest(imagesPath, opts.Tag); err == nil && strings.TrimSpace(existing.Created) != "" {
		createdAt = existing.Created
	} else if err != nil && !strings.Contains(err.Error(), "not found") {
		return err
	}
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
		if err := b.executeInstruction(i+1, len(instructions), inst, imagesPath); err != nil {
			return err
		}
	}

	manifest := imagestore.Manifest{
		Name:    name,
		Tag:     tag,
		Created: createdAt,
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

	fmt.Printf("Successfully built %s %s:%s (%s)\n", finalManifest.Digest, name, tag, time.Since(startTotal).Round(time.Millisecond))
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
func (b *builderState) executeInstruction(step, total int, inst buildInstruction, imagesPath string) error {
	switch inst.Op {
	case "FROM":
		if err := b.handleFrom(inst, imagesPath); err != nil {
			return err
		}
	case "WORKDIR":
		if err := b.handleWorkdir(inst); err != nil {
			return err
		}
	case "ENV":
		if err := b.handleEnv(inst); err != nil {
			return err
		}
	case "CMD":
		if err := b.handleCmd(inst); err != nil {
			return err
		}
	case "COPY":
		return b.handleCopyOrRun(step, total, inst)
	case "RUN":
		return b.handleCopyOrRun(step, total, inst)
	default:
		return fmt.Errorf("unsupported instruction %q", inst.Op)
	}

	fmt.Printf("Step %d/%d : %s\n", step, total, inst.Raw)
	return nil
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
	if !strings.HasPrefix(arg, "[") || !strings.HasSuffix(arg, "]") {
		return fmt.Errorf("CMD requires JSON array form, got %q", arg)
	}
	var cmd []string
	if err := json.Unmarshal([]byte(arg), &cmd); err != nil {
		return fmt.Errorf("invalid CMD JSON array: %w", err)
	}
	if len(cmd) == 0 {
		return fmt.Errorf("CMD array is empty")
	}
	for _, part := range cmd {
		if strings.TrimSpace(part) == "" {
			return fmt.Errorf("CMD array entries cannot be empty")
		}
	}
	b.cmd = append([]string(nil), cmd...)
	return nil
}

// handleCopyOrRun performs cache lookup, executes COPY/RUN on miss, and records a layer.
func (b *builderState) handleCopyOrRun(step, total int, inst buildInstruction) error {
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
			fmt.Printf("Step %d/%d : %s [CACHE HIT] %s\n", step, total, inst.Raw, time.Since(start).Round(time.Millisecond))
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
	fmt.Printf("Step %d/%d : %s [CACHE MISS] %s\n", step, total, inst.Raw, time.Since(start).Round(time.Millisecond))
	return nil
}

// copySourceFiles expands the COPY source argument into a deterministic list of files for cache hashing.
func (b *builderState) copySourceFiles(arg string) ([]string, error) {
	plan, err := b.resolveCopyPlan(arg)
	if err != nil {
		return nil, err
	}
	if !plan.IsPattern {
		return expandCopySources(plan.Sources[0].Abs)
	}

	files := make([]string, 0, len(plan.Sources))
	for _, source := range plan.Sources {
		files = append(files, source.Abs)
	}
	sort.Strings(files)
	return files, nil
}

// applyCopy applies a COPY instruction from context into the build rootfs.
func (b *builderState) applyCopy(arg string) error {
	plan, err := b.resolveCopyPlan(arg)
	if err != nil {
		return err
	}

	if !plan.IsPattern {
		srcPath := plan.Sources[0].Abs
		st, err := os.Stat(srcPath)
		if err != nil {
			return err
		}

		destAbs := plan.DestAbs
		if st.IsDir() {
			return copyDirectoryContents(srcPath, destAbs)
		}
		if strings.HasSuffix(plan.DestSpec, "/") {
			destAbs = filepath.Join(destAbs, filepath.Base(srcPath))
		}
		return copyPath(srcPath, destAbs)
	}

	if len(plan.Sources) == 1 && !strings.HasSuffix(plan.DestSpec, "/") {
		return copyPath(plan.Sources[0].Abs, plan.DestAbs)
	}
	for _, source := range plan.Sources {
		relTarget := source.Rel
		if relTarget == "." || relTarget == "" {
			relTarget = filepath.Base(source.Abs)
		}
		if err := copyPath(source.Abs, filepath.Join(plan.DestAbs, filepath.FromSlash(relTarget))); err != nil {
			return err
		}
	}
	return nil
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

type copySource struct {
	Abs string
	Rel string
}

type copyPlan struct {
	Sources   []copySource
	DestSpec  string
	DestAbs   string
	IsPattern bool
}

func (b *builderState) resolveCopyPlan(arg string) (copyPlan, error) {
	parts := strings.Fields(arg)
	if len(parts) != 2 {
		return copyPlan{}, fmt.Errorf("COPY supports exactly two arguments: COPY <src> <dest>")
	}

	sources, isPattern, err := resolveCopySources(b.contextDir, parts[0])
	if err != nil {
		return copyPlan{}, err
	}

	return copyPlan{
		Sources:   sources,
		DestSpec:  parts[1],
		DestAbs:   resolveContainerPath(b.rootfs, b.workDir, parts[1]),
		IsPattern: isPattern,
	}, nil
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

func resolveCopySources(contextDir, srcSpec string) ([]copySource, bool, error) {
	if strings.TrimSpace(srcSpec) == "" {
		return nil, false, fmt.Errorf("COPY source cannot be empty")
	}
	if filepath.IsAbs(srcSpec) {
		return nil, false, fmt.Errorf("COPY source must stay inside the build context: %s", srcSpec)
	}
	if !isGlobPattern(srcSpec) {
		absPath, err := joinWithinContext(contextDir, srcSpec)
		if err != nil {
			return nil, false, err
		}
		if _, err := os.Stat(absPath); err != nil {
			return nil, false, fmt.Errorf("COPY source not found: %s", srcSpec)
		}
		return []copySource{{Abs: absPath, Rel: filepath.Base(absPath)}}, false, nil
	}

	pattern := normalizeSlashPath(srcSpec)
	prefix := globFixedPrefix(pattern)
	matches := []copySource{}
	err := filepath.Walk(contextDir, func(pathname string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(contextDir, pathname)
		if err != nil {
			return err
		}
		rel = normalizeSlashPath(rel)
		matched, err := matchGlobPattern(pattern, rel)
		if err != nil {
			return err
		}
		if !matched {
			return nil
		}

		targetRel := rel
		if prefix != "" {
			targetRel = strings.TrimPrefix(rel, prefix)
			targetRel = strings.TrimPrefix(targetRel, "/")
			if targetRel == "" {
				targetRel = path.Base(rel)
			}
		}
		matches = append(matches, copySource{Abs: pathname, Rel: targetRel})
		return nil
	})
	if err != nil {
		return nil, true, err
	}
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].Rel != matches[j].Rel {
			return matches[i].Rel < matches[j].Rel
		}
		return matches[i].Abs < matches[j].Abs
	})
	if len(matches) == 0 {
		return nil, true, fmt.Errorf("COPY source pattern %q matched no files", srcSpec)
	}
	return matches, true, nil
}

func joinWithinContext(contextDir, srcSpec string) (string, error) {
	joined := filepath.Join(contextDir, filepath.FromSlash(srcSpec))
	cleaned := filepath.Clean(joined)
	rel, err := filepath.Rel(contextDir, cleaned)
	if err != nil {
		return "", fmt.Errorf("failed to resolve COPY source %s: %w", srcSpec, err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("COPY source must stay inside the build context: %s", srcSpec)
	}
	return cleaned, nil
}

func isGlobPattern(spec string) bool {
	return strings.ContainsAny(spec, "*?[")
}

func normalizeSlashPath(v string) string {
	return strings.TrimPrefix(filepath.ToSlash(filepath.Clean(v)), "./")
}

func globFixedPrefix(pattern string) string {
	segments := strings.Split(pattern, "/")
	fixed := make([]string, 0, len(segments))
	for _, segment := range segments {
		if segment == "**" || strings.ContainsAny(segment, "*?[") {
			break
		}
		fixed = append(fixed, segment)
	}
	return strings.Join(fixed, "/")
}

func matchGlobPattern(pattern, candidate string) (bool, error) {
	return matchGlobSegments(strings.Split(pattern, "/"), strings.Split(candidate, "/"))
}

func matchGlobSegments(patternSegments, candidateSegments []string) (bool, error) {
	if len(patternSegments) == 0 {
		return len(candidateSegments) == 0, nil
	}
	if patternSegments[0] == "**" {
		for i := 0; i <= len(candidateSegments); i++ {
			matched, err := matchGlobSegments(patternSegments[1:], candidateSegments[i:])
			if err != nil {
				return false, err
			}
			if matched {
				return true, nil
			}
		}
		return false, nil
	}
	if len(candidateSegments) == 0 {
		return false, nil
	}
	matched, err := path.Match(patternSegments[0], candidateSegments[0])
	if err != nil {
		return false, fmt.Errorf("invalid COPY glob %q: %w", strings.Join(patternSegments, "/"), err)
	}
	if !matched {
		return false, nil
	}
	return matchGlobSegments(patternSegments[1:], candidateSegments[1:])
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
