package sensephoto

import "golang.org/x/sys/windows"

func picturesDir() (string, error) {
	return windows.KnownFolderPath(windows.FOLDERID_Pictures, windows.KF_FLAG_DONT_VERIFY)
}

func publishPhoto(src, dest string) error {
	from, err := windows.UTF16PtrFromString(src)
	if err != nil {
		return err
	}
	to, err := windows.UTF16PtrFromString(dest)
	if err != nil {
		return err
	}
	return windows.MoveFile(from, to)
}
