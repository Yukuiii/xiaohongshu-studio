//go:build !(darwin && arm64) && !(windows && amd64)

package appupdate

import "os"

func installedKind(string, string) (InstallerKind, error) { return "", errUnsupportedInstaller }
func applicationDir(string) string                        { return "" }
func secureInstallerDir(string) error                     { return errUnsupportedInstaller }
func openInstallerFile(string) (*os.File, error)          { return nil, errUnsupportedInstaller }
func startInstallerProcess(string, string) error          { return errUnsupportedInstaller }
func cleanupInstallerProcess(string)                      {}
func reapInstallerProcesses(string)                       {}
func identifyProcess(int) (processIdentity, error)        { return processIdentity{}, errUnsupportedInstaller }
func waitForExit(processIdentity, processIdentity) error  { return errUnsupportedInstaller }
func installPackage(installerJob, string, string) error   { return errUnsupportedInstaller }
func relaunchApp(string) error                            { return errUnsupportedInstaller }
