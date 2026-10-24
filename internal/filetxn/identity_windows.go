//go:build windows

package filetxn

import (
	"os"
	"syscall"
)

// directoryIdentity returns the volume serial number and file index of the
// directory at path. Lstat results on Windows carry no file identity, so the
// directory is opened without following reparse points and the handle must
// still name the same object as info.
func directoryIdentity(path string, info os.FileInfo) (device, inode uint64, ok bool) {
	if !isRealDirectory(info) {
		return 0, 0, false
	}
	name, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return 0, 0, false
	}
	handle, err := syscall.CreateFile(
		name,
		0,
		syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE|syscall.FILE_SHARE_DELETE,
		nil,
		syscall.OPEN_EXISTING,
		syscall.FILE_FLAG_BACKUP_SEMANTICS|syscall.FILE_FLAG_OPEN_REPARSE_POINT,
		0,
	)
	if err != nil {
		return 0, 0, false
	}
	file := os.NewFile(uintptr(handle), path)
	defer file.Close()
	var data syscall.ByHandleFileInformation
	if err := syscall.GetFileInformationByHandle(handle, &data); err != nil {
		return 0, 0, false
	}
	if data.FileAttributes&syscall.FILE_ATTRIBUTE_DIRECTORY == 0 || data.FileAttributes&syscall.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return 0, 0, false
	}
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return 0, 0, false
	}
	index := uint64(data.FileIndexHigh)<<32 | uint64(data.FileIndexLow)
	if index == 0 {
		return 0, 0, false
	}
	return uint64(data.VolumeSerialNumber), index, true
}
