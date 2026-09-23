//go:build !unix && !windows

package fulltext

func tryLock(string) (func(), error) { return func() {}, nil }
