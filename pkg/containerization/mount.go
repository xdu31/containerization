package containerization

import (
	"os"
	"path/filepath"
	"syscall"

	"github.com/xdu31/containerization/pkg/utils"
)

const (
	ContainerDir = "/var/tmp/containers"
	RootFsDir    = "rootfs"
	ImageDir     = "images"
	BaseImage    = "busybox.tar"
	pivotRootDir = ".pivot_root"
	procDir      = "proc"
)

func PivotRoot(containerName string) error {
	newRoot := filepath.Join(ContainerDir, RootFsDir, containerName)
	putOld := filepath.Join(newRoot, pivotRootDir)

	// bind mount newRoot to itself - this is a slight hack needed to satisfy the
	// pivot_root requirement that newRoot and putOld must not be on the same
	// filesystem as the current root
	if err := syscall.Mount(
		newRoot,
		newRoot,
		"",
		syscall.MS_BIND|syscall.MS_REC,
		"",
	); err != nil {
		return err
	}

	// create putOld directory
	if err := os.MkdirAll(putOld, 0700); err != nil {
		return err
	}

	// call pivot_root system call
	if err := syscall.PivotRoot(newRoot, putOld); err != nil {
		return err
	}

	// ensure current working directory is set to new root
	if err := os.Chdir("/"); err != nil {
		return err
	}

	// umount putOld, which now lives at /.pivot_root
	putOld = filepath.Join("/", pivotRootDir)
	if err := syscall.Unmount(putOld, syscall.MNT_DETACH); err != nil {
		return err
	}

	// remove putOld
	if err := os.RemoveAll(putOld); err != nil {
		return err
	}

	return nil
}

func MountProc(containerName string) error {
	newRoot := filepath.Join(ContainerDir, RootFsDir, containerName)
	target := filepath.Join(newRoot, procDir)

	os.MkdirAll(target, 0755)
	if err := syscall.Mount(
		procDir,
		target,
		"proc",
		0,
		"",
	); err != nil {
		return err
	}

	return nil
}

func PrepareRootFs(imagePath, destRootFsDir string) error {
	err := os.RemoveAll(destRootFsDir)
	if err != nil {
		return err
	}

	err = os.Mkdir(destRootFsDir, os.FileMode(0775))
	if err != nil {
		return err
	}

	return utils.Untar(imagePath, destRootFsDir)
}

func CheckContainerDir(containerDir string) error {
	_, err := os.Stat(containerDir)
	if os.IsNotExist(err) {
		return err
	}

	rootFs := filepath.Join(containerDir, RootFsDir)
	_, err = os.Stat(rootFs)
	if os.IsNotExist(err) {
		return err
	}

	baseImageTar := filepath.Join(containerDir, ImageDir)
	_, err = os.Stat(baseImageTar)
	if os.IsNotExist(err) {
		return err
	}

	return nil
}
