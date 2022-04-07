package utils

import (
	"os/exec"
	"strings"

	"github.com/pkg/errors"
)

func Untar(srcTar, destDir string) error {
	if !strings.HasSuffix(srcTar, "tar") {
		return errors.Errorf("Invalid tarBall name: %d", srcTar)
	}

	cmd := exec.Command("tar", "-C", destDir, "-xf", srcTar)
	err := cmd.Start()
	if err != nil {
		return err
	}

	return cmd.Wait()
}
