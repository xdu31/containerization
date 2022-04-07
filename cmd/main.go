package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"

	"github.com/docker/docker/pkg/reexec"
	"go.uber.org/zap"

	"github.com/xdu31/containerization/pkg/containerization"
)

func init() {
	reexec.Register("nsInit", nsInit)
	if reexec.Init() {
		os.Exit(0)
	}
}

func nsInit() {
	containerName := os.Args[1]

	if err := containerization.MountProc(containerName); err != nil {
		fmt.Printf("MountProc failed - %s\n", err)
		os.Exit(1)
	}

	if err := containerization.PivotRoot(containerName); err != nil {
		fmt.Printf("PivotRoot failed - %s\n", err)
		os.Exit(1)
	}

	if err := syscall.Sethostname([]byte(containerName)); err != nil {
		fmt.Printf("Sethostname failed - %s\n", err)
		os.Exit(1)
	}

	if err := containerization.WaitForNetwork(); err != nil {
		fmt.Printf("WaitForNetwork failed - %s\n", err)
		os.Exit(1)
	}

	nsRun(containerName)
}

func nsRun(containerName string) {
	cmd := exec.Command("bin/sh")

	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	cmd.Env = []string{
		fmt.Sprintf("PS1=[%s] # ", containerName),
	}

	if err := cmd.Run(); err != nil {
		fmt.Printf("Error running the /bin/sh command - %s\n", err)
		os.Exit(1)
	}
}

var Logger *zap.Logger

func main() {
	var (
		err           error
		containerName string
		containerDir  string
		baseImageName string
	)
	flag.StringVar(
		&containerName,
		"container-name",
		"mycontainer",
		"Name of the container to use",
	)
	flag.StringVar(
		&containerDir,
		"container-dir",
		containerization.ContainerDir,
		"Container directory to use",
	)
	flag.StringVar(
		&baseImageName,
		"base-image",
		containerization.BaseImage,
		"Base image to use",
	)
	flag.Parse()

	// initialize Logger
	Logger, err = initLogger()
	if err != nil {
		fmt.Println("InitLogger failed")
		os.Exit(1)
	}

	err = containerization.CheckContainerDir(containerDir)
	if err != nil {
		Logger.Error("CheckContainerDir failed", zap.Error(err))
		os.Exit(1)
	}

	imagePath := filepath.Join(
		containerDir,
		containerization.ImageDir,
		baseImageName,
	)
	destRootFsDir := filepath.Join(
		containerDir,
		containerization.RootFsDir,
		containerName,
	)
	err = containerization.PrepareRootFs(imagePath, destRootFsDir)
	if err != nil {
		Logger.Error("PrepareRootFs failed", zap.Error(err))
		os.Exit(1)
	}

	cmd := reexec.Command("nsInit", containerName)

	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	cmd.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags: syscall.CLONE_NEWNS |
			syscall.CLONE_NEWUTS |
			syscall.CLONE_NEWIPC |
			syscall.CLONE_NEWPID |
			syscall.CLONE_NEWNET |
			syscall.CLONE_NEWUSER,
		UidMappings: []syscall.SysProcIDMap{
			{
				ContainerID: 0,
				HostID:      os.Getuid(),
				Size:        1,
			},
		},
		GidMappings: []syscall.SysProcIDMap{
			{
				ContainerID: 0,
				HostID:      os.Getgid(),
				Size:        1,
			},
		},
	}

	if err := cmd.Start(); err != nil {
		Logger.Error("Start failed", zap.Error(err))
		os.Exit(1)
	}

	//
	// note that netsetgo must be owned by root with the setuid bit set
	netSetConfig := &containerization.NetworkConfigInput{
		Pid:              cmd.Process.Pid,
		Logger:           Logger,
		BridgeName:       "brg0",
		VethNamePrefix:   "veth",
		BridgeAddress:    "10.10.10.1/24",
		ContainerAddress: "10.10.10.2/24",
	}

	err = containerization.SetNetwork(netSetConfig)
	if err != nil {
		Logger.Error("SetNetwork failed", zap.Error(err))
		os.Exit(1)
	}

	if err := cmd.Wait(); err != nil {
		Logger.Error("Wait failed", zap.Error(err))
		os.Exit(1)
	}
}
