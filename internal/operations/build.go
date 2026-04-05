package operations

type BuildOpts struct {
	Tag     string
	Context string
	NoCache bool
}

func Build(opts *BuildOpts) error {
	_ = opts
	return notImplemented("build")
}
