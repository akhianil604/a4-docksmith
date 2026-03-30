package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"

	"docksmith/image"
	"docksmith/layers"
	"docksmith/parser"
	"docksmith/state"
)

type buildExecutor interface {
	Build(BuildRequest) error
}

type runExecutor interface {
	Run(RunRequest) error
}

type BuildRequest struct {
	ImageName         string
	ImageTag          string
	ContextDir        string
	DocksmithfilePath string
	Instructions      []parser.Instruction
	NoCache           bool
	State             state.Paths
}

type RunRequest struct {
	Image        image.Manifest
	Command      []string
	EnvOverrides map[string]string
	State        state.Paths
}

type app struct {
	images  image.Store
	builder buildExecutor
	runner  runExecutor
}

func main() {
	paths, err := state.DefaultPaths()
	exitOnErr(err)
	exitOnErr(state.Ensure(paths))

	application := app{
		images:  image.NewStore(paths.ImagesDir),
		builder: localBuilder{images: image.NewStore(paths.ImagesDir)},
		runner:  localRunner{},
	}

	if err := application.run(os.Args[1:]); err != nil {
		fail(err)
	}
}

func (a app) run(args []string) error {
	if len(args) == 0 {
		return usageError("")
	}

	switch args[0] {
	case "build":
		return a.runBuild(args[1:])
	case "images":
		return a.runImages(args[1:])
	case "rmi":
		return a.runRMI(args[1:])
	case "run":
		return a.runContainer(args[1:])
	case "help", "-h", "--help":
		return usageError("")
	default:
		return usageError(fmt.Sprintf("unknown command %q", args[0]))
	}
}

func (a app) runBuild(args []string) error {
	fs := flag.NewFlagSet("build", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	tag := fs.String("t", "", "image name and tag in name:tag form")
	noCache := fs.Bool("no-cache", false, "skip cache lookups and writes")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if strings.TrimSpace(*tag) == "" {
		return fmt.Errorf("build requires -t <name:tag>")
	}
	name, tagValue, err := parseImageRef(*tag)
	if err != nil {
		return fmt.Errorf("invalid build tag: %w", err)
	}

	if fs.NArg() != 1 {
		return fmt.Errorf("build requires exactly one context directory")
	}
	contextDir, err := filepath.Abs(fs.Arg(0))
	if err != nil {
		return fmt.Errorf("resolve context directory: %w", err)
	}
	info, err := os.Stat(contextDir)
	if err != nil {
		return fmt.Errorf("context directory error: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("context path %q is not a directory", contextDir)
	}

	docksmithfilePath := filepath.Join(contextDir, "Docksmithfile")
	instructions, err := parser.ParseFile(docksmithfilePath)
	if err != nil {
		return err
	}

	paths, err := state.DefaultPaths()
	if err != nil {
		return err
	}

	return a.builder.Build(BuildRequest{
		ImageName:         name,
		ImageTag:          tagValue,
		ContextDir:        contextDir,
		DocksmithfilePath: docksmithfilePath,
		Instructions:      instructions,
		NoCache:           *noCache,
		State:             paths,
	})
}

func (a app) runImages(args []string) error {
	if len(args) != 0 {
		return fmt.Errorf("images does not accept positional arguments")
	}

	manifests, err := a.images.List()
	if err != nil {
		return err
	}

	sort.Slice(manifests, func(i, j int) bool {
		if manifests[i].Name != manifests[j].Name {
			return manifests[i].Name < manifests[j].Name
		}
		return manifests[i].Tag < manifests[j].Tag
	})

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tTAG\tID\tCREATED")
	for _, manifest := range manifests {
		fmt.Fprintf(
			w,
			"%s\t%s\t%s\t%s\n",
			manifest.Name,
			manifest.Tag,
			shortDigest(manifest.Digest),
			manifest.Created,
		)
	}
	return w.Flush()
}

func (a app) runRMI(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("rmi requires exactly one image reference in name:tag form")
	}

	name, tagValue, err := parseImageRef(args[0])
	if err != nil {
		return fmt.Errorf("invalid image reference: %w", err)
	}

	manifest, path, err := a.images.Load(name, tagValue)
	if err != nil {
		return err
	}

	paths, err := state.DefaultPaths()
	if err != nil {
		return err
	}

	for _, layer := range manifest.Layers {
		if removeErr := layers.DeleteLayer(layer.Digest, paths.LayersDir); removeErr != nil && !strings.Contains(removeErr.Error(), "not found") {
			return fmt.Errorf("remove layer %s: %w", layer.Digest, removeErr)
		}
	}

	if err := os.Remove(path); err != nil {
		return fmt.Errorf("remove image manifest: %w", err)
	}

	fmt.Fprintf(os.Stdout, "Removed %s:%s\n", name, tagValue)
	return nil
}

func (a app) runContainer(args []string) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	var envVars multiValueFlag
	fs.Var(&envVars, "e", "override environment variable (KEY=VALUE)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if fs.NArg() < 1 {
		return fmt.Errorf("run requires an image reference")
	}
	name, tagValue, err := parseImageRef(fs.Arg(0))
	if err != nil {
		return fmt.Errorf("invalid image reference: %w", err)
	}

	manifest, _, err := a.images.Load(name, tagValue)
	if err != nil {
		return err
	}

	command := fs.Args()[1:]
	if len(command) == 0 {
		command = append(command, manifest.Config.Cmd...)
	}
	if len(command) == 0 {
		return fmt.Errorf("image %s:%s has no CMD and no runtime command was provided", name, tagValue)
	}

	paths, err := state.DefaultPaths()
	if err != nil {
		return err
	}

	return a.runner.Run(RunRequest{
		Image:        manifest,
		Command:      command,
		EnvOverrides: envVars.values,
		State:        paths,
	})
}

type multiValueFlag struct {
	values map[string]string
}

func (m *multiValueFlag) String() string {
	if len(m.values) == 0 {
		return ""
	}
	pairs := make([]string, 0, len(m.values))
	for key, value := range m.values {
		pairs = append(pairs, key+"="+value)
	}
	sort.Strings(pairs)
	return strings.Join(pairs, ",")
}

func (m *multiValueFlag) Set(value string) error {
	key, val, ok := strings.Cut(value, "=")
	if !ok || strings.TrimSpace(key) == "" {
		return fmt.Errorf("environment overrides must use KEY=VALUE form")
	}
	if m.values == nil {
		m.values = map[string]string{}
	}
	m.values[key] = val
	return nil
}

func parseImageRef(ref string) (string, string, error) {
	name, tag, ok := strings.Cut(strings.TrimSpace(ref), ":")
	if !ok || name == "" || tag == "" {
		return "", "", fmt.Errorf("expected name:tag")
	}
	if strings.ContainsAny(name, "/\\ \t\r\n") || strings.ContainsAny(tag, "/\\ \t\r\n") {
		return "", "", fmt.Errorf("image name and tag cannot contain whitespace or path separators")
	}
	return name, tag, nil
}

func shortDigest(digest string) string {
	const prefix = "sha256:"
	if strings.HasPrefix(digest, prefix) {
		digest = strings.TrimPrefix(digest, prefix)
	}
	if len(digest) > 12 {
		return digest[:12]
	}
	return digest
}

func formatMap(values map[string]string) string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	pairs := make([]string, 0, len(keys))
	for _, key := range keys {
		pairs = append(pairs, key+"="+values[key])
	}
	return strings.Join(pairs, ", ")
}

func usageError(message string) error {
	var b strings.Builder
	if message != "" {
		b.WriteString(message)
		b.WriteString("\n\n")
	}
	b.WriteString("Usage:\n")
	b.WriteString("  docksmith build -t <name:tag> [--no-cache] <context>\n")
	b.WriteString("  docksmith images\n")
	b.WriteString("  docksmith rmi <name:tag>\n")
	b.WriteString("  docksmith run [-e KEY=VALUE] <name:tag> [cmd...]\n")
	return errors.New(b.String())
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "Error:", err)
	os.Exit(1)
}

func exitOnErr(err error) {
	if err != nil {
		fail(err)
	}
}
