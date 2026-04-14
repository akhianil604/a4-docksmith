package operations

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"docksmith/imagestore"
)

type ImagesOpts struct{}

func Images(opts *ImagesOpts) error {
	_ = opts
	imagesPath, err := imagestore.DefaultImagesPath()
	if err != nil {
		return err
	}
	manifests, err := imagestore.ListManifests(imagesPath)
	if err != nil {
		return err
	}

	sort.Slice(manifests, func(i, j int) bool {
		if manifests[i].Name != manifests[j].Name {
			return manifests[i].Name < manifests[j].Name
		}
		return manifests[i].Tag < manifests[j].Tag
	})

	fmt.Fprintln(os.Stdout, "NAME\tTAG\tID\tCREATED")
	for _, m := range manifests {
		id := strings.TrimPrefix(m.Digest, "sha256:")
		if len(id) > 12 {
			id = id[:12]
		}
		fmt.Fprintf(os.Stdout, "%s\t%s\t%s\t%s\n", m.Name, m.Tag, id, m.Created)
	}

	return nil
}
