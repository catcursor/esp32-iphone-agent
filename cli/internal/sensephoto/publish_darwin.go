package sensephoto

import "golang.org/x/sys/unix"

func publishPhoto(src, dest string) error {
	return unix.RenamexNp(src, dest, unix.RENAME_EXCL)
}
