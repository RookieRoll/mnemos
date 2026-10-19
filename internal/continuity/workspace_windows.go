package continuity

import (
	"os"
	"strings"
	"syscall"
	"unsafe"
)

// Canonicalize the opened target rather than enumerating every ancestor. A
// user may traverse a profile directory and read its Temp child without having
// permission to list the profile itself; filepath.EvalSymlinks normalizes each
// ancestor with FindFirstFile and rejects that otherwise accessible target.
func canonicalFilesystemPath(path string) (string, error) {
	wide, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return "", err
	}
	handle, err := syscall.CreateFile(wide, 0, syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE|syscall.FILE_SHARE_DELETE, nil, syscall.OPEN_EXISTING, syscall.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return "", err
	}
	defer syscall.CloseHandle(handle)
	proc := syscall.NewLazyDLL("kernel32.dll").NewProc("GetFinalPathNameByHandleW")
	buffer := make([]uint16, 32768)
	n, _, callErr := proc.Call(uintptr(handle), uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer)), 0)
	if n == 0 {
		if callErr != syscall.Errno(0) {
			return "", callErr
		}
		return "", os.ErrInvalid
	}
	if n >= uintptr(len(buffer)) {
		return "", os.ErrInvalid
	}
	final := syscall.UTF16ToString(buffer[:n])
	if strings.HasPrefix(final, `\\?\UNC\`) {
		final = `\\` + strings.TrimPrefix(final, `\\?\UNC\`)
	} else {
		final = strings.TrimPrefix(final, `\\?\`)
	}
	return final, nil
}
