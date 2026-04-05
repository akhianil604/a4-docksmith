package operations

import "fmt"

func notImplemented(op string) error {
	return fmt.Errorf("%s is not implemented yet", op)
}
