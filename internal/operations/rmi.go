package operations

type RMIOpts struct {
	Reference string
}

func RMI(opts *RMIOpts) error {
	_ = opts
	return notImplemented("rmi")
}
