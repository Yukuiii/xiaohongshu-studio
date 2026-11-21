package appupdate

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	installerCommand   = "--install-desktop-update"
	installerJobFile   = "job.json"
	installerErrorFile = "install-error"
	maxJobSize         = 16 << 10
	quitTimeout        = 45 * time.Second
)

var (
	errUnsupportedInstaller = errors.New("当前应用安装方式不支持自动更新")
	errRestartRequired      = errors.New("Windows 安装程序要求重启系统以完成更新")
)

type processIdentity struct {
	PID   int
	Start [2]uint64
}

type installerJob struct {
	HostExe string
	Host    processIdentity
	Sidecar processIdentity
	Package string
	Kind    InstallerKind
	Version string
	Size    int64
	Digest  [sha256.Size]byte
}

// InstalledKind 返回与当前桌面应用匹配的安装包类型。
func InstalledKind(hostExe, installer string) (InstallerKind, error) {
	if !filepath.IsAbs(hostExe) {
		return "", errUnsupportedInstaller
	}
	hostExe, err := filepath.EvalSymlinks(hostExe)
	if err != nil {
		return "", fmt.Errorf("无法定位已安装的桌面应用: %w", err)
	}
	return installedKind(hostExe, installer)
}

// StartInstaller 启动脱离当前应用的安装进程，保留安装包直到该进程结束。
func StartInstaller(dataDir, hostExe, installerPath string, kind InstallerKind, release Release, hostPID int) error {
	if !filepath.IsAbs(hostExe) || hostPID <= 1 || hostPID == os.Getpid() {
		return errUnsupportedInstaller
	}
	hostExe, err := filepath.EvalSymlinks(hostExe)
	if err != nil {
		return err
	}
	if _, err := installedKind(hostExe, string(kind)); err != nil {
		return err
	}
	root, err := updateRoot(dataDir)
	if err != nil {
		return err
	}
	if err := secureInstallerDir(root); err != nil {
		return err
	}
	if updateInsideApp(hostExe, root) {
		return errors.New("更新目录必须位于应用安装目录之外")
	}
	installerPath, err = filepath.Abs(installerPath)
	if err != nil {
		return err
	}
	packageName := filepath.Base(filepath.Dir(installerPath))
	assetRoot, err := filepath.EvalSymlinks(filepath.Dir(filepath.Dir(installerPath)))
	if err != nil {
		return err
	}
	job := installerJob{HostExe: hostExe, Package: packageName, Kind: kind, Version: release.Version, Size: release.Size, Digest: release.Digest}
	if !job.validAsset() || !samePath(assetRoot, root) || release.Name != assetName(job.Version, job.Kind) || filepath.Base(installerPath) != release.Name {
		return errors.New("安装包路径或发布记录无效")
	}
	packageDir := filepath.Join(root, packageName)
	installerPath = filepath.Join(packageDir, release.Name)
	if err := secureInstallerDir(packageDir); err != nil {
		return err
	}
	info, err := os.Lstat(installerPath)
	if err != nil || !info.Mode().IsRegular() || info.Size() != release.Size {
		return errors.New("下载的安装包不存在或大小已改变")
	}
	if job.Host, err = identifyProcess(hostPID); err != nil {
		return fmt.Errorf("无法确认桌面应用进程: %w", err)
	}
	if job.Sidecar, err = identifyProcess(os.Getpid()); err != nil {
		return fmt.Errorf("无法确认后端进程: %w", err)
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	dir, err := os.MkdirTemp(root, "install-")
	if err != nil {
		return err
	}
	started := false
	defer func() {
		if !started {
			_ = os.RemoveAll(dir)
		}
	}()
	if err := secureInstallerDir(dir); err != nil {
		return err
	}
	payload, err := json.Marshal(job)
	if err != nil {
		return err
	}
	jobPath := filepath.Join(dir, installerJobFile)
	if err := os.WriteFile(jobPath, payload, 0600); err != nil {
		return err
	}
	if err := os.Remove(filepath.Join(root, installerErrorFile)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := startInstallerProcess(self, jobPath); err != nil {
		return fmt.Errorf("无法启动安装进程: %w", err)
	}
	started = true
	return nil
}

// RunInstaller 等待应用退出，重新校验安装包并执行系统安装。
func RunInstaller(jobPath string) error {
	dir := filepath.Dir(jobPath)
	root := filepath.Dir(dir)
	if !filepath.IsAbs(jobPath) || filepath.Clean(jobPath) != jobPath || filepath.Base(jobPath) != installerJobFile || filepath.Base(root) != "updates" || !tempDirName(filepath.Base(dir), "install-") {
		return errors.New("安装任务路径无效")
	}
	if err := secureInstallerDir(root); err != nil {
		return err
	}
	if err := secureInstallerDir(dir); err != nil {
		return err
	}
	var job installerJob
	file, err := openInstallerFile(jobPath)
	if err != nil {
		return err
	}
	info, err := file.Stat()
	if err != nil || info.Size() > maxJobSize {
		_ = file.Close()
		return errors.New("安装任务大小无效")
	}
	decoder := json.NewDecoder(io.LimitReader(file, maxJobSize))
	decoder.DisallowUnknownFields()
	decodeErr := decoder.Decode(&job)
	if decodeErr == nil {
		var extra json.RawMessage
		if decoder.Decode(&extra) != io.EOF {
			decodeErr = errors.New("安装任务包含多余内容")
		}
	}
	if err := errors.Join(decodeErr, file.Close()); err != nil {
		return fmt.Errorf("无法读取安装任务: %w", err)
	}
	if !job.validAsset() || !filepath.IsAbs(job.HostExe) || job.HostExe != filepath.Clean(job.HostExe) || updateInsideApp(job.HostExe, root) || job.Host.PID <= 1 || job.Sidecar.PID <= 1 || job.Host.PID == job.Sidecar.PID || job.Host.Start == [2]uint64{} || job.Sidecar.Start == [2]uint64{} || job.Host.PID == os.Getpid() || job.Sidecar.PID == os.Getpid() {
		return errors.New("安装任务内容无效")
	}
	defer cleanupInstallerProcess(dir)
	packageDir := filepath.Join(root, job.Package)
	packageErr := secureInstallerDir(packageDir)
	if packageErr == nil {
		defer os.RemoveAll(packageDir)
	}
	if err := waitForExit(job.Host, job.Sidecar); err != nil {
		return recordInstallError(root, job.Version, err)
	}
	_, installErr := installedKind(job.HostExe, string(job.Kind))
	if installErr == nil {
		installErr = packageErr
	}
	if installErr == nil {
		installErr = verifyAndInstall(job, packageDir, dir)
	}
	if installErr == nil {
		return nil
	}
	installErr = recordInstallError(root, job.Version, installErr)
	if errors.Is(installErr, errRestartRequired) {
		return installErr
	}
	if err := relaunchApp(job.HostExe); err != nil {
		return recordInstallError(root, job.Version, errors.Join(installErr, fmt.Errorf("无法重新打开桌面应用: %w", err)))
	}
	return installErr
}

func verifyAndInstall(job installerJob, packageDir, workDir string) error {
	path := filepath.Join(packageDir, assetName(job.Version, job.Kind))
	file, err := openInstallerFile(path)
	if err != nil {
		return err
	}
	defer file.Close()
	hash := sha256.New()
	written, err := io.Copy(hash, io.LimitReader(file, job.Size+1))
	if err != nil {
		return err
	}
	var digest [sha256.Size]byte
	_ = hash.Sum(digest[:0])
	if written != job.Size || digest != job.Digest {
		return errors.New("安装包大小或 SHA-256 校验失败，请重新下载")
	}
	return installPackage(job, path, workDir)
}

func (j installerJob) validAsset() bool {
	return tempDirName(j.Package, "package-") && validTag(j.Version) && assetName(j.Version, j.Kind) != "" && j.Size > 0 && j.Size <= maxAssetSize && j.Digest != [sha256.Size]byte{}
}

func tempDirName(name, prefix string) bool {
	suffix, ok := strings.CutPrefix(name, prefix)
	return ok && suffix != "" && strings.Trim(suffix, "0123456789") == ""
}

func samePath(a, b string) bool {
	return a == b || runtime.GOOS == "windows" && strings.EqualFold(a, b)
}

func updateInsideApp(hostExe, root string) bool {
	rel, err := filepath.Rel(applicationDir(hostExe), root)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func updateRoot(dataDir string) (string, error) {
	abs, err := filepath.Abs(dataDir)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", err
	}
	root := filepath.Join(resolved, "updates")
	if err := validateInstallerDir(root); err != nil {
		return "", err
	}
	return root, nil
}

func validateInstallerDir(dir string) error {
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() || !samePath(dir, resolved) {
		return errors.New("更新目录必须是真实目录")
	}
	return nil
}

func regularInstallerFile(file *os.File) (*os.File, error) {
	info, err := file.Stat()
	if err == nil && info.Mode().IsRegular() {
		return file, nil
	}
	_ = file.Close()
	if err != nil {
		return nil, err
	}
	return nil, errors.New("安装文件类型无效")
}

func launchInstallerCommand(exe, jobPath string) *exec.Cmd {
	cmd := exec.Command(exe, installerCommand, jobPath)
	cmd.Dir = filepath.Dir(jobPath)
	return cmd
}

func startAndRelease(cmd *exec.Cmd) error {
	if err := cmd.Start(); err != nil {
		return err
	}
	_ = cmd.Process.Release()
	return nil
}

func recordInstallError(root, version string, cause error) error {
	if err := writeInstallError(root, "安装 "+version+" 失败："+cause.Error()); err != nil {
		return errors.Join(cause, err)
	}
	return cause
}

func writeInstallError(root, message string) error {
	file, err := os.CreateTemp(root, ".install-error-")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	_, err = file.WriteString(message)
	if err := errors.Join(err, file.Close()); err != nil {
		return err
	}
	return os.Rename(file.Name(), filepath.Join(root, installerErrorFile))
}

// LastInstallError 返回最近一次桌面更新的失败原因。
func LastInstallError(dataDir string) string {
	root, err := updateRoot(dataDir)
	if err != nil {
		return ""
	}
	reapInstallerProcesses(root)
	file, err := openInstallerFile(filepath.Join(root, installerErrorFile))
	if err != nil {
		return ""
	}
	defer file.Close()
	text, err := io.ReadAll(io.LimitReader(file, maxJobSize))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(text))
}
