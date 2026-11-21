//go:build darwin && arm64

package appupdate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

const (
	bundleID       = "com.xiaohongshu.studio"
	zombieProcess  = 5 // XNU 的 SZOMB 状态。
	installerMount = "mount"
)

type bundleInfo struct {
	ID         string `json:"CFBundleIdentifier"`
	Executable string `json:"CFBundleExecutable"`
	Version    string `json:"CFBundleShortVersionString"`
}

func installedKind(hostExe, installer string) (InstallerKind, error) {
	bundle := applicationDir(hostExe)
	if installer != "" && installer != string(DMG) || !strings.EqualFold(filepath.Ext(bundle), ".app") || filepath.Dir(hostExe) != filepath.Join(bundle, "Contents", "MacOS") {
		return "", errUnsupportedInstaller
	}
	if _, err := inspectBundle(bundle, filepath.Base(hostExe), ""); err != nil {
		return "", err
	}
	if err := preflightInstall(hostExe); err != nil {
		return "", err
	}
	return DMG, nil
}

func applicationDir(hostExe string) string {
	return filepath.Dir(filepath.Dir(filepath.Dir(hostExe)))
}

func preflightInstall(hostExe string) error {
	bundle := applicationDir(hostExe)
	parent := filepath.Dir(bundle)
	if err := unix.Access(parent, unix.W_OK|unix.X_OK); err != nil {
		return fmt.Errorf("无法替换当前应用，请将应用复制到可写的应用程序目录: %w", err)
	}
	var info unix.Stat_t
	if err := unix.Stat(bundle, &info); err != nil {
		return err
	}
	if info.Flags&(unix.UF_IMMUTABLE|unix.SF_IMMUTABLE|unix.UF_APPEND|unix.SF_APPEND) != 0 {
		return errors.New("当前应用已锁定，无法替换")
	}
	return nil
}

func inspectBundle(bundle, executable, version string) (string, error) {
	if err := validateInstallerDir(filepath.Join(bundle, "Contents", "MacOS")); err != nil {
		return "", fmt.Errorf("应用包目录无效: %w", err)
	}
	plist := filepath.Join(bundle, "Contents", "Info.plist")
	info, err := os.Lstat(plist)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 1<<20 {
		return "", errors.New("应用包缺少有效的 Info.plist")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "/usr/bin/plutil", "-convert", "json", "-o", "-", "--", plist).Output()
	if err != nil {
		return "", fmt.Errorf("无法读取应用包信息: %w", err)
	}
	var metadata bundleInfo
	if err := json.Unmarshal(out, &metadata); err != nil {
		return "", err
	}
	if metadata.ID != bundleID || metadata.Executable != executable || version != "" && metadata.Version != version {
		return "", errors.New("应用包的标识、版本或可执行文件与发布记录不符")
	}
	path := filepath.Join(bundle, "Contents", "MacOS", executable)
	info, err = os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
		return "", errors.New("应用包缺少有效的桌面可执行文件")
	}
	return metadata.Version, nil
}

func secureInstallerDir(dir string) error {
	if err := validateInstallerDir(dir); err != nil {
		return err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if info.Sys().(*syscall.Stat_t).Uid != uint32(os.Geteuid()) || info.Mode().Perm()&0077 != 0 {
		return errors.New("更新目录必须仅供当前用户访问")
	}
	return nil
}

func openInstallerFile(path string) (*os.File, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	return regularInstallerFile(os.NewFile(uintptr(fd), path))
}

func startInstallerProcess(exe, jobPath string) error {
	cmd := launchInstallerCommand(exe, jobPath)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return startAndRelease(cmd)
}

func cleanupInstallerProcess(dir string) {
	_ = os.Remove(filepath.Join(dir, installerJobFile))
	_ = os.Remove(filepath.Join(dir, installerMount))
	_ = os.Remove(dir)
}

func reapInstallerProcesses(string) {}

func identifyProcess(pid int) (processIdentity, error) {
	info, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		if errors.Is(err, unix.EIO) || errors.Is(err, unix.ESRCH) || errors.Is(err, unix.ENOENT) {
			return processIdentity{}, os.ErrProcessDone
		}
		return processIdentity{}, err
	}
	if int(info.Proc.P_pid) != pid || info.Proc.P_stat == zombieProcess {
		return processIdentity{}, os.ErrProcessDone
	}
	start := info.Proc.P_starttime
	return processIdentity{PID: pid, Start: [2]uint64{uint64(start.Sec), uint64(start.Usec)}}, nil
}

func waitForExit(host, sidecar processIdentity) error {
	deadline := time.Now().Add(quitTimeout)
	for {
		running := false
		for _, expected := range [...]processIdentity{host, sidecar} {
			actual, err := identifyProcess(expected.PID)
			if err != nil && !errors.Is(err, os.ErrProcessDone) {
				return err
			}
			running = running || actual == expected
		}
		if !running {
			return nil
		}
		if time.Now().After(deadline) {
			return errors.New("等待当前应用及后端退出超时，请关闭应用后重新尝试")
		}
		time.Sleep(150 * time.Millisecond)
	}
}

func installPackage(job installerJob, image, workDir string) error {
	bundle := applicationDir(job.HostExe)
	executable := filepath.Base(job.HostExe)
	version := strings.TrimPrefix(job.Version, "v")
	mount := filepath.Join(workDir, installerMount)
	if err := os.Mkdir(mount, 0700); err != nil {
		return err
	}
	if err := macCommand(3*time.Minute, "/usr/bin/hdiutil", "attach", "-readonly", "-nobrowse", "-noautoopen", "-mountpoint", mount, image); err != nil {
		return fmt.Errorf("无法挂载安装磁盘映像: %w", err)
	}
	mounted := true
	defer func() {
		if mounted {
			_ = macCommand(30*time.Second, "/usr/bin/hdiutil", "detach", mount, "-force")
		}
	}()
	entries, err := os.ReadDir(mount)
	if err != nil {
		return err
	}
	var source string
	for _, entry := range entries {
		if entry.IsDir() && strings.EqualFold(filepath.Ext(entry.Name()), ".app") {
			if source != "" {
				return errors.New("安装磁盘映像包含多个应用包")
			}
			source = filepath.Join(mount, entry.Name())
		}
	}
	if source == "" {
		return errors.New("安装磁盘映像缺少应用包")
	}
	if _, err := inspectBundle(source, executable, version); err != nil {
		return err
	}
	stage, err := os.MkdirTemp(filepath.Dir(bundle), ".xiaohongshu-update-")
	if err != nil {
		return err
	}
	keepBackup := false
	defer func() {
		if !keepBackup {
			_ = os.RemoveAll(stage)
		}
	}()
	next := filepath.Join(stage, filepath.Base(bundle))
	if err := macCommand(5*time.Minute, "/usr/bin/ditto", source, next); err != nil {
		return fmt.Errorf("无法暂存新版应用: %w", err)
	}
	if _, err := inspectBundle(next, executable, version); err != nil {
		return err
	}
	if err := macCommand(30*time.Second, "/usr/bin/hdiutil", "detach", mount); err != nil {
		return fmt.Errorf("无法卸载安装磁盘映像: %w", err)
	}
	mounted = false
	// 同卷交换保留旧应用，失败时原应用仍可启动。
	if err := unix.RenameatxNp(unix.AT_FDCWD, next, unix.AT_FDCWD, bundle, unix.RENAME_SWAP); err != nil {
		return fmt.Errorf("无法原子替换应用包: %w", err)
	}
	if err := relaunchApp(job.HostExe); err != nil {
		launchErr := fmt.Errorf("无法打开新版应用: %w", err)
		if err := unix.RenameatxNp(unix.AT_FDCWD, next, unix.AT_FDCWD, bundle, unix.RENAME_SWAP); err != nil {
			keepBackup = true
			return errors.Join(launchErr, fmt.Errorf("无法还原应用，旧包保留在 %s: %w", next, err))
		}
		return launchErr
	}
	return nil
}

func relaunchApp(hostExe string) error {
	if _, err := os.Stat(hostExe); err != nil {
		return err
	}
	return macCommand(30*time.Second, "/usr/bin/open", "-n", applicationDir(hostExe))
}

func macCommand(timeout time.Duration, program string, args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, program, args...).CombinedOutput()
	if err != nil {
		if detail := strings.TrimSpace(string(out)); detail != "" {
			return fmt.Errorf("%w (%s)", err, detail)
		}
	}
	return err
}
