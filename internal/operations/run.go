package operations

type RunOpts struct {
	Reference string
	Cmd       []string
	Env       map[string]string
}

func Run(opts *RunOpts) error {
	_ = opts
	return notImplemented("run")
}
