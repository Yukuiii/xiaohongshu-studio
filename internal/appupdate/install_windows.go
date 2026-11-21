//go:build windows && amd64

package appupdate

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const (
	windowsHostExe  = "xiaohongshu-studio-desktop.exe"
	windowsProduct  = "Xiaohongshu Studio"
	uninstallKey    = `Software\Microsoft\Windows\CurrentVersion\Uninstall`
	installerHelper = "installer-helper.exe"
	installerDone   = "done"
)

func installedKind(hostExe, installer string) (InstallerKind, error) {
	if !strings.EqualFold(filepath.Base(hostExe), windowsHostExe) {
		return "", errUnsupportedInstaller
	}
	info, err := os.Lstat(hostExe)
	if err != nil || !info.Mode().IsRegular() {
		return "", errUnsupportedInstaller
	}
	dir := applicationDir(hostExe)
	switch InstallerKind(installer) {
	case NSIS:
		// 官方 NSIS 包为当前用户安装，注册表须指向正在运行的副本。
		key, err := registry.OpenKey(registry.CURRENT_USER, uninstallKey+`\`+windowsProduct, registry.QUERY_VALUE)
		if err != nil {
			return "", errUnsupportedInstaller
		}
		defer key.Close()
		if registeredInstallDir(key, dir) {
			return NSIS, nil
		}
	case MSI:
		// 官方 MSI 包为整机安装，保留原注册目录和安装范围。
		root, err := registry.OpenKey(registry.LOCAL_MACHINE, uninstallKey, registry.ENUMERATE_SUB_KEYS|registry.WOW64_64KEY)
		if err != nil {
			return "", errUnsupportedInstaller
		}
		defer root.Close()
		names, err := root.ReadSubKeyNames(-1)
		if err != nil {
			return "", err
		}
		for _, name := range names {
			if len(name) != 38 || name[0] != '{' || name[37] != '}' {
				continue
			}
			key, err := registry.OpenKey(root, name, registry.QUERY_VALUE|registry.WOW64_64KEY)
			if err != nil {
				continue
			}
			product, _, _ := key.GetStringValue("DisplayName")
			msi, _, _ := key.GetIntegerValue("WindowsInstaller")
			matches := product == windowsProduct && msi == 1 && registeredInstallDir(key, dir)
			_ = key.Close()
			if matches {
				return MSI, nil
			}
		}
	}
	return "", errUnsupportedInstaller
}

func registeredInstallDir(key registry.Key, dir string) bool {
	value, kind, err := key.GetStringValue("InstallLocation")
	if err != nil {
		return false
	}
	if kind == registry.EXPAND_SZ {
		value, err = registry.ExpandString(value)
		if err != nil {
			return false
		}
	}
	path, err := filepath.EvalSymlinks(strings.Trim(value, `"`))
	return err == nil && samePath(dir, path)
}

func applicationDir(hostExe string) string { return filepath.Dir(hostExe) }

func secureInstallerDir(dir string) error {
	if err := validateInstallerDir(dir); err != nil {
		return err
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return err
	}
	ownerSD, err := windows.GetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION)
	if err != nil {
		return err
	}
	owner, _, err := ownerSD.Owner()
	if err != nil || !user.User.Sid.Equals(owner) {
		return errors.New("更新目录须由当前用户拥有")
	}
	// 管理员与系统必须能读取经校验的 MSI 安装包。
	sd, err := windows.SecurityDescriptorFromString("D:P(A;OICI;FA;;;" + user.User.Sid.String() + ")(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)")
	if err != nil {
		return err
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil)
}

func openInstallerFile(path string) (*os.File, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	attrs, err := windows.GetFileAttributes(name)
	if err != nil {
		return nil, err
	}
	if attrs&(windows.FILE_ATTRIBUTE_REPARSE_POINT|windows.FILE_ATTRIBUTE_DIRECTORY) != 0 {
		return nil, errors.New("安装文件类型无效")
	}
	// 校验和安装期间禁止写入、删除和替换安装文件。
	handle, err := windows.CreateFile(name, windows.GENERIC_READ, windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, err
	}
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil || info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		_ = windows.CloseHandle(handle)
		return nil, errors.New("安装文件类型无效")
	}
	return regularInstallerFile(os.NewFile(uintptr(handle), path))
}

func startInstallerProcess(exe, jobPath string) error {
	// Windows 锁定运行中的可执行文件，先将后端复制到应用目录之外。
	helper := filepath.Join(filepath.Dir(jobPath), installerHelper)
	src, err := os.Open(exe)
	if err != nil {
		return err
	}
	defer src.Close()
	dst, err := os.OpenFile(helper, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0700)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(dst, src)
	if err := errors.Join(copyErr, dst.Close()); err != nil {
		return err
	}
	cmd := launchInstallerCommand(helper, jobPath)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW}
	return startAndRelease(cmd)
}

func cleanupInstallerProcess(dir string) {
	_ = os.Remove(filepath.Join(dir, installerJobFile))
	_ = os.WriteFile(filepath.Join(dir, installerDone), nil, 0600)
}

func reapInstallerProcesses(root string) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() || !tempDirName(entry.Name(), "install-") {
			continue
		}
		dir := filepath.Join(root, entry.Name())
		if _, err := os.Lstat(filepath.Join(dir, installerDone)); err != nil {
			continue
		}
		if err := validateInstallerDir(dir); err != nil {
			continue
		}
		if err := os.Remove(filepath.Join(dir, installerHelper)); err == nil || errors.Is(err, os.ErrNotExist) {
			_ = os.RemoveAll(dir)
		}
	}
}

func identifyProcess(pid int) (processIdentity, error) {
	handle, err := openProcess(pid)
	if err != nil {
		return processIdentity{}, err
	}
	defer windows.CloseHandle(handle)
	return processStart(handle, pid)
}

func openProcess(pid int) (windows.Handle, error) {
	if uint64(pid) > uint64(^uint32(0)) {
		return 0, errors.New("进程 ID 无效")
	}
	handle, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
		return 0, os.ErrProcessDone
	}
	return handle, err
}

func processStart(handle windows.Handle, pid int) (processIdentity, error) {
	var start, exit, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(handle, &start, &exit, &kernel, &user); err != nil {
		return processIdentity{}, err
	}
	return processIdentity{PID: pid, Start: [2]uint64{uint64(start.HighDateTime)<<32 | uint64(start.LowDateTime)}}, nil
}

func waitForExit(host, sidecar processIdentity) error {
	var storage [2]windows.Handle
	handles := storage[:0]
	for _, expected := range [...]processIdentity{host, sidecar} {
		handle, err := openProcess(expected.PID)
		if errors.Is(err, os.ErrProcessDone) {
			continue
		}
		if err != nil {
			return err
		}
		defer windows.CloseHandle(handle)
		actual, err := processStart(handle, expected.PID)
		if err != nil {
			return err
		}
		if actual == expected {
			handles = append(handles, handle)
		}
	}
	if len(handles) == 0 {
		return nil
	}
	result, err := windows.WaitForMultipleObjects(handles, true, uint32(quitTimeout.Milliseconds()))
	if err != nil {
		return err
	}
	if result != windows.WAIT_OBJECT_0 {
		return errors.New("等待当前应用及后端退出超时，请关闭应用后重新尝试")
	}
	return nil
}

func installPackage(job installerJob, path, workDir string) error {
	dir := applicationDir(job.HostExe)
	var cmd *exec.Cmd
	switch job.Kind {
	case NSIS:
		cmd = exec.Command(path)
		// NSIS 要求 /D= 在命令行末尾且路径不加引号。
		cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW, CmdLine: windows.EscapeArg(path) + " /S /UPDATE /D=" + dir}
	case MSI:
		system, err := windows.GetSystemDirectory()
		if err != nil {
			return err
		}
		// 由 Windows Installer 处理 UAC，更新进程保持当前用户身份。
		cmd = exec.Command(filepath.Join(system, "msiexec.exe"), "/i", path, "/passive", "/norestart", "ALLUSERS=1", "INSTALLDIR="+dir)
		cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW}
	}
	cmd.Dir = workDir
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if job.Kind == MSI && errors.As(err, &exit) && (exit.ExitCode() == int(windows.ERROR_SUCCESS_REBOOT_REQUIRED) || exit.ExitCode() == int(windows.ERROR_SUCCESS_REBOOT_INITIATED)) {
			return errRestartRequired
		}
		return fmt.Errorf("Windows 安装程序未完成更新: %w", err)
	}
	return relaunchApp(job.HostExe)
}

func relaunchApp(hostExe string) error {
	cmd := exec.Command(hostExe)
	cmd.Dir = applicationDir(hostExe)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW}
	return startAndRelease(cmd)
}
