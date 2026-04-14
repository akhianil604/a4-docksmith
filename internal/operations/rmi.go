package operations

import (
	"fmt"

	"docksmith/imagestore"
	"docksmith/layers"
)

type RMIOpts struct {
	Reference string
}

func RMI(opts *RMIOpts) error {
	if opts == nil {
		return fmt.Errorf("rmi options cannot be nil")
	}
	imagesPath, err := imagestore.DefaultImagesPath()
	if err != nil {
		return err
	}
	manifest, _, err := imagestore.LoadManifest(imagesPath, opts.Reference)
	if err != nil {
		return err
	}

	storePath, err := layers.DefaultStorePath()
	if err != nil {
		return err
	}
	for _, layer := range manifest.Layers {
		if err := layers.DeleteLayer(layer.Digest, storePath); err != nil {
			return err
		}
	}

	if err := imagestore.DeleteManifest(imagesPath, opts.Reference); err != nil {
		return err
	}

	return nil
}
