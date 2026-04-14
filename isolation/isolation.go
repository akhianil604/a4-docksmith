package isolation

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"syscall"
)

const (
	InternalChildArg = "__docksmith_internal_isolation_child"
	specEnvKey       = "DOCKSMITH_ISOLATION_SPEC"
)

// Spec defines the process launch configuration for an isolated container process.
type Spec struct {
	RootFS     string   `json:"rootfs"`
	WorkingDir string   `json:"workingDir"`
	Env        []string `json:"env"`
	Cmd        []string `json:"cmd"`
}

// Execute runs a command in Linux process and filesystem isolation rooted at RootFS.
func Execute(spec Spec) (int, error) {
	if runtime.GOOS != "linux" {
		return -1, fmt.Errorf("isolation is only supported on linux")
	}
	if spec.RootFS == "" {
		return -1, fmt.Errorf("rootfs is required")
	}
	if len(spec.Cmd) == 0 {
		return -1, fmt.Errorf("command is required")
	}
	if spec.WorkingDir == "" {
		spec.WorkingDir = "/"
	}

	payload, err := encodeSpec(spec)
	if err != nil {
		return -1, err
	}

	cmd := exec.Command(os.Args[0], InternalChildArg)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = append(os.Environ(), specEnvKey+"="+payload)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags:   syscall.CLONE_NEWUSER | syscall.CLONE_NEWNS | syscall.CLONE_NEWUTS | syscall.CLONE_NEWPID,
		Unshareflags: syscall.CLONE_NEWNS,
		UidMappings: []syscall.SysProcIDMap{{
			ContainerID: 0,
			HostID:      os.Getuid(),
			Size:        1,
		}},
		GidMappingsEnableSetgroups: false,
		GidMappings: []syscall.SysProcIDMap{{
			ContainerID: 0,
			HostID:      os.Getgid(),
			Size:        1,
		}},
	}

	err = cmd.Run()
	if err == nil {
		return 0, nil
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		if status, ok := exitErr.Sys().(syscall.WaitStatus); ok {
			return status.ExitStatus(), nil
		}
		return 1, nil
	}
	return -1, fmt.Errorf("failed to launch isolated process: %w", err)
}

// ChildMain runs inside the isolated child process and execs the requested command.
func ChildMain() int {
	spec, err := decodeSpec(os.Getenv(specEnvKey))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if err := syscall.Sethostname([]byte("docksmith")); err != nil {
		fmt.Fprintln(os.Stderr, "sethostname failed:", err)
		return 1
	}
	if err := syscall.Mount("", "/", "", syscall.MS_REC|syscall.MS_PRIVATE, ""); err != nil {
		fmt.Fprintln(os.Stderr, "mount propagation setup failed:", err)
		return 1
	}
	if err := syscall.Chroot(spec.RootFS); err != nil {
		fmt.Fprintln(os.Stderr, "chroot failed:", err)
		return 1
	}
	if err := syscall.Chdir("/"); err != nil {
		fmt.Fprintln(os.Stderr, "chdir(/) failed:", err)
		return 1
	}
	if err := os.MkdirAll("/proc", 0755); err != nil {
		fmt.Fprintln(os.Stderr, "mkdir /proc failed:", err)
		return 1
	}
	if err := syscall.Mount("proc", "/proc", "proc", 0, ""); err != nil {
		fmt.Fprintln(os.Stderr, "mount /proc failed:", err)
		return 1
	}
	defer syscall.Unmount("/proc", 0)

	if err := syscall.Chdir(spec.WorkingDir); err != nil {
		fmt.Fprintln(os.Stderr, "chdir working dir failed:", err)
		return 1
	}

	cmd := exec.Command(spec.Cmd[0], spec.Cmd[1:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = spec.Env
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			if status, ok := exitErr.Sys().(syscall.WaitStatus); ok {
				return status.ExitStatus()
			}
		}
		fmt.Fprintln(os.Stderr, "exec failed:", err)
		return 1
	}
	return 0
}

func encodeSpec(spec Spec) (string, error) {
	data, err := json.Marshal(spec)
	if err != nil {
		return "", fmt.Errorf("failed to serialize isolation spec: %w", err)
	}
	return base64.StdEncoding.EncodeToString(data), nil
}

func decodeSpec(encoded string) (Spec, error) {
	if encoded == "" {
		return Spec{}, fmt.Errorf("missing isolation spec")
	}
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return Spec{}, fmt.Errorf("invalid isolation spec encoding: %w", err)
	}
	var spec Spec
	if err := json.Unmarshal(data, &spec); err != nil {
		return Spec{}, fmt.Errorf("invalid isolation spec payload: %w", err)
	}
	if spec.RootFS == "" || len(spec.Cmd) == 0 {
		return Spec{}, fmt.Errorf("isolation spec missing required fields")
	}
	if spec.WorkingDir == "" {
		spec.WorkingDir = "/"
	}
	return spec, nil
}
