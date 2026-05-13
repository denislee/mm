//go:build !unix

package cache

// lockFile is a no-op on platforms without flock. Multi-instance use on
// those platforms can race on the cache file; build-tag this out with a
// real locking primitive when needed.
func lockFile(path string) (func(), error) {
	return func() {}, nil
}
