package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"time"

	"docksmith/image"
	"docksmith/layers"
	"docksmith/parser"
)

type localBuilder struct {
	images image.Store
}

type localRunner struct{}

func (b localBuilder) Build(req BuildRequest) error {
	if len(req.Instructions) == 0 {
		return fmt.Errorf("no instructions to build")
	}
	startTotal := time.Now()
	steps := len(req.Instructions)

	from := req.Instructions[0]
	if from.Type != parser.InstructionFrom || len(from.Args) != 1 {
		return fmt.Errorf("first instruction must be FROM <name:tag>")
	}
	baseName, baseTag, err := parseImageRef(from.Args[0])
	if err != nil {
		return fmt.Errorf("line %d: invalid FROM image %q: %w", from.Line, from.Args[0], err)
	}
	baseManifest, _, err := b.images.Load(baseName, baseTag)
	if err != nil {
		return fmt.Errorf("line %d: base image %s:%s not found: %w", from.Line, baseName, baseTag, err)
	}

	fmt.Printf("Step 1/%d : %s\n", steps, from.Raw)

	finalLayers := make([]image.Layer, 0, len(baseManifest.Layers)+len(req.Instructions))
	finalLayers = append(finalLayers, baseManifest.Layers...)
	envMap := envSliceToMap(baseManifest.Config.Env)
	workdir := baseManifest.Config.WorkingDir
	cmd := append([]string(nil), baseManifest.Config.Cmd...)

	for i := 1; i < len(req.Instructions); i++ {
		inst := req.Instructions[i]
		fmt.Printf("Step %d/%d : %s", i+1, steps, inst.Raw)

		switch inst.Type {
		case parser.InstructionWorkdir:
			workdir = normalizeImagePath(inst.Value, workdir)
			fmt.Println()
		case parser.InstructionEnv:
			envMap[inst.Key] = inst.Value
			fmt.Println()
		case parser.InstructionCmd:
			cmd = append([]string(nil), inst.JSON...)
			fmt.Println()
		case parser.InstructionCopy:
			stepStart := time.Now()
			meta, err := b.executeCopy(req, inst, workdir)
			if err != nil {
				return err
			}
			finalLayers = append(finalLayers, image.Layer{Digest: meta.Digest, Size: meta.Size, CreatedBy: inst.Raw})
			fmt.Printf(" [CACHE MISS] %.2fs\n", time.Since(stepStart).Seconds())
		case parser.InstructionRun:
			stepStart := time.Now()
			meta, err := b.executeRun(req, inst, workdir, envMap, finalLayers)
			if err != nil {
				return err
			}
			finalLayers = append(finalLayers, image.Layer{Digest: meta.Digest, Size: meta.Size, CreatedBy: inst.Raw})
			fmt.Printf(" [CACHE MISS] %.2fs\n", time.Since(stepStart).Seconds())
		default:
			return fmt.Errorf("line %d: unsupported instruction %s", inst.Line, inst.Type)
		}
	}

	created := time.Now().UTC().Format(time.RFC3339)
	if existing, _, err := b.images.Load(req.ImageName, req.ImageTag); err == nil {
		created = existing.Created
	}
	manifest := image.Manifest{
		Name:    req.ImageName,
		Tag:     req.ImageTag,
		Digest:  "",
		Created: created,
		Config: image.Config{
			Env:        envMapToSortedSlice(envMap),
			Cmd:        cmd,
			WorkingDir: workdir,
		},
		Layers: finalLayers,
	}
	manifest.Digest, err = computeManifestDigest(manifest)
	if err != nil {
		return fmt.Errorf("compute manifest digest: %w", err)
	}
	if err := writeManifest(req.State.ImagesDir, manifest); err != nil {
		return err
	}

	fmt.Printf("Successfully built %s %s:%s (%.2fs)\n", manifest.Digest, req.ImageName, req.ImageTag, time.Since(startTotal).Seconds())
	return nil
}

func (b localBuilder) executeCopy(req BuildRequest, inst parser.Instruction, workdir string) (layers.LayerMetadata, error) {
	if len(inst.Args) != 2 {
		return layers.LayerMetadata{}, fmt.Errorf("line %d: COPY requires <src> <dest>", inst.Line)
	}
	matches, err := resolveCopyMatches(req.ContextDir, inst.Args[0])
	if err != nil {
		return layers.LayerMetadata{}, fmt.Errorf("line %d: resolve COPY source: %w", inst.Line, err)
	}
	if len(matches) == 0 {
		return layers.LayerMetadata{}, fmt.Errorf("line %d: COPY source %q matched no files", inst.Line, inst.Args[0])
	}

	deltaDir, err := os.MkdirTemp("", "docksmith-copy-delta-")
	if err != nil {
		return layers.LayerMetadata{}, fmt.Errorf("line %d: allocate delta dir: %w", inst.Line, err)
	}
	defer os.RemoveAll(deltaDir)

	destBase := normalizeImagePath(inst.Args[1], workdir)
	destIsDir := strings.HasSuffix(inst.Args[1], "/") || len(matches) > 1
	for _, rel := range matches {
		srcAbs := filepath.Join(req.ContextDir, filepath.FromSlash(rel))
		info, err := os.Stat(srcAbs)
		if err != nil {
			return layers.LayerMetadata{}, fmt.Errorf("line %d: stat %s: %w", inst.Line, rel, err)
		}
		if rel == "." {
			entries, err := os.ReadDir(req.ContextDir)
			if err != nil {
				return layers.LayerMetadata{}, fmt.Errorf("line %d: read context: %w", inst.Line, err)
			}
			for _, entry := range entries {
				name := entry.Name()
				target := path.Join(destBase, name)
				if err := copyFromContext(filepath.Join(req.ContextDir, name), target, deltaDir); err != nil {
					return layers.LayerMetadata{}, fmt.Errorf("line %d: COPY . failed: %w", inst.Line, err)
				}
			}
			continue
		}

		target := destBase
		if destIsDir || info.IsDir() {
			target = path.Join(destBase, path.Base(rel))
		}
		if err := copyFromContext(srcAbs, target, deltaDir); err != nil {
			return layers.LayerMetadata{}, fmt.Errorf("line %d: COPY %s failed: %w", inst.Line, rel, err)
		}
	}

	meta, err := layers.CreateLayer(deltaDir, req.State.LayersDir, inst.Raw)
	if err != nil {
		return layers.LayerMetadata{}, fmt.Errorf("line %d: create COPY layer: %w", inst.Line, err)
	}
	return meta, nil
}

func (b localBuilder) executeRun(req BuildRequest, inst parser.Instruction, workdir string, envMap map[string]string, currentLayers []image.Layer) (layers.LayerMetadata, error) {
	rootfs, err := os.MkdirTemp("", "docksmith-build-rootfs-")
	if err != nil {
		return layers.LayerMetadata{}, fmt.Errorf("line %d: create build rootfs: %w", inst.Line, err)
	}
	defer os.RemoveAll(rootfs)

	if err := extractLayers(currentLayers, req.State.LayersDir, rootfs); err != nil {
		return layers.LayerMetadata{}, fmt.Errorf("line %d: assemble rootfs: %w", inst.Line, err)
	}

	if workdir == "" {
		workdir = "/"
	}
	workdirHost := filepath.Join(rootfs, filepath.FromSlash(strings.TrimPrefix(workdir, "/")))
	if err := os.MkdirAll(workdirHost, 0755); err != nil {
		return layers.LayerMetadata{}, fmt.Errorf("line %d: create WORKDIR %s: %w", inst.Line, workdir, err)
	}

	before, err := snapshotRootfs(rootfs)
	if err != nil {
		return layers.LayerMetadata{}, fmt.Errorf("line %d: snapshot before RUN: %w", inst.Line, err)
	}

	if err := runIsolated(rootfs, workdir, envMapToSortedSlice(envMap), []string{"/bin/sh", "-c", inst.Value}); err != nil {
		return layers.LayerMetadata{}, fmt.Errorf("line %d: RUN failed: %w", inst.Line, err)
	}

	after, err := snapshotRootfs(rootfs)
	if err != nil {
		return layers.LayerMetadata{}, fmt.Errorf("line %d: snapshot after RUN: %w", inst.Line, err)
	}

	deltaDir, err := os.MkdirTemp("", "docksmith-run-delta-")
	if err != nil {
		return layers.LayerMetadata{}, fmt.Errorf("line %d: create RUN delta dir: %w", inst.Line, err)
	}
	defer os.RemoveAll(deltaDir)

	if err := writeDelta(rootfs, before, after, deltaDir); err != nil {
		return layers.LayerMetadata{}, fmt.Errorf("line %d: create RUN delta: %w", inst.Line, err)
	}
	meta, err := layers.CreateLayer(deltaDir, req.State.LayersDir, inst.Raw)
	if err != nil {
		return layers.LayerMetadata{}, fmt.Errorf("line %d: store RUN layer: %w", inst.Line, err)
	}
	return meta, nil
}

func (r localRunner) Run(req RunRequest) error {
	rootfs, err := os.MkdirTemp("", "docksmith-run-rootfs-")
	if err != nil {
		return fmt.Errorf("create run rootfs: %w", err)
	}
	defer os.RemoveAll(rootfs)

	if err := extractLayers(req.Image.Layers, req.State.LayersDir, rootfs); err != nil {
		return fmt.Errorf("assemble rootfs: %w", err)
	}

	env := envSliceToMap(req.Image.Config.Env)
	for k, v := range req.EnvOverrides {
		env[k] = v
	}
	workdir := req.Image.Config.WorkingDir
	if workdir == "" {
		workdir = "/"
	}
	workdirHost := filepath.Join(rootfs, filepath.FromSlash(strings.TrimPrefix(workdir, "/")))
	if err := os.MkdirAll(workdirHost, 0755); err != nil {
		return fmt.Errorf("ensure working directory %s: %w", workdir, err)
	}

	if err := runIsolated(rootfs, workdir, envMapToSortedSlice(env), req.Command); err != nil {
		return err
	}
	fmt.Fprintln(os.Stdout, "Exit code: 0")
	return nil
}

func extractLayers(manifestLayers []image.Layer, layersDir, rootfs string) error {
	for _, layer := range manifestLayers {
		if err := layers.ExtractLayer(layer.Digest, layersDir, rootfs); err != nil {
			return err
		}
	}
	return nil
}

func runIsolated(rootfs, workdir string, env []string, command []string) error {
	if len(command) == 0 {
		return fmt.Errorf("empty command")
	}
	cmd := exec.Command(command[0], command[1:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = env
	cmd.Dir = workdir
	cmd.SysProcAttr = &syscall.SysProcAttr{Chroot: rootfs}

	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			code := exitErr.ExitCode()
			fmt.Fprintf(os.Stdout, "Exit code: %d\n", code)
			return fmt.Errorf("command exited with code %d", code)
		}
		return fmt.Errorf("run command in isolated rootfs: %w", err)
	}
	return nil
}

type fileSnapshot struct {
	Type    string
	Mode    fs.FileMode
	Digest  string
	LinkDst string
}

func snapshotRootfs(rootfs string) (map[string]fileSnapshot, error) {
	snap := map[string]fileSnapshot{}
	err := filepath.WalkDir(rootfs, func(full string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(rootfs, full)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		imgPath := "/" + filepath.ToSlash(rel)
		info, err := os.Lstat(full)
		if err != nil {
			return err
		}
		entry := fileSnapshot{Mode: info.Mode()}
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			entry.Type = "symlink"
			link, err := os.Readlink(full)
			if err != nil {
				return err
			}
			entry.LinkDst = link
		case info.IsDir():
			entry.Type = "dir"
		case info.Mode().IsRegular():
			entry.Type = "file"
			digest, err := layers.ComputeFileDigest(full)
			if err != nil {
				return err
			}
			entry.Digest = digest
		default:
			entry.Type = "other"
		}
		snap[imgPath] = entry
		return nil
	})
	if err != nil {
		return nil, err
	}
	return snap, nil
}

func writeDelta(rootfs string, before, after map[string]fileSnapshot, deltaDir string) error {
	changed := make([]string, 0)
	for p, post := range after {
		pre, ok := before[p]
		if !ok || pre != post {
			changed = append(changed, p)
		}
	}
	sort.Strings(changed)
	for _, p := range changed {
		full := filepath.Join(rootfs, filepath.FromSlash(strings.TrimPrefix(p, "/")))
		if err := copyFromContext(full, p, deltaDir); err != nil {
			return err
		}
	}
	return nil
}

func copyFromContext(srcAbs, imagePath, staging string) error {
	info, err := os.Lstat(srcAbs)
	if err != nil {
		return err
	}
	dstAbs := filepath.Join(staging, filepath.FromSlash(strings.TrimPrefix(imagePath, "/")))

	if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(srcAbs)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(dstAbs), 0755); err != nil {
			return err
		}
		_ = os.Remove(dstAbs)
		return os.Symlink(target, dstAbs)
	}
	if info.IsDir() {
		if err := os.MkdirAll(dstAbs, info.Mode().Perm()); err != nil {
			return err
		}
		entries, err := os.ReadDir(srcAbs)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			nextSrc := filepath.Join(srcAbs, entry.Name())
			nextDst := path.Join(imagePath, entry.Name())
			if err := copyFromContext(nextSrc, nextDst, staging); err != nil {
				return err
			}
		}
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(dstAbs), 0755); err != nil {
		return err
	}
	in, err := os.Open(srcAbs)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dstAbs, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

func resolveCopyMatches(contextDir, pattern string) ([]string, error) {
	pattern = filepath.ToSlash(filepath.Clean(pattern))
	if pattern == "." {
		return []string{"."}, nil
	}
	if !strings.ContainsAny(pattern, "*?[") {
		abs := filepath.Join(contextDir, filepath.FromSlash(pattern))
		if _, err := os.Stat(abs); err != nil {
			if os.IsNotExist(err) {
				return nil, nil
			}
			return nil, err
		}
		return []string{pattern}, nil
	}

	var re *regexp.Regexp
	if strings.Contains(pattern, "**") {
		compiled, err := regexp.Compile(globToRegex(pattern))
		if err != nil {
			return nil, err
		}
		re = compiled
	}

	matches := make([]string, 0)
	err := filepath.WalkDir(contextDir, func(full string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(contextDir, full)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == "." {
			return nil
		}
		matched := false
		if re != nil {
			matched = re.MatchString(rel)
		} else {
			matched, err = path.Match(pattern, rel)
			if err != nil {
				return err
			}
		}
		if matched {
			matches = append(matches, rel)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(matches)
	return matches, nil
}

func globToRegex(glob string) string {
	var b strings.Builder
	b.WriteString("^")
	for i := 0; i < len(glob); i++ {
		ch := glob[i]
		if ch == '*' && i+1 < len(glob) && glob[i+1] == '*' {
			b.WriteString(".*")
			i++
			continue
		}
		switch ch {
		case '*':
			b.WriteString("[^/]*")
		case '?':
			b.WriteString("[^/]")
		case '.', '+', '(', ')', '|', '^', '$', '{', '}', '[', ']', '\\':
			b.WriteByte('\\')
			b.WriteByte(ch)
		default:
			b.WriteByte(ch)
		}
	}
	b.WriteString("$")
	return b.String()
}

func normalizeImagePath(value, currentWorkdir string) string {
	if value == "" {
		if currentWorkdir == "" {
			return "/"
		}
		return currentWorkdir
	}
	if strings.HasPrefix(value, "/") {
		return path.Clean(value)
	}
	base := currentWorkdir
	if base == "" {
		base = "/"
	}
	return path.Clean(path.Join(base, value))
}

func envSliceToMap(values []string) map[string]string {
	m := map[string]string{}
	for _, pair := range values {
		k, v, ok := strings.Cut(pair, "=")
		if !ok || k == "" {
			continue
		}
		m[k] = v
	}
	return m
}

func envMapToSortedSlice(values map[string]string) []string {
	if len(values) == 0 {
		return nil
	}
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, k+"="+values[k])
	}
	return out
}

func computeManifestDigest(manifest image.Manifest) (string, error) {
	manifest.Digest = ""
	payload, err := json.Marshal(manifest)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(payload)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func writeManifest(imagesDir string, manifest image.Manifest) error {
	if err := os.MkdirAll(imagesDir, 0755); err != nil {
		return fmt.Errorf("create images dir: %w", err)
	}
	path := filepath.Join(imagesDir, sanitizeSegment(manifest.Name)+"_"+sanitizeSegment(manifest.Tag)+".json")
	payload, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal manifest: %w", err)
	}
	if err := os.WriteFile(path, append(payload, '\n'), 0644); err != nil {
		return fmt.Errorf("write manifest: %w", err)
	}
	return nil
}

func sanitizeSegment(input string) string {
	var b strings.Builder
	for _, r := range input {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '.', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	if b.Len() == 0 {
		return "image"
	}
	return b.String()
}
